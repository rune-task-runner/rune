//go:build unix

package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rune-task-runner/rune/internal/eval"
	"github.com/rune-task-runner/rune/internal/runtime/scheduler"
	"github.com/rune-task-runner/rune/internal/sandbox"
)

func TestSandboxWorkerInterp(t *testing.T) {
	stdout, _, result, err := workerRun(t, sandboxJob{Kind: jobInterp, Task: "sh", Script: "echo hi\n", Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, _, _ := readWorkerResult(strings.NewReader(result)); stdout != "hi\n" || got.Kind != "" {
		t.Errorf("stdout %q, result %+v", stdout, got)
	}
	_, _, result, err = workerRun(t, sandboxJob{Kind: jobInterp, Task: "py", Script: "x\n", Command: []string{"no-such-interpreter-rune"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, _, _ := readWorkerResult(strings.NewReader(result)); got.Kind != resultMissingInterpreter || got.Name != "no-such-interpreter-rune" || got.Task != "py" {
		t.Errorf("result = %+v", got)
	}
}

// The worker run by hand has no pipes and says so.
func TestWorkerPipesAbsent(t *testing.T) {
	cmd := exec.Command(os.Args[0], SandboxWorkerArg)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != ExitUsage {
		t.Fatalf("exit = %v, output %q", err, out)
	}
	if want := "rune: --sandbox-worker is started by rune itself\n"; string(out) != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestRunConfinedFailure(t *testing.T) {
	src := "build:\n    @echo before\n    @exit 3\n\n[network]\nfetch:\n    @exit 4\n\nfine:\n    @echo ok\n"
	a, _ := confinedAdapter(t, src)
	res, err := a.Call(context.Background(), "build", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 || res.Stdout != "before\n" {
		t.Errorf("result = %+v", res)
	}
	want := `note: task "build" ran in the sandbox: it has no network (it is not marked [network]) and can write only inside ` + a.root + ".\n"
	if !strings.HasSuffix(res.Stderr, want) {
		t.Errorf("stderr = %q, want it to end with %q", res.Stderr, want)
	}

	// The same body in the server exits with the same code.
	plain, _ := confinedAdapter(t, src)
	plain.sandbox = nil
	unconfined, err := plain.Call(context.Background(), "build", nil)
	if err != nil || unconfined.ExitCode != res.ExitCode {
		t.Errorf("unconfined exit %d, confined %d, %v", unconfined.ExitCode, res.ExitCode, err)
	}
	if strings.Contains(unconfined.Stderr, "ran in the sandbox") {
		t.Errorf("a body run in the server got the hint: %q", unconfined.Stderr)
	}

	res, err = a.Call(context.Background(), "fetch", nil)
	if err != nil || res.ExitCode != 4 {
		t.Fatalf("fetch = %+v, %v", res, err)
	}
	if want := `note: task "fetch" ran in the sandbox: it can write only inside ` + a.root + ".\n"; !strings.HasSuffix(res.Stderr, want) {
		t.Errorf("stderr = %q", res.Stderr)
	}

	res, err = a.Call(context.Background(), "fine", nil)
	if err != nil || res.ExitCode != 0 || strings.Contains(res.Stderr, "ran in the sandbox") {
		t.Errorf("a body that succeeds: %+v, %v", res, err)
	}
}

func TestRunConfinedStartFailure(t *testing.T) {
	src := "build: || diagnose\n    @touch body.ran\n\ndiagnose:\n    @touch hook.ran\n"
	a, stub := confinedAdapter(t, src)
	stub.command = func(ctx context.Context, _ []string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "echo 'bwrap: no namespaces' >&2; exit 1")
	}
	res, err := a.Call(context.Background(), "build", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != ExitTaskFail {
		t.Errorf("exit = %d", res.ExitCode)
	}
	for _, want := range []string{"bwrap: no namespaces", `the body of task "build" did not run: the sandbox could not start (exit status 1)`} {
		if !strings.Contains(res.Stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, res.Stderr)
		}
	}
	if strings.Contains(res.Stderr, "ran in the sandbox") {
		t.Errorf("a body that did not run got the hint:\n%s", res.Stderr)
	}
	for _, marker := range []string{"body.ran", "hook.ran"} {
		if exists(filepath.Join(a.root, marker)) {
			t.Errorf("%s exists", marker)
		}
	}
	// The answer that proved wrong is not trusted for the next call.
	before := stub.probeCount()
	if err := a.sandbox.ready(context.Background()); err != nil || stub.probeCount() != before+1 {
		t.Errorf("after a failed start: %v, %d probes, want %d", err, stub.probeCount(), before+1)
	}

	var start *sandboxStartError
	if !errors.As(error(&sandboxStartError{}), &start) || !errors.Is(&sandboxStartError{}, scheduler.ErrBodyNotRun) {
		t.Error("a start failure is not a body that did not run")
	}
}

func TestRunConfinedWorkerDies(t *testing.T) {
	a, stub := confinedAdapter(t, "build:\n    @echo hi\n")
	stub.command = func(ctx context.Context, _ []string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "printf 'started\\n' >&4; exit 2")
	}
	res, err := a.Call(context.Background(), "build", nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := `the worker of task "build" ended without a result (exit status 2)`; res.ExitCode != ExitTaskFail || !strings.Contains(res.Stderr, want) {
		t.Errorf("exit %d, stderr %q", res.ExitCode, res.Stderr)
	}

	// A worker that reports and then exits badly is believed as far as it
	// did not end well.
	stub.command = func(ctx context.Context, _ []string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "printf 'started\\ngarbage\\n' >&4")
	}
	res, err = a.Call(context.Background(), "build", nil)
	if err != nil || !strings.Contains(res.Stderr, "ended without a result (exit status 0)") {
		t.Errorf("garbage: %+v, %v", res, err)
	}
}

func TestRunConfinedCancel(t *testing.T) {
	// The body spins in the shell itself. A child process would outlive the
	// stub's worker, which has no PID namespace to end it, and hold the
	// output pipes open.
	a, _ := confinedAdapter(t, "wait:\n    @: > started\n    @while true; do :; done\n")
	tmpParent := filepath.Join(a.sandbox.base, "tmp")
	run := func(ctx context.Context) time.Duration {
		begin := time.Now()
		_, _ = a.Call(ctx, "wait", nil)
		return time.Since(begin)
	}
	waitStarted := func(cancel context.CancelFunc) {
		go func() {
			for range 200 {
				if exists(filepath.Join(a.root, "started")) {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			cancel()
		}()
	}

	ctx, cancel := context.WithCancel(context.Background())
	waitStarted(cancel)
	if took := run(ctx); took > 10*time.Second {
		t.Errorf("a cancelled call returned after %v", took)
	}
	if entries, err := os.ReadDir(tmpParent); err != nil || len(entries) != 0 {
		t.Errorf("the temporary directory was left behind: %v, %v", entries, err)
	}

	ctx, stop := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer stop()
	if took := run(ctx); took > 10*time.Second {
		t.Errorf("a call past its deadline returned after %v", took)
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Errorf("context = %v", ctx.Err())
	}
}

// runConfined returns the context's error for a body that was cancelled, as
// the executors do.
func TestRunConfinedCancelError(t *testing.T) {
	a, _ := confinedAdapter(t, "wait:\n    @while true; do :; done\n")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	eng, _ := bodyEngine(a, ctx, a.root)
	err := eng.runBody(a.tasks["wait"], eval.Args{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want the deadline", err)
	}
}

// bodyEngine is an engine that confines its bodies with the adapter's state
// and runs them in workDir, without the preflight of a call.
func bodyEngine(a *mcpAdapter, ctx context.Context, workDir string) (*engine, *lockedBuffer) {
	stderr := &lockedBuffer{}
	return &engine{
		tasks: a.tasks, settings: a.settings, workDir: workDir, root: a.root, ctx: ctx,
		scope:   eval.NewScope(a.assigns, a.overrides),
		opts:    Options{Stdout: &lockedBuffer{}, Stderr: stderr, Quiet: true},
		plan:    planRun,
		sandbox: &bodySandbox{state: a.sandbox, workspace: a.root},
	}, stderr
}

func TestRunConfinedDirOutside(t *testing.T) {
	a, stub := confinedAdapter(t, "build:\n    @echo hi\n")
	eng, stderr := bodyEngine(a, context.Background(), "/")
	err := eng.runBody(a.tasks["build"], eval.Args{})
	if !errors.Is(err, scheduler.ErrBodyNotRun) {
		t.Fatalf("error = %v", err)
	}
	if want := "its working directory / is outside the project " + a.root; !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want %q in it", stderr.String(), want)
	}
	if len(stub.specs) != 0 {
		t.Error("the backend was asked for a body outside the project")
	}
}

// A body starts in the resolved directory. The path the load wrote may go
// through a link, and a link under /tmp is not there inside the sandbox.
func TestRunConfinedResolvesDir(t *testing.T) {
	a, _ := confinedAdapter(t, "where:\n    @pwd\n")
	link := filepath.Join(realTempDir(t), "link")
	if err := os.Symlink(a.root, link); err != nil {
		t.Fatal(err)
	}
	a.workDir = link
	res, err := a.Call(context.Background(), "where", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || res.Stdout != a.root+"\n" {
		t.Errorf("the body ran in %q (exit %d), want %q", res.Stdout, res.ExitCode, a.root)
	}
}

// A variable whose value is not UTF-8 reaches a confined body unchanged.
func TestRunConfinedEnvBytes(t *testing.T) {
	if _, err := exec.LookPath("printenv"); err != nil {
		t.Skip("printenv is not installed")
	}
	// Into a file: a tool result is text, and the file holds the bytes.
	a, _ := confinedAdapter(t, "show:\n    @printenv RAW > raw.out\n")
	a.baseEnv = append(os.Environ(), "RAW=a\xffb")
	res, err := a.Call(context.Background(), "show", nil)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("result = %+v, %v", res, err)
	}
	got, err := os.ReadFile(filepath.Join(a.root, "raw.out"))
	if err != nil || string(got) != "a\xffb\n" {
		t.Errorf("the body saw %q, %v", got, err)
	}
}

func TestRunConfinedInterp(t *testing.T) {
	a, _ := confinedAdapter(t, "[script(\"sh\")]\nbuild:\n    echo from-sh\n    exit 5\n")
	res, err := a.Call(context.Background(), "build", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 5 || res.Stdout != "from-sh\n" {
		t.Errorf("result = %+v", res)
	}
}

func TestRunConfinedAgent(t *testing.T) {
	src := `set agent_cmd := ["sh", "-c", "printf 'prompt=%s url=%s' \"$1\" \"$RUNE_MCP_URL\"", "stub"]` + "\n\nask (agent):\n    Do it.\n"
	a, stub := confinedAdapter(t, src)
	res, err := a.Call(context.Background(), "ask", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || res.Stdout != "prompt=Do it.\n url=\n" {
		t.Errorf("result = %+v", res)
	}
	if len(stub.specs) != 1 {
		t.Errorf("the agent body ran through %d sandboxes", len(stub.specs))
	}
}

func TestRunConfinedAgentErrors(t *testing.T) {
	ask := "\n\nask (agent):\n    Do it.\n"
	cases := []struct {
		name string
		src  string
		exit int
	}{
		{"not configured", "ask (agent):\n    Do it.\n", ExitUsage},
		{"not installed", `set agent_cmd := ["no-such-agent-rune"]` + ask, ExitTaskFail},
		{"fails", `set agent_cmd := ["sh", "-c", "exit 9"]` + ask, ExitTaskFail},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, _ := confinedAdapter(t, c.src)
			res, err := a.Call(context.Background(), "ask", nil)
			if err != nil || res.ExitCode != c.exit {
				t.Errorf("result = %+v, %v, want exit %d", res, err, c.exit)
			}
			plain, _ := confinedAdapter(t, c.src)
			plain.sandbox = nil
			if unconfined, err := plain.Call(context.Background(), "ask", nil); err != nil || unconfined.ExitCode != c.exit {
				t.Errorf("unconfined = %+v, %v, want exit %d", unconfined, err, c.exit)
			}
		})
	}
}

// A program a body starts holds no descriptor the worker inherited, also
// when the worker was started by a process that marked none of its own, as
// the endpoint of an (agent) task is.
func TestWorkerClosesInheritedFiles(t *testing.T) {
	if _, err := os.Stat("/proc/self/fd"); err != nil {
		t.Skip("no /proc/self/fd on this system")
	}
	// Opened without close-on-exec, so the worker inherits it.
	fd, err := syscall.Open(os.DevNull, syscall.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Close(fd) }()
	a, _ := confinedAdapter(t, "fds n:\n    @sh -c 'test -e /proc/self/fd/{{n}} && echo open || echo closed'\n")
	res, err := a.Call(context.Background(), "fds", map[string]string{"n": strconv.Itoa(fd)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "closed\n" {
		t.Errorf("descriptor %d in a program of the body: %q\n%s", fd, res.Stdout, res.Stderr)
	}
}

// The worker's own environment is the server's, with a temporary directory
// that exists inside the sandbox.
func TestLaunchEnvironment(t *testing.T) {
	a, stub := confinedAdapter(t, "build:\n    @echo hi\n")
	t.Setenv("TMPDIR", "/somewhere/else")
	t.Setenv("RUNE_TEST_FROM_SERVER", "yes")
	env := stubEnv(t, a, stub)
	if got := lastValue(env, "TMPDIR"); got != "/tmp" {
		t.Errorf("the worker's TMPDIR = %q", got)
	}
	if got := lastValue(env, "RUNE_TEST_FROM_SERVER"); got != "yes" {
		t.Errorf("the worker lost the server's environment: %q", got)
	}
}

// stubEnv runs one call and returns the environment its command had.
func stubEnv(t *testing.T, a *mcpAdapter, stub *stubBackend) []string {
	t.Helper()
	var cmd *exec.Cmd
	stub.command = func(ctx context.Context, argv []string) *exec.Cmd {
		cmd = exec.CommandContext(ctx, argv[0], argv[1:]...)
		return cmd
	}
	if _, err := a.Call(context.Background(), "build", nil); err != nil {
		t.Fatal(err)
	}
	return cmd.Env
}

// lastValue is the value an environment gives a name: the last entry wins.
func lastValue(env []string, name string) string {
	value := ""
	for _, entry := range env {
		if v, ok := strings.CutPrefix(entry, name+"="); ok {
			value = v
		}
	}
	return value
}

func TestLaunchCommandError(t *testing.T) {
	s := testState(sandbox.ModeEnforce, sandbox.Detect("windows", nil), "/ws", "/base", nil, nil)
	_, started, err := s.launch(context.Background(), sandbox.Spec{}, sandboxJob{}, nil, nil, nil)
	if started || err == nil {
		t.Errorf("a backend with no command: started %v, %v", started, err)
	}
	stub := &stubBackend{name: "bubblewrap", command: func(ctx context.Context, _ []string) *exec.Cmd {
		return exec.CommandContext(ctx, "/nonexistent/rune-worker")
	}}
	s = testState(sandbox.ModeEnforce, stub, "/ws", "/base", nil, nil)
	if _, started, err := s.launch(context.Background(), sandbox.Spec{}, sandboxJob{}, nil, nil, nil); started || err == nil {
		t.Errorf("a command that cannot start: started %v, %v", started, err)
	}
}
