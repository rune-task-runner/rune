package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/rune-task-runner/rune/internal/runtime/agent"
	"github.com/rune-task-runner/rune/internal/runtime/interp"
	"github.com/rune-task-runner/rune/internal/runtime/shell"
	"github.com/rune-task-runner/rune/internal/token"
)

// The server puts back together the error the executor returned in the
// worker, so that everything that reads the error behaves as for a body run
// in the server.
func TestSandboxResultRoundTrip(t *testing.T) {
	span := token.Span{File: "Runefile", Start: token.Position{Offset: 10, Line: 2, Col: 5}, End: token.Position{Offset: 16, Line: 2, Col: 11}}
	cases := []struct {
		name string
		err  error
		kind string
	}{
		{"shell exit status", &shell.ExecError{Task: "build", Line: "exit 3", Span: span, Code: 3}, resultExec},
		{"shell error", &shell.ExecError{Task: "build", Line: "x(", Span: span, Err: errors.New("parse error")}, resultExec},
		{"missing interpreter", &interp.MissingInterpreterError{Task: "py", Name: "python3", Err: errors.New("not found")}, resultMissingInterpreter},
		{"interpreter exit status", &interp.ExecError{Task: "py", Name: "python3", Code: 2, Span: span}, resultInterpExec},
		{"interpreter error", &interp.ExecError{Task: "py", Name: "python3", Err: errors.New("fork failed"), Span: span}, resultInterpExec},
		{"agent not configured", &agent.NotConfiguredError{}, resultAgentNotConfigured},
		{"agent not installed", &agent.NotInstalledError{Name: "claude"}, resultAgentNotInstalled},
		{"agent failed", &agent.AuthError{Name: "claude", Err: errors.New("exit status 1")}, resultAgentAuth},
		{"other", errors.New("something else"), resultError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result := resultOf(c.err)
			if result.Kind != c.kind {
				t.Fatalf("kind = %q, want %q", result.Kind, c.kind)
			}
			// The result crosses a pipe as JSON.
			data, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			var decoded sandboxResult
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			got := errorOf(decoded, context.Background())
			if got == nil || got.Error() != c.err.Error() {
				t.Fatalf("error = %v, want %v", got, c.err)
			}
			if reflect.TypeOf(got) != reflect.TypeOf(c.err) {
				t.Errorf("type = %T, want %T", got, c.err)
			}
			var gotShell, wantShell *shell.ExecError
			if errors.As(c.err, &wantShell) && (!errors.As(got, &gotShell) || gotShell.Code != wantShell.Code || gotShell.Span != wantShell.Span) {
				t.Errorf("shell error = %+v, want %+v", gotShell, wantShell)
			}
			var gotInterp, wantInterp *interp.ExecError
			if errors.As(c.err, &wantInterp) && (!errors.As(got, &gotInterp) || gotInterp.Code != wantInterp.Code || gotInterp.Span != wantInterp.Span) {
				t.Errorf("interpreter error = %+v, want %+v", gotInterp, wantInterp)
			}
		})
	}

	if got := errorOf(resultOf(nil), context.Background()); got != nil {
		t.Errorf("no error came back as %v", got)
	}
	if r := resultOf(context.Canceled); r.Kind != resultCancelled {
		t.Errorf("a cancelled body has kind %q", r.Kind)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := errorOf(sandboxResult{Kind: resultCancelled}, ctx); !errors.Is(got, context.Canceled) {
		t.Errorf("a cancelled result = %v", got)
	}
	if got := errorOf(sandboxResult{Kind: resultCancelled}, context.Background()); !errors.Is(got, context.Canceled) {
		t.Errorf("a cancelled result with a live context = %v", got)
	}
}

// workerRun runs a job through the worker in this process.
func workerRun(t *testing.T, job any) (stdout, stderr, result string, err error) {
	t.Helper()
	payload, ok := job.([]byte)
	if !ok {
		var marshalErr error
		if payload, marshalErr = json.Marshal(job); marshalErr != nil {
			t.Fatal(marshalErr)
		}
	}
	var out, errOut, res bytes.Buffer
	err = runSandboxWorker(context.Background(), bytes.NewReader(payload), &res, nil, &out, &errOut)
	return out.String(), errOut.String(), res.String(), err
}

func TestSandboxWorkerShell(t *testing.T) {
	job := sandboxJob{Kind: jobShell, Task: "build", Quiet: true, Lines: []shell.Line{{Text: "echo out"}, {Text: "echo err >&2"}}}
	stdout, stderr, result, err := workerRun(t, job)
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "out\n" || stderr != "err\n" {
		t.Errorf("stdout %q, stderr %q", stdout, stderr)
	}
	got, started, readErr := readWorkerResult(strings.NewReader(result))
	if !started || readErr != nil || got.Kind != "" {
		t.Errorf("result %q: %+v, started %v, %v", result, got, started, readErr)
	}
}

func TestSandboxWorkerShellFailure(t *testing.T) {
	job := sandboxJob{Kind: jobShell, Task: "build", Quiet: true, Lines: []shell.Line{{Text: "exit 7"}}}
	_, _, result, err := workerRun(t, job)
	if err != nil {
		t.Fatal(err)
	}
	got, started, readErr := readWorkerResult(strings.NewReader(result))
	if !started || readErr != nil {
		t.Fatalf("result %q: started %v, %v", result, started, readErr)
	}
	if got.Kind != resultExec || got.Code != 7 || got.Line != "exit 7" || got.Task != "build" {
		t.Errorf("result = %+v", got)
	}
}

// A line that failed is reported with its exit status however long it is:
// a result past the limit is not read at all.
func TestSandboxWorkerLongFailingLine(t *testing.T) {
	long := "exit 7 # " + strings.Repeat("x", 2*maxWorkerResult)
	job := sandboxJob{Kind: jobShell, Task: "build", Quiet: true, Lines: []shell.Line{{Text: long}}}
	_, _, result, err := workerRun(t, job)
	if err != nil {
		t.Fatal(err)
	}
	got, started, readErr := readWorkerResult(strings.NewReader(result))
	if !started || readErr != nil || got.Kind != resultExec || got.Code != 7 {
		t.Fatalf("result = %+v, started %v, %v", got, started, readErr)
	}
	if want := long[:maxResultText] + "..."; got.Line != want {
		t.Errorf("the line is %d bytes, want its first %d and a mark", len(got.Line), maxResultText)
	}
}

func TestClip(t *testing.T) {
	if got := clip("short"); got != "short" {
		t.Errorf("a short text = %q", got)
	}
	// A cut never splits a character.
	accents := strings.Repeat("é", maxResultText)
	if got := clip(accents); !utf8.ValidString(got) || len(got) > maxResultText+len("...") || !strings.HasSuffix(got, "...") {
		t.Errorf("a long text was cut to %d bytes, valid %v", len(got), utf8.ValidString(got))
	}
	// Two texts of the largest size fit a result when JSON writes every
	// byte of them as an escape.
	control := clip(strings.Repeat("\x01", 2*maxWorkerResult))
	data, err := json.Marshal(sandboxResult{Kind: resultExec, Task: "build", Line: control, Message: control, Code: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(workerStarted)+len(data) > maxWorkerResult {
		t.Errorf("the largest result is %d bytes, over the limit of %d", len(data), maxWorkerResult)
	}
}

// An environment reaches the worker byte for byte: a value need not be
// UTF-8, and null still means that the worker's own is inherited.
func TestSandboxJobEnvKeepsBytes(t *testing.T) {
	for _, env := range [][]string{nil, {}, {"A=1", "RAW=a\xffb", "EMPTY="}} {
		data, err := json.Marshal(sandboxJob{Env: env})
		if err != nil {
			t.Fatal(err)
		}
		var got sandboxJob
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		if (got.Env == nil) != (env == nil) || !slices.Equal([]string(got.Env), env) {
			t.Errorf("env %q came back as %q from %s", env, got.Env, data)
		}
	}
	var bad sandboxJob
	if err := json.Unmarshal([]byte(`{"env":["not base64!"]}`), &bad); err == nil {
		t.Error("an entry that is not base64 was accepted")
	}
}

func TestSandboxWorkerBadJob(t *testing.T) {
	_, _, result, err := workerRun(t, []byte("not json"))
	if err == nil {
		t.Error("a job that is not JSON gave no error")
	}
	if result != "" {
		t.Errorf("a worker with no job wrote %q", result)
	}
	_, _, result, err = workerRun(t, sandboxJob{Kind: "dance", Task: "x"})
	if err != nil {
		t.Fatal(err)
	}
	got, _, _ := readWorkerResult(strings.NewReader(result))
	if got.Kind != resultError || !strings.Contains(got.Message, `unknown job kind "dance"`) {
		t.Errorf("an unknown kind = %+v", got)
	}
}

func TestSandboxWorkerEnvAndDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("compares the directory a POSIX pwd prints")
	}
	dir := t.TempDir()
	job := sandboxJob{
		Kind: jobShell, Task: "where", Quiet: true, Dir: dir,
		Env:   []string{"PROBE=from-the-job"},
		Lines: []shell.Line{{Text: "echo $PROBE"}, {Text: "pwd"}},
	}
	stdout, _, _, err := workerRun(t, job)
	if err != nil {
		t.Fatal(err)
	}
	if want := "from-the-job\n" + dir + "\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

func TestSandboxWorkerAgentText(t *testing.T) {
	var b bytes.Buffer
	writeAgentText(&b, "")
	writeAgentText(&b, "one")
	writeAgentText(&b, "two\n")
	if b.String() != "one\ntwo\n" {
		t.Errorf("agent text = %q", b.String())
	}
}

func TestReadWorkerResult(t *testing.T) {
	object := `{"kind":"exec","code":3,"span":{"File":"","Start":{"Offset":0,"Line":0,"Col":0},"End":{"Offset":0,"Line":0,"Col":0}}}`
	got, started, err := readWorkerResult(strings.NewReader(workerStarted + object + "\n"))
	if !started || err != nil || got.Kind != resultExec || got.Code != 3 {
		t.Errorf("a good result: %+v, started %v, %v", got, started, err)
	}
	for name, input := range map[string]string{
		"nothing":       "",
		"no start mark": object,
		"a cut mark":    "start",
	} {
		if _, started, err := readWorkerResult(strings.NewReader(input)); started || err != nil {
			t.Errorf("%s: started %v, %v", name, started, err)
		}
	}
	for name, input := range map[string]string{
		"no result":      workerStarted,
		"two objects":    workerStarted + object + "\n" + object + "\n",
		"garbage":        workerStarted + "ok\n",
		"unknown fields": workerStarted + `{"kind":"exec","exit":0}`,
		"too much":       workerStarted + `{"kind":"error","message":"` + strings.Repeat("x", maxWorkerResult) + `"}`,
	} {
		if _, started, err := readWorkerResult(strings.NewReader(input)); !started || !errors.Is(err, errWorkerResult) {
			t.Errorf("%s: started %v, %v", name, started, err)
		}
	}
	// The read stops at the bound, whatever a body writes into the pipe.
	endless := &countingReader{}
	if _, _, err := readWorkerResult(endless); !errors.Is(err, errWorkerResult) {
		t.Errorf("an endless result: %v", err)
	}
	if endless.n > maxWorkerResult+1 {
		t.Errorf("read %d bytes of an endless result", endless.n)
	}
}

// countingReader gives the start mark and then bytes without end.
type countingReader struct{ n int }

func (r *countingReader) Read(p []byte) (int, error) {
	n := 0
	if r.n == 0 {
		n = copy(p, workerStarted)
	}
	for i := n; i < len(p); i++ {
		p[i] = 'x'
	}
	r.n += len(p)
	return len(p), nil
}

func TestHintLine(t *testing.T) {
	if got, want := hintLine("build", false, "/repo"), `note: task "build" ran in the sandbox: it has no network (it is not marked [network]) and can write only inside /repo.`; got != want {
		t.Errorf("hint = %q, want %q", got, want)
	}
	if got, want := hintLine("fetch", true, "/repo"), `note: task "fetch" ran in the sandbox: it can write only inside /repo.`; got != want {
		t.Errorf("hint = %q, want %q", got, want)
	}
}

func TestSandboxStartErrorText(t *testing.T) {
	err := &sandboxStartError{task: "build", detail: "exit status 1"}
	if got, want := err.Error(), `the body of task "build" did not run: the sandbox could not start (exit status 1)`; got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
	if exitText(nil) != "exit status 0" || exitText(errWorkerResult) != "exit status 0" || exitText(errors.New("exit status 2")) != "exit status 2" {
		t.Error("exitText")
	}
}

func TestResolvConf(t *testing.T) {
	resolver := func(target string, err error) func(string) (string, error) {
		return func(path string) (string, error) {
			if path != "/etc/resolv.conf" {
				t.Errorf("resolved %q", path)
			}
			return target, err
		}
	}
	if got := resolvConf(resolver("/run/systemd/resolve/stub-resolv.conf", nil)); got != "/run/systemd/resolve/stub-resolv.conf" {
		t.Errorf("under /run: %q", got)
	}
	if got := resolvConf(resolver("/etc/resolv.conf", nil)); got != "" {
		t.Errorf("elsewhere: %q", got)
	}
	if got := resolvConf(resolver("", errors.New("no such file"))); got != "" {
		t.Errorf("an error: %q", got)
	}
}
