//go:build unix

package sandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// requireSandbox skips a test on a host where bubblewrap cannot start a
// sandbox. With RUNE_TEST_SANDBOX=require it fails instead, so that a host
// which is meant to run these tests cannot skip them silently.
func requireSandbox(t *testing.T) *Bubblewrap {
	t.Helper()
	b := NewBubblewrap(exec.LookPath, []string{os.Args[0], "-test.run=^$"})
	if err := b.Probe(context.Background()); err != nil {
		if os.Getenv("RUNE_TEST_SANDBOX") == "require" {
			t.Fatalf("a sandbox is required here: %v", err)
		}
		t.Skipf("no sandbox on this host: %v", err)
	}
	return b
}

// fixtureDir is a directory for a test that starts a real sandbox. It is
// never under /tmp, which the sandbox replaces.
func fixtureDir(t *testing.T) string {
	t.Helper()
	base := os.Getenv("RUNE_TEST_SANDBOX_DIR")
	if base == "" {
		wd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		base = filepath.Join(wd, "..", "..", ".rune", "sandbox-test")
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(base, "sandbox-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

// head is how every argument list starts, with or without the network.
func head(network bool) []string {
	args := []string{"--unshare-all"}
	if network {
		args = append(args, "--share-net")
	}
	return append(args, "--die-with-parent", "--new-session", "--cap-drop", "ALL",
		"--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc",
		"--tmpfs", "/run", "--tmpfs", "/var/tmp")
}

func TestBwrapArgs(t *testing.T) {
	argv := []string{"/bin/rune", "--sandbox-worker"}
	tail := func(remounts ...string) []string {
		args := []string{"--remount-ro", "/run", "--remount-ro", "/var/tmp"}
		for _, dir := range remounts {
			args = append(args, "--remount-ro", dir)
		}
		return args
	}
	end := func(dir string) []string { return append([]string{"--chdir", dir, "--"}, argv...) }
	join := func(parts ...[]string) []string { return slices.Concat(parts...) }

	cases := []struct {
		name string
		spec Spec
		want []string
	}{
		{
			name: "plain",
			spec: Spec{Workspace: "/ws", Tmp: "/priv/tmp", Cache: "/priv/cache", Exe: "/bin/rune"},
			want: join(head(false),
				[]string{"--bind", "/priv/tmp", "/tmp", "--bind", "/ws", "/ws", "--bind", "/priv/cache", "/priv/cache", "--ro-bind", "/bin/rune", "/bin/rune"},
				tail(), end("/ws")),
		},
		{
			name: "network with a resolver file",
			spec: Spec{Workspace: "/ws", Tmp: "/priv/tmp", Cache: "/priv/cache", Exe: "/bin/rune", Network: true, ResolvConf: "/run/systemd/resolve/stub-resolv.conf"},
			want: join(head(true),
				[]string{
					"--bind", "/priv/tmp", "/tmp", "--bind", "/ws", "/ws", "--bind", "/priv/cache", "/priv/cache",
					"--ro-bind", "/run/systemd/resolve/stub-resolv.conf", "/run/systemd/resolve/stub-resolv.conf",
					"--ro-bind", "/bin/rune", "/bin/rune",
				},
				tail(), end("/ws")),
		},
		{
			name: "a resolver file without the network is left out",
			spec: Spec{Workspace: "/ws", Tmp: "/priv/tmp", ResolvConf: "/run/r.conf"},
			want: join(head(false), []string{"--bind", "/priv/tmp", "/tmp", "--bind", "/ws", "/ws"}, tail(), end("/ws")),
		},
		{
			name: "protected paths and their ancestors",
			spec: Spec{
				Workspace: "/ws", Tmp: "/priv/tmp",
				Protected: []string{"/ws/sub/deep/t.rune", "/ws/Runefile", "/ws/.rune", "/ws/.git", "/ws/sub/deep/u.rune", "/ws/.rune/plans/x"},
			},
			want: join(head(false),
				[]string{
					"--bind", "/priv/tmp", "/tmp", "--bind", "/ws", "/ws",
					"--bind", "/ws/sub", "/ws/sub", "--bind", "/ws/sub/deep", "/ws/sub/deep",
					"--ro-bind", "/ws/.git", "/ws/.git", "--ro-bind", "/ws/.rune", "/ws/.rune",
					"--ro-bind", "/ws/Runefile", "/ws/Runefile",
					"--ro-bind", "/ws/sub/deep/t.rune", "/ws/sub/deep/t.rune",
					"--ro-bind", "/ws/sub/deep/u.rune", "/ws/sub/deep/u.rune",
				},
				tail(), end("/ws")),
		},
		{
			name: "hidden locations outside and inside the workspace",
			spec: Spec{
				Workspace: "/home/u", Tmp: "/priv/tmp", VarRunDir: true,
				HiddenDirs:  []string{"/home/u/.ssh", "/var/audit", "/home/u/.aws"},
				HiddenFiles: []string{"/home/u/.netrc", "/srv/agent.sock"},
				Protected:   []string{"/home/u/Runefile"},
			},
			want: join(
				[]string{
					"--unshare-all", "--die-with-parent", "--new-session", "--cap-drop", "ALL",
					"--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc",
					"--tmpfs", "/run", "--tmpfs", "/var/run", "--tmpfs", "/var/tmp",
				},
				[]string{
					"--bind", "/priv/tmp", "/tmp",
					"--tmpfs", "/var/audit",
					"--ro-bind", "/dev/null", "/srv/agent.sock",
					"--bind", "/home/u", "/home/u",
					"--ro-bind", "/home/u/Runefile", "/home/u/Runefile",
					"--tmpfs", "/home/u/.aws", "--tmpfs", "/home/u/.ssh",
					"--ro-bind", "/dev/null", "/home/u/.netrc",
					"--remount-ro", "/run", "--remount-ro", "/var/run", "--remount-ro", "/var/tmp",
					"--remount-ro", "/home/u/.aws", "--remount-ro", "/home/u/.ssh", "--remount-ro", "/var/audit",
				},
				end("/home/u")),
		},
		{
			name: "hidden files behind an empty file",
			spec: Spec{Workspace: "/ws", Tmp: "/priv/tmp", HiddenFiles: []string{"/home/u/.npmrc"}, Empty: "/priv/empty"},
			want: join(head(false),
				[]string{"--bind", "/priv/tmp", "/tmp", "--ro-bind", "/priv/empty", "/home/u/.npmrc", "--bind", "/ws", "/ws"},
				tail(), end("/ws")),
		},
		{
			name: "read only",
			spec: Spec{Workspace: "/repo", ReadOnly: true, Protected: []string{"/repo/sub/x"}, Cache: "/priv/cache"},
			want: join(head(false),
				[]string{"--tmpfs", "/tmp", "--ro-bind", "/repo", "/repo"},
				[]string{"--remount-ro", "/run", "--remount-ro", "/var/tmp", "--remount-ro", "/tmp"},
				end("/repo")),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := BwrapArgs(c.spec, argv)
			if !slices.Equal(got, c.want) {
				t.Errorf("BwrapArgs:\n got  %q\n want %q", got, c.want)
			}
		})
	}
}

func TestBwrapProbeMissing(t *testing.T) {
	b := NewBubblewrap(func(string) (string, error) { return "", exec.ErrNotFound }, []string{"true"})
	err := b.Probe(context.Background())
	if err == nil || err.Error() != "bubblewrap (bwrap) was not found on the PATH" {
		t.Errorf("probe = %v", err)
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("the error is not ErrNotFound: %v", err)
	}
	if b.Path() != "" {
		t.Errorf("path = %q", b.Path())
	}
	if _, err := b.Command(context.Background(), Spec{}, []string{"true"}); err == nil {
		t.Error("Command returned no error")
	}
}

func TestBwrapProbeFails(t *testing.T) {
	dir := t.TempDir()
	script := func(name, body string) func(string) (string, error) {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
		return func(string) (string, error) { return path, nil }
	}
	b := NewBubblewrap(script("fails", "echo 'bwrap: boom' >&2\necho second >&2\nexit 1\n"), []string{"true"})
	err := b.Probe(context.Background())
	if err == nil || err.Error() != "bubblewrap could not start a sandbox: bwrap: boom" {
		t.Errorf("probe = %v", err)
	}
	if !errors.Is(err, ErrCannotStart) {
		t.Errorf("the error is not ErrCannotStart: %v", err)
	}

	long := strings.Repeat("x", 300)
	b = NewBubblewrap(script("long", "echo '"+long+"' >&2\nexit 1\n"), []string{"true"})
	err = b.Probe(context.Background())
	want := "bubblewrap could not start a sandbox: " + long[:200]
	if err == nil || err.Error() != want {
		t.Errorf("a long line was not cut:\n got  %v\n want %s", err, want)
	}

	b = NewBubblewrap(script("silent", "exit 3\n"), []string{"true"})
	if err := b.Probe(context.Background()); err == nil || !strings.HasPrefix(err.Error(), "bubblewrap could not start a sandbox: exit status 3") {
		t.Errorf("a silent failure: %v", err)
	}

	b = NewBubblewrap(script("works", "exit 0\n"), []string{"true"})
	if err := b.Probe(context.Background()); err != nil {
		t.Errorf("a program that exits 0: %v", err)
	}
	if got, want := b.Path(), filepath.Join(dir, "works"); got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}

func TestSandboxBwrapProbe(t *testing.T) {
	requireSandbox(t)
}

func TestSandboxBwrapCommand(t *testing.T) {
	b := requireSandbox(t)
	ws := fixtureDir(t)
	tmp := filepath.Join(fixtureDir(t), "tmp")
	if err := os.Mkdir(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	run := func(spec Spec, script string) error {
		cmd, err := b.Command(context.Background(), spec, []string{"sh", "-c", script})
		if err != nil {
			t.Fatal(err)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Logf("output: %s", out)
		}
		return err
	}
	script := "echo x > inside && ! echo x > /etc/rune-probe 2>/dev/null && ! echo x > /run/rune-probe 2>/dev/null && echo t > /tmp/t"
	if err := run(Spec{Workspace: ws, Tmp: tmp}, script); err != nil {
		t.Fatalf("the confined command failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, "inside")); err != nil {
		t.Errorf("the file inside the workspace is missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "t")); err != nil {
		t.Errorf("the private temporary directory did not get the file: %v", err)
	}
	if err := run(Spec{Workspace: ws, ReadOnly: true}, "cat inside >/dev/null && ! echo x > inside 2>/dev/null && ! echo x > /tmp/t 2>/dev/null"); err != nil {
		t.Errorf("the read-only command failed: %v", err)
	}
}
