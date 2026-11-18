package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/rune-task-runner/rune/internal/sandbox"
)

// stubBackend stands in for bubblewrap. Its probe answers what a test sets,
// and its command runs argv with no confinement, so that everything around
// the sandbox is tested on a host that has none.
type stubBackend struct {
	name string
	path string

	mu       sync.Mutex
	probeErr error
	probes   int
	// probeCtxErr is the error of the context the last probe was given.
	probeCtxErr error
	specs       []sandbox.Spec
	// command, when set, replaces the command of a body.
	command func(ctx context.Context, argv []string) *exec.Cmd
}

func (b *stubBackend) Name() string { return b.name }
func (b *stubBackend) Path() string { return b.path }

func (b *stubBackend) Probe(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.probes++
	b.probeCtxErr = ctx.Err()
	return b.probeErr
}

func (b *stubBackend) Command(ctx context.Context, spec sandbox.Spec, argv []string) (*exec.Cmd, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.specs = append(b.specs, spec)
	if b.command != nil {
		return b.command(ctx, argv), nil
	}
	return exec.CommandContext(ctx, argv[0], argv[1:]...), nil
}

func (b *stubBackend) setProbe(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.probeErr = err
}

func (b *stubBackend) probeCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.probes
}

func (b *stubBackend) lastSpec() sandbox.Spec {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.specs) == 0 {
		return sandbox.Spec{}
	}
	return b.specs[len(b.specs)-1]
}

func TestSandboxModeOf(t *testing.T) {
	str := func(s string) *string { return &s }
	cases := []struct {
		name string
		flag *string
		env  string
		want sandbox.Mode
		err  string
	}{
		{"neither", nil, "", sandbox.ModeEnforce, ""},
		{"the variable", nil, "off", sandbox.ModeOff, ""},
		{"the variable says enforce", nil, "enforce", sandbox.ModeEnforce, ""},
		{"the flag wins", str("enforce"), "off", sandbox.ModeEnforce, ""},
		{"the flag turns it off", str("off"), "", sandbox.ModeOff, ""},
		{"a bad flag", str("maybe"), "off", "", `--sandbox: "maybe" is not a mode; use enforce or off`},
		{"an empty flag", str(""), "", "", `--sandbox: "" is not a mode; use enforce or off`},
		{"a bad variable", nil, "maybe", "", `RUNE_SANDBOX: "maybe" is not a mode; use enforce or off`},
		{"a good flag over a bad variable", str("off"), "maybe", sandbox.ModeOff, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := sandboxModeOf(c.flag, c.env)
			if c.err != "" {
				var usage *UsageError
				if !errors.As(err, &usage) || err.Error() != c.err {
					t.Fatalf("error = %v, want usage error %q", err, c.err)
				}
				return
			}
			if err != nil || got != c.want {
				t.Errorf("mode = %q, %v, want %q", got, err, c.want)
			}
		})
	}
}

// testState is a state over a stub backend, with a clock the test moves.
func testState(mode sandbox.Mode, backend sandbox.Backend, workspace, base string, env map[string]string, clock *time.Time) *sandboxState {
	now := time.Now
	if clock != nil {
		now = func() time.Time { return *clock }
	}
	return newSandboxState(mode, backend, "/opt/rune/rune", workspace, base, "", "", func(key string) string { return env[key] }, now)
}

func TestSandboxStateReady(t *testing.T) {
	ctx := context.Background()
	clock := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)

	t.Run("mode off never asks", func(t *testing.T) {
		stub := &stubBackend{name: "bubblewrap", path: "/usr/bin/bwrap"}
		s := testState(sandbox.ModeOff, stub, "/ws", "/cache/base", nil, &clock)
		if s.enforcing() {
			t.Error("mode off enforces")
		}
		s.auditRecord(ctx)
		if stub.probeCount() != 0 {
			t.Errorf("mode off probed %d times", stub.probeCount())
		}
		if (*sandboxState)(nil).enforcing() {
			t.Error("a nil state enforces")
		}
	})

	t.Run("a success is kept", func(t *testing.T) {
		stub := &stubBackend{name: "bubblewrap", path: "/usr/bin/bwrap"}
		s := testState(sandbox.ModeEnforce, stub, "/ws", "/cache/base", nil, &clock)
		for range 3 {
			if err := s.ready(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if stub.probeCount() != 1 {
			t.Errorf("probed %d times, want 1", stub.probeCount())
		}
		s.failed()
		if err := s.ready(ctx); err != nil || stub.probeCount() != 2 {
			t.Errorf("after a failed start: %v, %d probes", err, stub.probeCount())
		}
	})

	t.Run("a failure is asked again after a second", func(t *testing.T) {
		at := clock
		stub := &stubBackend{name: "bubblewrap", path: "/usr/bin/bwrap", probeErr: sandbox.ErrNotFound}
		s := testState(sandbox.ModeEnforce, stub, "/ws", "/cache/base", nil, &at)
		var unavailable *unavailableError
		if err := s.ready(ctx); !errors.As(err, &unavailable) || !errors.Is(err, sandbox.ErrNotFound) {
			t.Fatalf("ready = %v", err)
		}
		at = at.Add(500 * time.Millisecond)
		if err := s.ready(ctx); err == nil || stub.probeCount() != 1 {
			t.Errorf("within the second: %v, %d probes", err, stub.probeCount())
		}
		at = at.Add(600 * time.Millisecond)
		stub.setProbe(nil)
		if err := s.ready(ctx); err != nil || stub.probeCount() != 2 {
			t.Errorf("after the second: %v, %d probes", err, stub.probeCount())
		}
		if got := s.startupPart(); got != "sandbox: bubblewrap" {
			t.Errorf("part = %q", got)
		}
	})

	// The answer serves every call, so a call that was cancelled while the
	// backend was asked must not decide it.
	t.Run("a cancelled call does not cancel the probe", func(t *testing.T) {
		stub := &stubBackend{name: "bubblewrap", path: "/usr/bin/bwrap"}
		s := testState(sandbox.ModeEnforce, stub, "/ws", "/cache/base", nil, &clock)
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if err := s.ready(cancelled); err != nil {
			t.Errorf("ready = %v", err)
		}
		if stub.probeCtxErr != nil {
			t.Errorf("the probe ran under a context that was already done: %v", stub.probeCtxErr)
		}
	})

	reasons := []struct {
		name string
		stub *stubBackend
		exe  string
		env  map[string]string
		want string
	}{
		{"the backend inside the project", &stubBackend{name: "bubblewrap", path: "/ws/bin/bwrap"}, "/opt/rune/rune", nil, "bubblewrap (/ws/bin/bwrap) is inside the project, where a task could replace it"},
		{"the backend inside the private base", &stubBackend{name: "bubblewrap", path: "/cache/base/bwrap"}, "/opt/rune/rune", nil, "bubblewrap (/cache/base/bwrap) is inside the project, where a task could replace it"},
		{"the binary inside the project", &stubBackend{name: "bubblewrap", path: "/usr/bin/bwrap"}, "/ws/dist/rune", nil, "the Rune binary (/ws/dist/rune) is inside the project, where a task could replace it"},
		{"a PATH directory the sandbox hides", &stubBackend{name: "bubblewrap", path: "/usr/bin/bwrap"}, "/opt/rune/rune", map[string]string{"PATH": "/usr/bin:relative:/nonexistent-rune-dir:/tmp"}, "the PATH holds /tmp, which the sandbox hides"},
	}
	for _, c := range reasons {
		t.Run(c.name, func(t *testing.T) {
			if runtime.GOOS == "windows" {
				t.Skip("these reasons are about the paths of a POSIX host")
			}
			s := testState(sandbox.ModeEnforce, c.stub, "/ws", "/cache/base", c.env, &clock)
			s.exe = c.exe
			err := s.ready(ctx)
			if err == nil || err.Error() != c.want {
				t.Fatalf("ready = %v, want %q", err, c.want)
			}
			if c.stub.probeCount() != 0 {
				t.Errorf("probed %d times before the reason was found", c.stub.probeCount())
			}
			var unavailable *unavailableError
			if !errors.As(err, &unavailable) || unavailable.remedy() != sandboxOffRemedy {
				t.Errorf("remedy = %v", err)
			}
		})
	}
}

// The backend finds its program again for every command. One that has
// appeared inside the project since the look that ready keeps is not run,
// for a body and for git alike, and the next look reports it.
func TestCommandRefusesBackendInProject(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the paths of a POSIX host")
	}
	ctx := context.Background()
	stub := &stubBackend{name: "bubblewrap", path: "/usr/bin/bwrap"}
	s := testState(sandbox.ModeEnforce, stub, "/ws", "", nil, nil)
	if err := s.ready(ctx); err != nil {
		t.Fatal(err)
	}
	if cmd, err := s.command(ctx, sandbox.Spec{}, []string{"/usr/bin/true"}); err != nil || cmd.Path != "/usr/bin/true" {
		t.Fatalf("a program outside the project: %v, %v", cmd, err)
	}

	stub.command = func(ctx context.Context, _ []string) *exec.Cmd {
		return exec.CommandContext(ctx, "/ws/.venv/bin/bwrap")
	}
	const want = "bubblewrap (/ws/.venv/bin/bwrap) is inside the project, where a task could replace it"
	if cmd, err := s.command(ctx, sandbox.Spec{}, []string{"/usr/bin/true"}); cmd != nil || err == nil || err.Error() != want {
		t.Errorf("command = %v, %v, want %q", cmd, err, want)
	}
	if _, started, err := s.launch(ctx, sandbox.Spec{}, sandboxJob{}, nil, nil, nil); started || err == nil || err.Error() != want {
		t.Errorf("launch: started %v, %v", started, err)
	}
	if cmd := s.gitRunner()(ctx, "/ws", "status"); cmd.Path != "" {
		t.Errorf("git got a command: %v", cmd.Args)
	}
}

func TestSandboxTexts(t *testing.T) {
	ctx := context.Background()
	const (
		notFound    = "bubblewrap (bwrap) was not found on the PATH"
		install     = "Install bubblewrap, or turn the sandbox off with --sandbox off or RUNE_SANDBOX=off to run tasks unsandboxed."
		allow       = "Allow unprivileged user namespaces for bubblewrap, or turn the sandbox off with --sandbox off or RUNE_SANDBOX=off to run tasks unsandboxed."
		turnOff     = "Turn the sandbox off with --sandbox off or RUNE_SANDBOX=off to run tasks unsandboxed."
		cannotStart = "bubblewrap could not start a sandbox: bwrap: No permissions to create a new namespace"
		unsupported = "this system (macos) has no sandbox backend"
	)
	cases := []struct {
		name            string
		mode            sandbox.Mode
		backend         sandbox.Backend
		part            string
		warning         string
		refusal, remedy string
	}{
		{
			name: "confining", mode: sandbox.ModeEnforce, backend: &stubBackend{name: "bubblewrap", path: "/usr/bin/bwrap"},
			part: "sandbox: bubblewrap",
		},
		{
			name: "off", mode: sandbox.ModeOff, backend: &stubBackend{name: "bubblewrap"},
			part:    "sandbox: off, tasks run unsandboxed",
			warning: "sandbox: off. The tasks agents call run without a sandbox: they can use the network and write wherever this user can.",
		},
		{
			name: "not found", mode: sandbox.ModeEnforce, backend: &stubBackend{name: "bubblewrap", probeErr: sandbox.ErrNotFound},
			part:    "sandbox: unavailable, calls are refused",
			warning: "sandbox: " + notFound + ". Every task call will be refused. " + install,
			refusal: notFound, remedy: install,
		},
		{
			name: "cannot start", mode: sandbox.ModeEnforce,
			backend: &stubBackend{name: "bubblewrap", path: "/usr/bin/bwrap", probeErr: fmt.Errorf("%w: bwrap: No permissions to create a new namespace", sandbox.ErrCannotStart)},
			part:    "sandbox: unavailable, calls are refused",
			warning: "sandbox: " + cannotStart + ". Every task call will be refused. " + allow,
			refusal: cannotStart, remedy: allow,
		},
		{
			name: "another system", mode: sandbox.ModeEnforce, backend: sandbox.Detect("darwin", nil),
			part:    "sandbox: unavailable, calls are refused",
			warning: "sandbox: " + unsupported + ". Every task call will be refused. " + turnOff,
			refusal: unsupported, remedy: turnOff,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := testState(c.mode, c.backend, "/ws", "/cache/base", nil, nil)
			err := error(nil)
			if s.enforcing() {
				err = s.ready(ctx)
			}
			if got := s.startupPart(); got != c.part {
				t.Errorf("part = %q, want %q", got, c.part)
			}
			if got := s.startupWarning(); got != c.warning {
				t.Errorf("warning = %q, want %q", got, c.warning)
			}
			if c.refusal == "" {
				return
			}
			want := `task "build" was not run: the sandbox is unavailable: ` + c.refusal + ". " + c.remedy
			if got := s.refusal("build", err); got != want {
				t.Errorf("refusal = %q, want %q", got, want)
			}
		})
	}

	s := testState(sandbox.ModeEnforce, &stubBackend{name: "bubblewrap"}, "/ws", "/cache/base", nil, nil)
	other := &preflightError{text: "the sandbox cannot confine it: reasons"}
	if got, want := s.refusal("build", other), `task "build" was not run: the sandbox cannot confine it: reasons`; got != want {
		t.Errorf("refusal = %q, want %q", got, want)
	}
}

func TestSandboxRecord(t *testing.T) {
	ctx := context.Background()
	text := func(s *sandboxState) string {
		data, err := json.Marshal(s.auditRecord(ctx))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	confining := testState(sandbox.ModeEnforce, &stubBackend{name: "bubblewrap", path: "/usr/bin/bwrap"}, "/ws", "/cache/base", nil, nil)
	if got, want := text(confining), `{"enforced":true,"backend":"bubblewrap","mode":"enforce"}`; got != want {
		t.Errorf("confining = %s, want %s", got, want)
	}
	unavailable := testState(sandbox.ModeEnforce, &stubBackend{name: "bubblewrap", probeErr: sandbox.ErrNotFound}, "/ws", "/cache/base", nil, nil)
	if got, want := text(unavailable), `{"enforced":false,"mode":"enforce"}`; got != want {
		t.Errorf("unavailable = %s, want %s", got, want)
	}
	off := testState(sandbox.ModeOff, &stubBackend{name: "bubblewrap"}, "/ws", "/cache/base", nil, nil)
	if got, want := text(off), `{"enforced":false,"mode":"off"}`; got != want {
		t.Errorf("off = %s, want %s", got, want)
	}
	if got, want := text(nil), `{"enforced":false}`; got != want {
		t.Errorf("no state = %s, want %s", got, want)
	}
}
