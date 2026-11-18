package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// The two ways bubblewrap can be unavailable on Linux.
var (
	ErrNotFound    = errors.New("bubblewrap (bwrap) was not found on the PATH")
	ErrCannotStart = errors.New("bubblewrap could not start a sandbox")
)

const (
	bwrapProgram = "bwrap"
	probeTimeout = 10 * time.Second
	// maxProbeLine bounds what a failed probe quotes of bubblewrap's output.
	maxProbeLine = 200
)

// Bubblewrap is the Linux backend: the bwrap program with user namespaces.
type Bubblewrap struct {
	lookPath func(string) (string, error)
	probe    []string
}

// NewBubblewrap returns the backend. lookPath finds the program, as
// exec.LookPath does, and probe is the command a Probe runs inside.
func NewBubblewrap(lookPath func(string) (string, error), probe []string) *Bubblewrap {
	return &Bubblewrap{lookPath: lookPath, probe: probe}
}

// Name is the backend's name in records and messages.
func (*Bubblewrap) Name() string { return "bubblewrap" }

// Path is the program with its links followed, or "" when there is none. It
// is looked up on every call, so a program installed later is found.
func (b *Bubblewrap) Path() string {
	path, err := b.lookPath(bwrapProgram)
	if err != nil {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	return path
}

// Probe starts a sandbox over a temporary directory and runs the probe
// command in it. The program's version says nothing: on a host that denies
// user namespaces it prints one and still cannot start.
func (b *Bubblewrap) Probe(ctx context.Context) error {
	path := b.Path()
	if path == "" {
		return ErrNotFound
	}
	dir, err := os.MkdirTemp("", "rune-sandbox-probe-")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrCannotStart, err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	spec := Spec{Workspace: filepath.Join(Resolve(dir), "work"), Tmp: filepath.Join(Resolve(dir), "tmp"), VarRunDir: VarRunDir()}
	for _, d := range []string{spec.Workspace, spec.Tmp} {
		if err := os.Mkdir(d, 0o700); err != nil {
			return fmt.Errorf("%w: %w", ErrCannotStart, err)
		}
	}
	if len(b.probe) > 0 && filepath.IsAbs(b.probe[0]) {
		spec.Exe = b.probe[0]
	}

	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, BwrapArgs(spec, b.probe)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", ErrCannotStart, probeReason(stderr.String(), err))
	}
	return nil
}

// probeReason is the first line bubblewrap printed, bounded, or how the
// command ended when it printed nothing.
func probeReason(stderr string, err error) string {
	for line := range strings.Lines(stderr) {
		if line = strings.TrimSpace(line); line != "" {
			if len(line) > maxProbeLine {
				line = strings.ToValidUTF8(line[:maxProbeLine], "")
			}
			return line
		}
	}
	return err.Error()
}

// Command returns the command that runs argv confined by spec.
func (b *Bubblewrap) Command(ctx context.Context, spec Spec, argv []string) (*exec.Cmd, error) {
	path := b.Path()
	if path == "" {
		return nil, ErrNotFound
	}
	return exec.CommandContext(ctx, path, BwrapArgs(spec, argv)...), nil
}

// VarRunDir reports whether /var/run is a directory of its own. On current
// systems it is a link to /run and is hidden with it.
func VarRunDir() bool {
	info, err := os.Lstat("/var/run")
	return err == nil && info.IsDir()
}

// BwrapArgs is the argument list that runs argv under spec. The order is the
// contract: a later mount covers an earlier one, so the workspace is visible
// again where a hidden directory covered it, a hidden location inside the
// workspace is mounted after the workspace, and the read-only remounts come
// after every mount that needs a mount point inside them.
func BwrapArgs(spec Spec, argv []string) []string {
	args := []string{"--unshare-all"}
	if spec.Network {
		args = append(args, "--share-net")
	}
	args = append(args,
		// The body dies with the server, has no terminal to write into, and
		// keeps no capability when the server runs as root.
		"--die-with-parent", "--new-session", "--cap-drop", "ALL",
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--tmpfs", "/run",
	)
	remount := []string{"/run"}
	if spec.VarRunDir {
		args = append(args, "--tmpfs", "/var/run")
		remount = append(remount, "/var/run")
	}
	args = append(args, "--tmpfs", "/var/tmp")
	remount = append(remount, "/var/tmp")
	if spec.Tmp != "" {
		args = append(args, "--bind", spec.Tmp, "/tmp")
	} else {
		args = append(args, "--tmpfs", "/tmp")
		remount = append(remount, "/tmp")
	}

	inside := func(path string) bool { return spec.Workspace != "" && Within(spec.Workspace, path) }
	empty := spec.Empty
	if empty == "" {
		empty = "/dev/null"
	}
	hide := func(in bool) {
		for _, dir := range sorted(spec.HiddenDirs) {
			if inside(dir) == in {
				args = append(args, "--tmpfs", dir)
			}
		}
		for _, file := range sorted(spec.HiddenFiles) {
			if inside(file) == in {
				args = append(args, "--ro-bind", empty, file)
			}
		}
	}
	hide(false)

	if spec.Workspace != "" {
		bind := "--bind"
		if spec.ReadOnly {
			bind = "--ro-bind"
		}
		args = append(args, bind, spec.Workspace, spec.Workspace)
	}
	if !spec.ReadOnly {
		protected := outermost(spec.Protected)
		var pins []string
		for _, path := range protected {
			pins = append(pins, Ancestors(spec.Workspace, path)...)
		}
		// A directory that is a mount point cannot be renamed, which keeps
		// the protected file below it where the Runefile looks for it.
		for _, dir := range sorted(pins) {
			args = append(args, "--bind", dir, dir)
		}
		for _, path := range protected {
			args = append(args, "--ro-bind", path, path)
		}
	}
	hide(true)

	if spec.Cache != "" && !spec.ReadOnly {
		args = append(args, "--bind", spec.Cache, spec.Cache)
	}
	if spec.Network && spec.ResolvConf != "" {
		args = append(args, "--ro-bind", spec.ResolvConf, spec.ResolvConf)
	}
	if spec.Exe != "" {
		args = append(args, "--ro-bind", spec.Exe, spec.Exe)
	}

	// A tmpfs is writable when made. Remounting it read-only leaves a hidden
	// location that takes no write and no memory.
	for _, dir := range remount {
		args = append(args, "--remount-ro", dir)
	}
	for _, dir := range sorted(spec.HiddenDirs) {
		args = append(args, "--remount-ro", dir)
	}

	dir := spec.Workspace
	if dir == "" {
		dir = "/"
	}
	args = append(args, "--chdir", dir, "--")
	return append(args, argv...)
}

// sorted returns a sorted copy without duplicates.
func sorted(paths []string) []string {
	out := slices.Clone(paths)
	slices.Sort(out)
	return slices.Compact(out)
}

// outermost returns the sorted paths that lie under no other path of the
// list. A bind inside a read-only directory would open that directory again.
func outermost(paths []string) []string {
	all := sorted(paths)
	return slices.DeleteFunc(slices.Clone(all), func(path string) bool {
		return slices.ContainsFunc(all, func(other string) bool {
			return other != path && Within(other, path)
		})
	})
}
