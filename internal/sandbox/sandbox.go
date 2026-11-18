// Package sandbox confines a command with what the operating system offers:
// no network unless asked for, a read-only view of the host, and one writable
// directory. It knows nothing about tasks. The engine turns a task body into
// a set of paths and a network flag (a Spec), and a Backend turns that into a
// command (spec 053).
//
// The only backend is bubblewrap, on Linux. Every other system has none, and
// says so: a caller never gets a command that looks confined and is not.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// Mode is the operator's choice for a server.
type Mode string

// The modes. There is no automatic one: confining where it works and running
// unconfined elsewhere would be a silent fallback.
const (
	ModeEnforce Mode = "enforce"
	ModeOff     Mode = "off"
)

// ParseMode reads a mode as the operator wrote it.
func ParseMode(value string) (Mode, error) {
	switch Mode(value) {
	case ModeEnforce, ModeOff:
		return Mode(value), nil
	}
	return "", fmt.Errorf("%q is not a mode; use enforce or off", value)
}

// Spec is the rule set for one command. Every path is absolute and resolved,
// and exists: the caller chooses and classifies them, and nothing here reads
// a file.
type Spec struct {
	// Workspace is the directory the command may write, and where it starts.
	Workspace string
	// ReadOnly makes the workspace read-only as well: nothing is writable.
	ReadOnly bool
	// Protected are paths inside the workspace that stay readable and cannot
	// be changed, removed, renamed or replaced.
	Protected []string
	// HiddenDirs and HiddenFiles are seen empty and read-only.
	HiddenDirs, HiddenFiles []string
	// Empty is an empty file that stands in for each hidden file. A tool
	// that insists on reading its configuration then reads nothing, where
	// the null device, which is used when Empty is not set, cannot be opened
	// at all under a mount that allows no devices.
	Empty string
	// Tmp is bound at /tmp. Empty gives an empty read-only /tmp.
	Tmp string
	// Cache is a second writable directory, bound at its own path.
	Cache string
	// Network keeps the host's network. Without it the command has a
	// loopback of its own and nothing else.
	Network bool
	// ResolvConf is the resolver file to bind back for a command with the
	// network, when it lies under a hidden directory.
	ResolvConf string
	// VarRunDir says /var/run is a directory of its own, not a link to /run.
	VarRunDir bool
	// Exe is a program to keep reachable at its own path, wherever it lies.
	Exe string
}

// Backend provides the sandbox on a host.
type Backend interface {
	// Name is recorded in the audit log and shown to the operator. A host
	// with no backend has the empty name.
	Name() string
	// Path is the resolved program the backend runs, or "".
	Path() string
	// Probe starts a real sandbox. Nil means the backend can confine.
	Probe(ctx context.Context) error
	// Command returns the command that runs argv under the rules of spec.
	Command(ctx context.Context, spec Spec, argv []string) (*exec.Cmd, error)
}

// Detect returns the backend of an operating system. probe is the command a
// Probe runs inside the sandbox: it must exit 0.
func Detect(goos string, probe []string) Backend {
	if goos == "linux" {
		return NewBubblewrap(exec.LookPath, probe)
	}
	return unsupported{goos: goos}
}

// ErrUnsupported is the reason of a system with no backend.
var ErrUnsupported = errors.New("no sandbox backend")

// unsupported is the backend of a system that has none.
type unsupported struct{ goos string }

func (unsupported) Name() string { return "" }
func (unsupported) Path() string { return "" }

func (u unsupported) Probe(context.Context) error {
	// The Runefile's word for the system, as in an OS attribute.
	name := u.goos
	if name == "darwin" {
		name = "macos"
	}
	return fmt.Errorf("this system (%s) has %w", name, ErrUnsupported)
}

func (u unsupported) Command(ctx context.Context, _ Spec, _ []string) (*exec.Cmd, error) {
	return nil, u.Probe(ctx)
}
