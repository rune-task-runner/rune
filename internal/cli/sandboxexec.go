package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rune-task-runner/rune/internal/ast"
	rt "github.com/rune-task-runner/rune/internal/runtime"
	"github.com/rune-task-runner/rune/internal/runtime/agent"
	"github.com/rune-task-runner/rune/internal/runtime/interp"
	"github.com/rune-task-runner/rune/internal/runtime/scheduler"
	"github.com/rune-task-runner/rune/internal/runtime/shell"
	"github.com/rune-task-runner/rune/internal/sandbox"
	"github.com/rune-task-runner/rune/internal/token"
)

// A body an agent's call runs does not run in the server (spec 053). The
// built-in shell interprets redirections and built-ins in its own process,
// so confining only the programs a body starts would leave `echo x > /etc/x`
// free. Each body runs in a worker instead: this binary, started inside the
// sandbox, which reads one job, runs the same executor and reports one
// result.

const (
	// SandboxWorkerArg and SandboxProbeArg are the two internal ways to start
	// the binary. Neither can be a task name, which never starts with "-".
	SandboxWorkerArg = "--sandbox-worker"
	SandboxProbeArg  = "--sandbox-probe"

	workerStarted = "started\n"
	// maxWorkerResult bounds what the server reads from a worker. The body
	// runs in the worker and can reach its result pipe, so the read must not
	// follow whatever a body writes there.
	maxWorkerResult = 64 << 10
	// maxResultText bounds each text of a result. The line that failed can be
	// as long as an argument an agent sent, and a result past maxWorkerResult
	// is not read, which would lose the body's exit status with it. Two texts
	// of this size fit when every byte of them is written as an escape.
	maxResultText = 4 << 10
	// sandboxTmpEnv names the private temporary directory of a body, which
	// the sandbox binds at /tmp.
	sandboxTmpEnv = "TMPDIR=/tmp"
	// workerWaitDelay is how long the output pipes may stay open after the
	// sandbox was killed.
	workerWaitDelay = 5 * time.Second

	jobShell  = "shell"
	jobInterp = "interp"
	jobAgent  = "agent"

	resultExec               = "exec"
	resultMissingInterpreter = "missing-interpreter"
	resultInterpExec         = "interp-exec"
	resultAgentNotConfigured = "agent-not-configured"
	resultAgentNotInstalled  = "agent-not-installed"
	resultAgentAuth          = "agent-auth"
	resultCancelled          = "cancelled"
	resultError              = "error"
)

// sandboxJob is one body, as runBodyTo prepared it.
type sandboxJob struct {
	Kind    string       `json:"kind"`
	Task    string       `json:"task"`
	Lines   []shell.Line `json:"lines,omitempty"`
	Script  string       `json:"script,omitempty"`
	Command []string     `json:"command,omitempty"`
	Span    token.Span   `json:"span"`
	Prompt  string       `json:"prompt,omitempty"`
	Dir     string       `json:"dir"`
	// Env is the task's environment. Null means the executor inherits the
	// worker's, as a nil slice does in the server.
	Env   jobEnv `json:"env"`
	Quiet bool   `json:"quiet"`
}

// jobEnv is an environment on its way to the worker. Its entries travel as
// bytes: a variable's value need not be UTF-8, and JSON text would replace
// every byte that is not.
type jobEnv []string

func (e jobEnv) MarshalJSON() ([]byte, error) {
	if e == nil {
		return []byte("null"), nil
	}
	entries := make([][]byte, len(e))
	for i, entry := range e {
		entries[i] = []byte(entry)
	}
	return json.Marshal(entries)
}

func (e *jobEnv) UnmarshalJSON(data []byte) error {
	var entries [][]byte
	if err := json.Unmarshal(data, &entries); err != nil {
		return err
	}
	*e = nil
	if entries != nil {
		*e = make(jobEnv, len(entries))
		for i, entry := range entries {
			(*e)[i] = string(entry)
		}
	}
	return nil
}

// sandboxResult is how a body ended: the executor's error taken apart, so
// that the server can put the same error back together.
type sandboxResult struct {
	Kind    string     `json:"kind,omitempty"`
	Task    string     `json:"task,omitempty"`
	Line    string     `json:"line,omitempty"`
	Name    string     `json:"name,omitempty"`
	Span    token.Span `json:"span"`
	Code    int        `json:"code,omitempty"`
	Message string     `json:"message,omitempty"`
}

// resultOf takes an executor's error apart.
func resultOf(err error) sandboxResult {
	if err == nil {
		return sandboxResult{}
	}
	text := func(wrapped error) string {
		if wrapped == nil {
			return ""
		}
		return wrapped.Error()
	}
	var (
		shellErr      *shell.ExecError
		missing       *interp.MissingInterpreterError
		interpErr     *interp.ExecError
		notConfigured *agent.NotConfiguredError
		notInstalled  *agent.NotInstalledError
		authErr       *agent.AuthError
	)
	switch {
	case errors.As(err, &shellErr):
		return sandboxResult{Kind: resultExec, Task: shellErr.Task, Line: shellErr.Line, Span: shellErr.Span, Code: shellErr.Code, Message: text(shellErr.Err)}
	case errors.As(err, &missing):
		return sandboxResult{Kind: resultMissingInterpreter, Task: missing.Task, Name: missing.Name, Message: text(missing.Err)}
	case errors.As(err, &interpErr):
		return sandboxResult{Kind: resultInterpExec, Task: interpErr.Task, Name: interpErr.Name, Span: interpErr.Span, Code: interpErr.Code, Message: text(interpErr.Err)}
	case errors.As(err, &notConfigured):
		return sandboxResult{Kind: resultAgentNotConfigured}
	case errors.As(err, &notInstalled):
		return sandboxResult{Kind: resultAgentNotInstalled, Name: notInstalled.Name}
	case errors.As(err, &authErr):
		return sandboxResult{Kind: resultAgentAuth, Name: authErr.Name, Message: text(authErr.Err)}
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return sandboxResult{Kind: resultCancelled, Message: err.Error()}
	}
	return sandboxResult{Kind: resultError, Message: err.Error()}
}

// clip bounds a text of a result to maxResultText, cut between two
// characters.
func clip(text string) string {
	if len(text) <= maxResultText {
		return text
	}
	cut := maxResultText
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "..."
}

// errorOf puts an executor's error back together, so that exit codes,
// banners and failure hooks see what they see for a body run in the server.
func errorOf(r sandboxResult, ctx context.Context) error {
	wrapped := func() error {
		if r.Message == "" {
			return nil
		}
		return errors.New(r.Message)
	}
	switch r.Kind {
	case "":
		return nil
	case resultExec:
		return &shell.ExecError{Task: r.Task, Line: r.Line, Span: r.Span, Code: r.Code, Err: wrapped()}
	case resultMissingInterpreter:
		return &interp.MissingInterpreterError{Task: r.Task, Name: r.Name, Err: wrapped()}
	case resultInterpExec:
		return &interp.ExecError{Task: r.Task, Name: r.Name, Span: r.Span, Code: r.Code, Err: wrapped()}
	case resultAgentNotConfigured:
		return &agent.NotConfiguredError{}
	case resultAgentNotInstalled:
		return &agent.NotInstalledError{Name: r.Name}
	case resultAgentAuth:
		return &agent.AuthError{Name: r.Name, Err: errors.New(r.Message)}
	case resultCancelled:
		if err := ctx.Err(); err != nil {
			return err
		}
		return context.Canceled
	}
	return errors.New(r.Message)
}

// errWorkerResult reports a worker that started and left no usable result.
var errWorkerResult = errors.New("no result")

// readWorkerResult reads what a worker wrote: the start mark, then exactly
// one JSON object. started is false when the mark never came, which means
// the sandbox did not start and the body did not run.
func readWorkerResult(r io.Reader) (result sandboxResult, started bool, err error) {
	data, readErr := io.ReadAll(io.LimitReader(r, maxWorkerResult+1))
	rest, ok := bytes.CutPrefix(data, []byte(workerStarted))
	if !ok {
		return sandboxResult{}, false, nil
	}
	if readErr != nil || len(data) > maxWorkerResult {
		return sandboxResult{}, true, errWorkerResult
	}
	dec := json.NewDecoder(bytes.NewReader(rest))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&result); err != nil {
		return sandboxResult{}, true, errWorkerResult
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return sandboxResult{}, true, errWorkerResult
	}
	return result, true, nil
}

// runSandboxWorker runs one job and reports how it ended. The error it
// returns is the worker's own failure, never the body's: a body that fails
// is a result.
func runSandboxWorker(ctx context.Context, jobIn io.Reader, resultOut io.Writer, stdin io.Reader, stdout, stderr io.Writer) error {
	var job sandboxJob
	if err := json.NewDecoder(jobIn).Decode(&job); err != nil {
		return fmt.Errorf("the job could not be read: %w", err)
	}
	if _, err := io.WriteString(resultOut, workerStarted); err != nil {
		return err
	}
	var err error
	switch job.Kind {
	case jobShell:
		err = shell.Run(ctx, job.Task, job.Lines, shell.Options{
			Stdin: stdin, Stdout: stdout, Stderr: stderr, Dir: job.Dir, Env: job.Env, Quiet: job.Quiet,
		})
	case jobInterp:
		err = interp.Run(ctx, job.Task, job.Script, job.Command, job.Span, interp.Options{
			Stdin: stdin, Stdout: stdout, Stderr: stderr, Dir: job.Dir, Env: job.Env,
		})
	case jobAgent:
		var final string
		final, err = agent.CLIProvider{}.Run(ctx, job.Prompt, agent.Options{
			AgentCmd: job.Command, Dir: job.Dir, Env: job.Env, Stdin: stdin, Stderr: stderr,
		})
		if err == nil {
			writeAgentText(stdout, final)
		}
	default:
		err = fmt.Errorf("unknown job kind %q", job.Kind)
	}
	result := resultOf(err)
	result.Line, result.Message = clip(result.Line), clip(result.Message)
	return json.NewEncoder(resultOut).Encode(result)
}

// writeAgentText prints an agent's final text as a task's output, ending
// with a newline.
func writeAgentText(w io.Writer, text string) {
	if text == "" {
		return
	}
	fmt.Fprint(w, text)
	if !strings.HasSuffix(text, "\n") {
		fmt.Fprintln(w)
	}
}

// SandboxWorker is the binary's worker mode. It is started only by Rune,
// inside a sandbox, with the job and the result on two pipes.
func SandboxWorker(ctx context.Context) int {
	job, result, err := workerPipes()
	if err != nil {
		fmt.Fprintf(os.Stderr, "rune: %s is started by rune itself\n", SandboxWorkerArg)
		return ExitUsage
	}
	defer func() { _ = result.Close() }()
	// Here and not only in the server: the endpoint of an (agent) task starts
	// workers too, from a process that cannot mark its own descriptors without
	// taking them from the agent it runs.
	closeInheritedOnExec()
	err = runSandboxWorker(ctx, job, result, os.Stdin, os.Stdout, os.Stderr)
	_ = job.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "rune: sandbox worker: %v\n", err)
		return ExitTaskFail
	}
	return ExitSuccess
}

// sandboxStartError reports a body that did not run because its sandbox did
// not start. The scheduler fires no failure hook for it.
type sandboxStartError struct {
	task   string
	detail string
}

func (e *sandboxStartError) Error() string {
	return fmt.Sprintf("the body of task %q did not run: the sandbox could not start (%s)", e.task, e.detail)
}

func (e *sandboxStartError) Unwrap() error { return scheduler.ErrBodyNotRun }

// sandboxWorkerError reports a worker that started the body and ended
// without saying how it went.
type sandboxWorkerError struct {
	task   string
	detail string
}

func (e *sandboxWorkerError) Error() string {
	return fmt.Sprintf("the worker of task %q ended without a result (%s)", e.task, e.detail)
}

// hintLine is what a failed confined body gets after its own output. The
// program that failed names no cause: it says "Could not resolve host" or
// "Read-only file system".
func hintLine(task string, network bool, workspace string) string {
	if network {
		return fmt.Sprintf("note: task %q ran in the sandbox: it can write only inside %s.", task, workspace)
	}
	return fmt.Sprintf("note: task %q ran in the sandbox: it has no network (it is not marked [network]) and can write only inside %s.", task, workspace)
}

// bodySandbox is what a call's engine needs to confine its bodies: the
// server's state, and the paths of this load.
type bodySandbox struct {
	state     *sandboxState
	workspace string
	// protected are the existing paths a body can read and cannot change.
	protected []string
}

// resolvConf returns the real path of the resolver file when it lies under
// /run, which the sandbox hides, so that a body with the network can still
// resolve names. On a host with systemd-resolved it does.
func resolvConf(resolve func(string) (string, error)) string {
	real, err := resolve("/etc/resolv.conf")
	if err != nil || !sandbox.Within("/run", real) {
		return ""
	}
	return real
}

// runConfined runs one body in a worker inside a sandbox. It is what
// runExecutor is for a body that runs in the server.
func (e *engine) runConfined(task *ast.Task, sel rt.Selection, lines []shell.Line, dir string, env []string, stdout io.Writer) error {
	sb := e.sandbox
	resolved := sandbox.Resolve(dir)
	if !sandbox.Within(sb.workspace, resolved) {
		return &sandboxStartError{task: task.Name, detail: fmt.Sprintf("its working directory %s is outside the project %s", resolved, sb.workspace)}
	}
	// The body starts in the resolved directory: the path as the load wrote
	// it may go through a link that lies where the sandbox hides everything.
	job := sandboxJob{Kind: jobShell, Task: task.Name, Dir: resolved, Env: env, Quiet: e.settings.Quiet || e.opts.Quiet}
	switch sel.Kind {
	case rt.KindShell:
		job.Lines = lines
	case rt.KindInterp:
		job.Kind, job.Script, job.Command = jobInterp, joinBody(lines), sel.Command
		if len(task.Body) > 0 {
			job.Span = task.Body[0].Sp
		}
	case rt.KindAgent:
		// An agent task reached by an agent's call opens no endpoint and
		// gathers no context, so its body is the prompt and nothing more.
		job.Kind, job.Prompt, job.Command = jobAgent, joinBody(lines), e.settings.AgentCmd
	default:
		return &UsageError{Err: errorf("executor %q is not supported yet", sel.Display)}
	}

	tmp, cache, cleanup, err := sb.state.privateDirs()
	if err != nil {
		return &sandboxStartError{task: task.Name, detail: err.Error()}
	}
	defer cleanup()
	hiddenDirs, hiddenFiles := sb.state.hidden()
	spec := sandbox.Spec{
		Workspace:   sb.workspace,
		Protected:   sb.protected,
		HiddenDirs:  hiddenDirs,
		HiddenFiles: hiddenFiles,
		Empty:       sb.state.emptyFile(),
		Tmp:         tmp,
		Cache:       cache,
		Network:     task.Attr(ast.AttrNetwork) != nil,
		VarRunDir:   sandbox.VarRunDir(),
		Exe:         sb.state.exe,
	}
	if spec.Network {
		spec.ResolvConf = resolvConf(sb.state.evalSymlinks)
	}
	result, started, err := sb.state.launch(e.ctx, spec, job, e.opts.Stdin, stdout, e.opts.Stderr)
	switch {
	case e.ctx.Err() != nil:
		return e.ctx.Err()
	case !started:
		// The next call asks the backend again instead of trusting an
		// answer that just proved wrong.
		sb.state.failed()
		return &sandboxStartError{task: task.Name, detail: exitText(err)}
	case err != nil:
		return &sandboxWorkerError{task: task.Name, detail: exitText(err)}
	}
	return errorOf(result, e.ctx)
}

// exitText words how a worker's command ended.
func exitText(err error) string {
	if err == nil || errors.Is(err, errWorkerResult) {
		return "exit status 0"
	}
	return err.Error()
}

// launch starts the worker for one job under spec and waits for it. err is
// how the command ended when it did not end well, or errWorkerResult when it
// ended well and reported nothing usable.
func (s *sandboxState) launch(ctx context.Context, spec sandbox.Spec, job sandboxJob, stdin io.Reader, stdout, stderr io.Writer) (result sandboxResult, started bool, err error) {
	payload, err := json.Marshal(job)
	if err != nil {
		return sandboxResult{}, false, err
	}
	cmd, err := s.command(ctx, spec, []string{s.exe, SandboxWorkerArg})
	if err != nil {
		return sandboxResult{}, false, err
	}
	jobR, jobW, err := os.Pipe()
	if err != nil {
		return sandboxResult{}, false, err
	}
	resultR, resultW, err := os.Pipe()
	if err != nil {
		_ = jobR.Close()
		_ = jobW.Close()
		return sandboxResult{}, false, err
	}
	// The worker gets the server's environment, so an interpreter is found
	// as it is found in the server. Its own temporary files go to the
	// private directory: the server's TMPDIR may not exist in the sandbox.
	cmd.Env = append(os.Environ(), sandboxTmpEnv)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	cmd.ExtraFiles = []*os.File{jobR, resultW}
	cmd.WaitDelay = workerWaitDelay
	startErr := cmd.Start()
	// The child has its own copies now, or never started.
	_ = jobR.Close()
	_ = resultW.Close()
	if startErr != nil {
		_ = jobW.Close()
		_ = resultR.Close()
		return sandboxResult{}, false, startErr
	}
	// A worker that never reads ends with its sandbox, which closes the pipe
	// and ends the write.
	_, _ = jobW.Write(payload)
	_ = jobW.Close()
	result, started, readErr := readWorkerResult(resultR)
	_ = resultR.Close()
	if waitErr := cmd.Wait(); waitErr != nil {
		return result, started, waitErr
	}
	return result, started, readErr
}
