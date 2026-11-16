package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Issue #45 (MCP-10): the sessions under testdata/clients were recorded from
// real agent clients with testdata/clients/recorder. These tests keep the
// recorder working, and hold each recording to the cases below, which repeat
// what docs/mcp.md reports as observed for its client. No test reads
// docs/mcp.md, so a change to its table and to a case go together.

// recEntry is one line of a recording, as the recorder writes it.
type recEntry struct {
	MS     int64           `json:"ms"`
	From   string          `json:"from"`
	Msg    json.RawMessage `json:"msg"`
	Raw    string          `json:"raw"`
	Stderr string          `json:"stderr"`
	Event  string          `json:"event"`
}

// recMsg is the part of a recorded JSON-RPC message the tests read.
type recMsg struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		Name       string          `json:"name"`
		RequestID  json.RawMessage `json:"requestId"`
		ClientInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"clientInfo"`
	} `json:"params"`
	Result *struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"result"`
}

// recStep is a recorded entry with its message decoded, when it has one.
type recStep struct {
	recEntry
	msg recMsg
}

// decodeRecording decodes a recording and checks its shape as
// contracts/recording-format.md of spec 046 gives it: one kind of content per
// line, a sender on everything but an event, and time that never runs
// backwards.
func decodeRecording(data []byte) ([]recStep, error) {
	var steps []recStep
	var last int64
	for i, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var s recStep
		if err := json.Unmarshal([]byte(line), &s.recEntry); err != nil {
			return nil, fmt.Errorf("line %d is not a recording entry: %w", i+1, err)
		}
		kinds := 0
		for _, set := range []bool{len(s.Msg) > 0, s.Raw != "", s.Stderr != "", s.Event != ""} {
			if set {
				kinds++
			}
		}
		if kinds != 1 {
			return nil, fmt.Errorf("line %d holds %d kinds of content, want exactly 1: %s", i+1, kinds, line)
		}
		if s.Event == "" && s.From != "client" && s.From != "server" {
			return nil, fmt.Errorf("line %d has sender %q, want client or server", i+1, s.From)
		}
		if s.Event != "" && s.From != "" {
			return nil, fmt.Errorf("line %d is an event with sender %q, want none", i+1, s.From)
		}
		if s.MS < last {
			return nil, fmt.Errorf("line %d is at %dms, before the line above it at %dms", i+1, s.MS, last)
		}
		last = s.MS
		if len(s.Msg) > 0 {
			if err := json.Unmarshal(s.Msg, &s.msg); err != nil {
				return nil, fmt.Errorf("line %d holds a message that does not decode: %w", i+1, err)
			}
		}
		steps = append(steps, s)
	}
	return steps, nil
}

// parseRecording is decodeRecording for a recording that must be well formed.
func parseRecording(t *testing.T, data []byte) []recStep {
	t.Helper()
	steps, err := decodeRecording(data)
	if err != nil {
		t.Fatal(err)
	}
	return steps
}

// find returns the index of the first step at or after from that a sender
// sent with the method, or -1.
func find(steps []recStep, from int, sender, method string) int {
	for i := from; i < len(steps); i++ {
		if steps[i].From == sender && steps[i].msg.Method == method {
			return i
		}
	}
	return -1
}

// cancelledCall returns the index of the client's first
// notifications/cancelled and of the tools/call it names, each -1 when the
// recording has none. The call is found by its request ID, because a session
// can make other calls before the one it cancels.
func cancelledCall(steps []recStep) (cancel, call int) {
	cancel = find(steps, 0, "client", "notifications/cancelled")
	if cancel < 0 {
		return -1, -1
	}
	call = slices.IndexFunc(steps, func(s recStep) bool {
		return s.From == "client" && s.msg.Method == "tools/call" &&
			bytes.Equal(s.msg.ID, steps[cancel].msg.Params.RequestID)
	})
	return cancel, call
}

// limitSlackMS is how far from its limit a client's cancellation may be
// recorded. The recorded clients were 54ms and 1ms over.
const limitSlackMS = 500

// atLimit reports whether a cancellation delayMS after a call came at a limit
// of seconds, less than limitSlackMS away from it. The recorder times the
// arrival of both messages, not the client's own timer, so the delay can be a
// little under the limit as well as over it.
func atLimit(delayMS int64, seconds int) bool {
	off := delayMS - int64(seconds)*1000
	return off > -limitSlackMS && off < limitSlackMS
}

// calledTools returns the name of every tools/call the client sent, in order.
func calledTools(steps []recStep) []string {
	var names []string
	for _, s := range steps {
		if s.From == "client" && s.msg.Method == "tools/call" {
			names = append(names, s.msg.Params.Name)
		}
	}
	return names
}

func TestClientRecordings(t *testing.T) {
	const noCancel = -1
	cases := []struct {
		file    string
		client  string
		version string
		// called is every tool call that reached the server, in order. A call
		// the client refused itself is absent.
		called []string
		// relists reports whether the client asked for the tool list again
		// after notifications/tools/list_changed. A recording in which grow ran
		// must have that notification.
		relists bool
		// cancelAfter is the client's limit on a call in seconds: it sends
		// notifications/cancelled at that limit (see atLimit), or noCancel.
		cancelAfter int
	}{
		{
			file: "claude-code/session.jsonl", client: "claude-code", version: "2.1.325",
			called:      []string{"status", "build", "clean", "fetch", "docs-check", "tools__lint", "grow", "slow", "late"},
			relists:     true,
			cancelAfter: noCancel,
		},
		{
			file: "claude-code/approval.jsonl", client: "claude-code", version: "2.1.325",
			called:      nil,
			cancelAfter: noCancel,
		},
		{
			file: "claude-code/timeout.jsonl", client: "claude-code", version: "2.1.325",
			called:      []string{"slow"},
			cancelAfter: 5,
		},
		{
			file: "claude-code/project-config.jsonl", client: "claude-code", version: "2.1.325",
			called:      []string{"status"},
			cancelAfter: noCancel,
		},
		{
			file: "codex/session.jsonl", client: "codex-mcp-client", version: "0.167.0",
			called:      []string{"status", "build", "docs-check", "tools__lint", "grow", "slow"},
			relists:     false,
			cancelAfter: noCancel,
		},
		{
			file: "codex/list-changed-flag.jsonl", client: "codex-mcp-client", version: "0.167.0",
			called:      []string{"grow", "slow", "late"},
			relists:     true,
			cancelAfter: noCancel,
		},
		{
			file: "codex/approval-mode.jsonl", client: "codex-mcp-client", version: "0.167.0",
			called:      []string{"clean", "fetch"},
			cancelAfter: noCancel,
		},
		{
			file: "codex/timeout.jsonl", client: "codex-mcp-client", version: "0.167.0",
			called:      []string{"slow"},
			cancelAfter: 300,
		},
		{
			file: "codex/project-config.jsonl", client: "codex-mcp-client", version: "0.167.0",
			called:      []string{"status"},
			cancelAfter: noCancel,
		},
	}

	seen := map[string]bool{}
	for _, tc := range cases {
		seen[tc.file] = true
		t.Run(tc.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", "clients", filepath.FromSlash(tc.file)))
			if err != nil {
				t.Fatal(err)
			}
			steps := parseRecording(t, data)

			init := find(steps, 0, "client", "initialize")
			if init < 0 {
				t.Fatal("the client never sent initialize")
			}
			if info := steps[init].msg.Params.ClientInfo; info.Name != tc.client || info.Version != tc.version {
				t.Errorf("client is %q %q, want %q %q", info.Name, info.Version, tc.client, tc.version)
			}
			if last := steps[len(steps)-1]; !strings.HasPrefix(last.Event, "server exited with code ") {
				t.Errorf("the recording does not end with the server's exit: %+v", last.recEntry)
			}

			// What the model was given is not on the wire: the client's reply
			// to the session is the evidence for it.
			reply := strings.TrimSuffix(tc.file, ".jsonl") + ".reply.txt"
			text, err := os.ReadFile(filepath.Join("testdata", "clients", filepath.FromSlash(reply)))
			if err != nil {
				t.Errorf("the recording has no reply file: %v", err)
			} else if len(bytes.TrimSpace(text)) == 0 {
				t.Errorf("%s is empty", reply)
			}

			if got := calledTools(steps); !slices.Equal(got, tc.called) {
				t.Errorf("tools called:\n got %v\nwant %v", got, tc.called)
			}

			if changed := find(steps, 0, "server", "notifications/tools/list_changed"); changed >= 0 {
				if got := find(steps, changed, "client", "tools/list") >= 0; got != tc.relists {
					t.Errorf("client listed tools again after list_changed: %v, want %v", got, tc.relists)
				}
			} else if tc.relists || slices.Contains(tc.called, "grow") {
				// Without the notification, a client that did not list again
				// shows nothing about how it handles one.
				t.Error("the recording has no notifications/tools/list_changed")
			}

			cancel, call := cancelledCall(steps)
			if tc.cancelAfter == noCancel {
				if cancel >= 0 {
					t.Errorf("unexpected notifications/cancelled at %dms", steps[cancel].MS)
				}
				return
			}
			if cancel < 0 {
				t.Fatal("the client never sent notifications/cancelled")
			}
			if call < 0 {
				t.Fatalf("cancelled request %s is not a tools/call the client sent", steps[cancel].msg.Params.RequestID)
			}
			if delay := steps[cancel].MS - steps[call].MS; !atLimit(delay, tc.cancelAfter) {
				t.Errorf("cancelled %dms after the call, want within %dms of %ds", delay, limitSlackMS, tc.cancelAfter)
			}
			// Rune stops the task and answers the cancelled call as interrupted.
			answered := false
			for _, s := range steps[cancel:] {
				if s.From == "server" && bytes.Equal(s.msg.ID, steps[call].msg.ID) && s.msg.Result != nil {
					answered = s.msg.Result.IsError && len(s.msg.Result.Content) == 1 &&
						s.msg.Result.Content[0].Text == "[exit 130]"
				}
			}
			if !answered {
				t.Error("the server did not answer the cancelled call with an [exit 130] error")
			}
		})
	}

	// A recording nobody checks is evidence nobody holds to the table.
	found, err := filepath.Glob(filepath.Join("testdata", "clients", "*", "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		rel, err := filepath.Rel(filepath.Join("testdata", "clients"), f)
		if err != nil {
			t.Fatal(err)
		}
		if !seen[filepath.ToSlash(rel)] {
			t.Errorf("recording %s has no case in this test", filepath.ToSlash(rel))
		}
	}
}

// recorderBin compiles testdata/clients/recorder once for the package, next to
// the rune binary, in the directory TestMain removes.
var recorderBin = sync.OnceValues(func() (string, error) {
	root, err := moduleRoot()
	if err != nil {
		return "", err
	}
	bin := filepath.Join(filepath.Dir(runeBin), "recorder"+exeSuffix())
	build := exec.Command("go", "build", "-o", bin, "./test/integration/testdata/clients/recorder")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("%w\n%s", err, out)
	}
	return bin, nil
})

// buildRecorder returns the recorder binary.
func buildRecorder(t *testing.T) string {
	t.Helper()
	bin, err := recorderBin()
	if err != nil {
		t.Fatalf("building the recorder: %v", err)
	}
	return bin
}

// recorded runs the recorder to its end with stdin already closed and returns
// its exit code, what it passed through, and the recording.
func recorded(t *testing.T, bin, dir string, argv ...string) (code int, stdout, stderr string, steps []recStep) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "rec.jsonl")
	ctx, cancel := context.WithTimeout(context.Background(), reloadTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, append([]string{"-out", out}, argv...)...)
	cmd.Dir = dir
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !asExit(err, &exit) {
			t.Fatalf("running the recorder: %v", err)
		}
		code = exit.ExitCode()
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return code, so.String(), se.String(), parseRecording(t, data)
}

func TestClientRecorder_RecordsASession(t *testing.T) {
	bin := buildRecorder(t)
	dir := writeRunefile(t, reloadRunefile)
	out := filepath.Join(t.TempDir(), "rec.jsonl")

	ctx, cancel := context.WithTimeout(context.Background(), reloadTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-out", out, runeBin, "mcp")
	cmd.Dir = dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	// The first frame has spaces and <, > and &, which a JSON encoder would
	// drop or escape: a recording must keep them.
	frames := []string{
		`{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"it <&>","version":"0.0.0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"build","arguments":{}}}`,
	}
	for _, f := range frames {
		if _, err := io.WriteString(stdin, f+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	// The session ends when the client has its answer, as a real one does.
	var passed []string
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		passed = append(passed, sc.Text())
		if strings.Contains(sc.Text(), `"id":2`) {
			break
		}
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, stdout); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("the recorder failed: %v\nstderr:\n%s", err, stderr.String())
	}

	if len(passed) != 2 || !strings.Contains(passed[1], "built") {
		t.Fatalf("stdout passed through:\n%s\nwant the two replies, the second with the task output", strings.Join(passed, "\n"))
	}
	if !strings.Contains(stderr.String(), "rune MCP server on stdio") {
		t.Errorf("stderr was not passed through:\n%s", stderr.String())
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	steps := parseRecording(t, data)
	var client, server, banner, closed, exited int
	for _, s := range steps {
		switch {
		case s.From == "client" && len(s.Msg) > 0:
			if client < len(frames) && string(s.Msg) != frames[client] {
				t.Errorf("client message %d recorded as %s, want it verbatim: %s", client, s.Msg, frames[client])
			}
			client++
		case s.From == "server" && len(s.Msg) > 0:
			if server < len(passed) && string(s.Msg) != passed[server] {
				t.Errorf("server message %d recorded as %s, want it verbatim: %s", server, s.Msg, passed[server])
			}
			server++
		case strings.HasPrefix(s.Stderr, "rune MCP server on stdio"):
			banner++
		case s.Event == "client closed stdin":
			closed++
		case s.Event == "server exited with code 0":
			exited++
		}
	}
	if client != 3 || server != 2 || banner != 1 || closed != 1 || exited != 1 {
		t.Errorf("recorded %d client messages, %d server messages, %d banners, %d stdin closes, %d exits; want 3, 2, 1, 1, 1\n%s",
			client, server, banner, closed, exited, data)
	}
	if last := steps[len(steps)-1]; last.Event != "server exited with code 0" {
		t.Errorf("the last entry is %+v, want the server's exit", last.recEntry)
	}
}

func TestClientRecorder_RecordsTextThatIsNotJSON(t *testing.T) {
	bin := buildRecorder(t)
	code, stdout, _, steps := recorded(t, bin, t.TempDir(), runeBin, "--version")
	if code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
	if stdout == "" {
		t.Fatal("stdout was not passed through")
	}
	raw := slices.IndexFunc(steps, func(s recStep) bool { return s.From == "server" && s.Raw+"\n" == stdout })
	if raw < 0 {
		t.Errorf("recorded %+v, want the version line as raw text from the server: %q", steps, stdout)
	}
}

// A line that is valid JSON but not an object is not a message: recorded as
// msg, decodeRecording would reject the recording.
func TestClientRecorder_RecordsJSONThatIsNotAnObjectAsRaw(t *testing.T) {
	bin := buildRecorder(t)
	dir := writeRunefile(t, "t:\n    @echo 42\n    @echo '[1]'\n")
	code, _, _, steps := recorded(t, bin, dir, runeBin, "t")
	if code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
	for _, want := range []string{"42", "[1]"} {
		if i := slices.IndexFunc(steps, func(s recStep) bool { return s.From == "server" && s.Raw == want }); i < 0 {
			t.Errorf("the line %s was not recorded as raw text: %+v", want, steps)
		}
	}
}

func TestClientRecorder_ExitsWithTheServersCode(t *testing.T) {
	bin := buildRecorder(t)
	dir := writeRunefile(t, "fail:\n    @echo broken >&2; exit 7\n")
	code, _, stderr, steps := recorded(t, bin, dir, runeBin, "fail")
	if code != 7 {
		t.Errorf("exit code %d, want the server's 7", code)
	}
	if !strings.Contains(stderr, "broken") {
		t.Errorf("stderr was not passed through:\n%s", stderr)
	}
	if i := slices.IndexFunc(steps, func(s recStep) bool { return s.From == "server" && s.Stderr == "broken" }); i < 0 {
		t.Errorf("the stderr line was not recorded: %+v", steps)
	}
	if last := steps[len(steps)-1]; last.Event != "server exited with code 7" {
		t.Errorf("the last entry is %+v, want the exit with code 7", last.recEntry)
	}
}

func TestClientRecorder_RejectsBadUsage(t *testing.T) {
	bin := buildRecorder(t)
	out := filepath.Join(t.TempDir(), "rec.jsonl")
	cases := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"no -out", []string{runeBin, "mcp"}, 2, "usage: recorder -out FILE COMMAND [ARG...]"},
		{"no command", []string{"-out", out}, 2, "usage: recorder -out FILE COMMAND [ARG...]"},
		{"command that does not exist", []string{"-out", out, filepath.Join(t.TempDir(), "missing")}, 1, "recorder:"},
		{"recording that cannot be written", []string{"-out", filepath.Join(t.TempDir(), "missing", "rec.jsonl"), runeBin, "mcp"}, 1, "recorder:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin, tc.args...)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			err := cmd.Run()
			var exit *exec.ExitError
			if !asExit(err, &exit) || exit.ExitCode() != tc.code {
				t.Fatalf("got %v, want exit code %d", err, tc.code)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Errorf("stderr %q does not contain %q", stderr.String(), tc.want)
			}
		})
	}
}

// Both recorded clients stop the server with a signal, so a recording must
// show it and the server must still receive it.
func TestClientRecorder_RecordsAndForwardsASignal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a process cannot be sent an interrupt on Windows")
	}
	bin := buildRecorder(t)
	dir := writeRunefile(t, reloadRunefile)
	out := filepath.Join(t.TempDir(), "rec.jsonl")

	cmd := exec.Command(bin, "-out", out, runeBin, "mcp")
	cmd.Dir = dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	watchdog := time.AfterFunc(reloadTimeout, func() { _ = cmd.Process.Kill() })
	defer watchdog.Stop()

	// The banner shows the server is up, so the signal reaches a running one.
	if _, err := bufio.NewReader(stderr).ReadString('\n'); err != nil {
		t.Fatalf("waiting for the server to start: %v", err)
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, stderr); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	var exit *exec.ExitError
	if !asExit(err, &exit) || exit.ExitCode() != 2 {
		t.Fatalf("got %v, want the interrupted server's exit code 2", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	steps := parseRecording(t, data)
	signalled := slices.IndexFunc(steps, func(s recStep) bool { return s.Event == "client sent signal: interrupt" })
	if signalled < 0 {
		t.Fatalf("the signal was not recorded:\n%s", data)
	}
	if last := steps[len(steps)-1]; last.Event != "server exited with code 2" {
		t.Errorf("the last entry is %+v, want the exit with code 2", last.recEntry)
	}
}

// A reply is never recorded above the message it answers, however fast the
// server is. cat answers each line at once, with the line itself.
func TestClientRecorder_RecordsAMessageBeforeItsReply(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs cat")
	}
	bin := buildRecorder(t)
	out := filepath.Join(t.TempDir(), "rec.jsonl")

	const lines = 5000
	var in strings.Builder
	for i := range lines {
		fmt.Fprintf(&in, "{\"n\":%d}\n", i)
	}
	ctx, cancel := context.WithTimeout(context.Background(), reloadTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-out", out, "cat")
	cmd.Stdin = strings.NewReader(in.String())
	if err := cmd.Run(); err != nil {
		t.Fatalf("running the recorder: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	sent := map[string]bool{}
	answered, early := 0, 0
	for _, s := range parseRecording(t, data) {
		switch s.From {
		case "client":
			sent[string(s.Msg)] = true
		case "server":
			answered++
			if !sent[string(s.Msg)] {
				early++
			}
		}
	}
	if answered != lines || early != 0 {
		t.Errorf("recorded %d replies, %d of them above their message; want %d and 0", answered, early, lines)
	}
}

// A server that stops reading is not a client that closed stdin, and the
// message the client sent it is still part of the session.
func TestClientRecorder_ServerThatStopsReading(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	bin := buildRecorder(t)
	out := filepath.Join(t.TempDir(), "rec.jsonl")

	ctx, cancel := context.WithTimeout(context.Background(), reloadTimeout)
	defer cancel()
	// The server stays up until the recording holds the client's message, so
	// the test does not depend on how fast the recorder reads it.
	server := `exec 0<&-; echo ready; n=0; until grep -q '"ping"' "$1" 2>/dev/null || [ $n -ge 200 ]; do n=$((n+1)); sleep 0.05; done`
	cmd := exec.CommandContext(ctx, bin, "-out", out, "sh", "-c", server, "sh", out)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// The server prints ready after it closed its stdin.
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("waiting for the server: %q, %v", line, err)
	}
	const ping = `{"jsonrpc":"2.0","method":"ping"}`
	if _, err := io.WriteString(stdin, ping+"\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, stdout); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("the recorder failed: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	steps := parseRecording(t, data)
	if i := slices.IndexFunc(steps, func(s recStep) bool { return s.From == "client" && string(s.Msg) == ping }); i < 0 {
		t.Errorf("the client's message was not recorded:\n%s", data)
	}
	if i := slices.IndexFunc(steps, func(s recStep) bool { return s.Event == "client closed stdin" }); i >= 0 {
		t.Errorf("recorded that the client closed stdin, which it did not:\n%s", data)
	}
}

// A server killed by a signal has no exit code. The recording says what
// ended it, not that it exited with code -1.
func TestClientRecorder_ServerKilledByASignal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh and signals")
	}
	bin := buildRecorder(t)
	code, _, _, steps := recorded(t, bin, t.TempDir(), "sh", "-c", "kill -KILL $$")
	if code != 1 {
		t.Errorf("exit code %d, want 1", code)
	}
	if last := steps[len(steps)-1]; last.Event != "server ended without an exit code: signal: killed" {
		t.Errorf("the last entry is %+v, want the signal that ended the server", last.recEntry)
	}
}

func TestCancelledCall(t *testing.T) {
	cases := []struct {
		name         string
		recording    string
		cancel, call int
	}{
		{
			name: "the cancelled call is not the first call",
			recording: `{"ms":1,"from":"client","msg":{"id":2,"method":"tools/call","params":{"name":"status"}}}
{"ms":2,"from":"server","msg":{"id":2,"result":{}}}
{"ms":3,"from":"client","msg":{"id":3,"method":"tools/call","params":{"name":"slow"}}}
{"ms":9,"from":"client","msg":{"method":"notifications/cancelled","params":{"requestId":3}}}`,
			cancel: 3, call: 2,
		},
		{
			name:      "no cancellation",
			recording: `{"ms":1,"from":"client","msg":{"id":2,"method":"tools/call","params":{"name":"status"}}}`,
			cancel:    -1, call: -1,
		},
		{
			name: "the cancellation names a request that is not a call",
			recording: `{"ms":1,"from":"client","msg":{"id":2,"method":"tools/call","params":{"name":"slow"}}}
{"ms":9,"from":"client","msg":{"method":"notifications/cancelled","params":{"requestId":7}}}`,
			cancel: 1, call: -1,
		},
		{
			name: "a cancellation from the server is not the client's",
			recording: `{"ms":1,"from":"client","msg":{"id":2,"method":"tools/call","params":{"name":"slow"}}}
{"ms":9,"from":"server","msg":{"method":"notifications/cancelled","params":{"requestId":2}}}`,
			cancel: -1, call: -1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cancel, call := cancelledCall(parseRecording(t, []byte(tc.recording)))
			if cancel != tc.cancel || call != tc.call {
				t.Errorf("got cancel %d and call %d, want %d and %d", cancel, call, tc.cancel, tc.call)
			}
		})
	}
}

// Identifiers and paths a recording or a reply must not hold when it is
// committed: they name the machine and the vendor sessions that made it, and
// show nothing about the client. contracts/recording-format.md of spec 046
// gives the placeholders that replace them.
var (
	homePathRe   = regexp.MustCompile(`/home/|/root/|[/\\]Users[/\\]`)
	uuidRe       = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	uuidHolderRe = regexp.MustCompile(`^00000000-0000-0000-0000-[0-9]{12}$`)
	vendorIDRe   = regexp.MustCompile(`\b(toolu|ctc)_[0-9A-Za-z]*`)
	vendorHoldRe = regexp.MustCompile(`^(toolu|ctc)_[0-9]{4}$`)
)

// privateData returns every home path and every identifier in data that is
// not a placeholder, in the order found.
func privateData(data []byte) []string {
	var found []string
	for _, m := range homePathRe.FindAll(data, -1) {
		found = append(found, string(m))
	}
	for _, m := range uuidRe.FindAll(data, -1) {
		if !uuidHolderRe.Match(m) {
			found = append(found, string(m))
		}
	}
	for _, m := range vendorIDRe.FindAll(data, -1) {
		if !vendorHoldRe.Match(m) {
			found = append(found, string(m))
		}
	}
	return found
}

func TestPrivateData(t *testing.T) {
	cases := []struct {
		name string
		data string
		want []string
	}{
		{"clean", `{"sessionId":"00000000-0000-0000-0000-000000000001","id":"toolu_0001","itemId":"ctc_0012","cwd":"/work/rune"}`, nil},
		{"a Linux home", `{"cwd":"/home/someone/project"}`, []string{"/home/"}},
		{"a macOS home", `{"cwd":"/Users/someone/project"}`, []string{"/Users/"}},
		{"the home of root", `{"cwd":"/root/project"}`, []string{"/root/"}},
		{"a Windows profile, as JSON writes it", `{"cwd":"C:\\Users\\someone"}`, []string{`\Users\`}},
		{"a session UUID", `{"sessionId":"01a1e152-3c05-7ae2-8261-1175e2f31e3e"}`, []string{"01a1e152-3c05-7ae2-8261-1175e2f31e3e"}},
		{"a UUID inside a longer value", `{"callId":"exec-f2694af3-6f80-4f57-81b5-31f52818144c","windowId":"00000000-0000-0000-0000-000000000001:0"}`, []string{"f2694af3-6f80-4f57-81b5-31f52818144c"}},
		{"a placeholder with letters is not one", `{"sessionId":"00000000-0000-0000-0000-00000000000a"}`, []string{"00000000-0000-0000-0000-00000000000a"}},
		{"a tool-use identifier", `{"claudecode/toolUseId":"toolu_01BCDsTUTbN4xXdzVxSrcB8W"}`, []string{"toolu_01BCDsTUTbN4xXdzVxSrcB8W"}},
		{"an item identifier", `{"itemId":"ctc_09dca4bd09655435"}`, []string{"ctc_09dca4bd09655435"}},
		{"a placeholder of the wrong length", `{"a":"toolu_001","b":"ctc_00001"}`, []string{"toolu_001", "ctc_00001"}},
		{"a word that only ends like a prefix", `{"text":"the xtoolu_ value and a path /homestead"}`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := privateData([]byte(tc.data)); !slices.Equal(got, tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClientRecordingsHoldNoPrivateData(t *testing.T) {
	var files []string
	for _, pattern := range []string{"*.jsonl", "*.reply.txt"} {
		found, err := filepath.Glob(filepath.Join("testdata", "clients", "*", pattern))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, found...)
	}
	if len(files) == 0 {
		t.Fatal("found no recording to check")
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if found := privateData(data); len(found) > 0 {
			t.Errorf("%s holds %d paths or identifiers that are not placeholders, the first: %s", filepath.ToSlash(f), len(found), found[0])
		}
	}
}

func TestDecodeRecording(t *testing.T) {
	good := `{"ms":1,"from":"client","msg":{"id":1,"method":"initialize"}}
{"ms":1,"from":"server","stderr":"rune MCP server on stdio"}
{"ms":2,"from":"server","raw":"not json"}
{"ms":3,"event":"client closed stdin"}
{"ms":3,"event":"server exited with code 0"}`
	steps, err := decodeRecording([]byte(good))
	if err != nil {
		t.Fatalf("a well formed recording was rejected: %v", err)
	}
	if len(steps) != 5 || steps[0].msg.Method != "initialize" || steps[4].Event != "server exited with code 0" {
		t.Errorf("decoded %+v", steps)
	}

	bad := []struct {
		name      string
		recording string
		want      string
	}{
		{"not JSON", `{"ms":1,`, "line 1 is not a recording entry"},
		{"no content", `{"ms":1,"from":"client"}`, "line 1 holds 0 kinds of content"},
		{"two kinds of content", `{"ms":1,"from":"server","raw":"x","stderr":"y"}`, "line 1 holds 2 kinds of content"},
		{"a message without a sender", `{"ms":1,"msg":{"id":1}}`, `line 1 has sender ""`},
		{"an unknown sender", `{"ms":1,"from":"proxy","raw":"x"}`, `line 1 has sender "proxy"`},
		{"an event with a sender", `{"ms":1,"from":"client","event":"client closed stdin"}`, `line 1 is an event with sender "client"`},
		{"time runs backwards", "{\"ms\":5,\"event\":\"a\"}\n{\"ms\":4,\"event\":\"b\"}", "line 2 is at 4ms, before the line above it at 5ms"},
		{"a message that is not an object", `{"ms":1,"from":"client","msg":[1]}`, "line 1 holds a message that does not decode"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeRecording([]byte(tc.recording))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got error %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestAtLimit(t *testing.T) {
	cases := []struct {
		delayMS int64
		seconds int
		want    bool
	}{
		{5000, 5, true},
		{5054, 5, true},
		{5499, 5, true},
		{5500, 5, false},
		{5900, 5, false},
		{4999, 5, true},
		{4501, 5, true},
		{4500, 5, false},
		{300001, 300, true},
		{60000, 300, false},
	}
	for _, tc := range cases {
		if got := atLimit(tc.delayMS, tc.seconds); got != tc.want {
			t.Errorf("atLimit(%d, %d) = %v, want %v", tc.delayMS, tc.seconds, got, tc.want)
		}
	}
}
