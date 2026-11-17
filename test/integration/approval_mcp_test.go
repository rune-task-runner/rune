package integration

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Spec 049, issue #47: a call that can run a [confirm] task the operator did
// not grant is put to the person at the client, and runs once on an explicit
// yes. These tests drive the built binary with a client whose elicitation
// handler plays the person. The rows are those of
// specs/049-mcp-confirm-elicitation/quickstart.md.

// approvalRunefile is the quickstart scratch Runefile: every body writes a
// marker file, so "nothing ran" is checkable from the outside.
const approvalRunefile = `# Builds the artifacts.
build:
    @echo built > build.done

# Applies the plan to an environment.
[confirm("Apply to prod? 3 resources change.")]
apply env region="eu":
    @echo {{env}}-{{region}} > apply.done

# Deletes the build output.
[confirm("Delete dist?")]
clean:
    @echo cleaned > clean.done

# Wipes everything.
[confirm]
wipe:
    @echo wiped > wipe.done

# Cuts a release.
release: build clean
    @echo released > release.done

# Publishes, then cleans.
publish: && clean
    @echo published > publish.done

# Deploys, wiping on failure.
deploy: || wipe
    @echo deployed > deploy.done

# Resets, which also wipes.
[confirm("Reset?")]
reset: wipe
    @echo reset > reset.done

# Cleans and wipes.
full: build clean wipe
    @echo full > full.done

# Ships after a private scrub.
ship: _scrub
    @echo shipped > ship.done

[private]
[confirm("Scrub?")]
_scrub:
    @echo scrubbed > scrub.done

# Notes something.
[confirm("Note it?")]
note text:
    @echo {{text}} >> note.done
`

// asker is the person at the client: it keeps every request it is shown and
// answers from a function.
type asker struct {
	answer func(message string) (*mcp.ElicitResult, error)

	mu       sync.Mutex
	messages []string
}

func (a *asker) handle(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
	a.mu.Lock()
	a.messages = append(a.messages, req.Params.Message)
	a.mu.Unlock()
	return a.answer(req.Params.Message)
}

func (a *asker) shown() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.messages)
}

func (a *asker) options() *mcp.ClientOptions {
	return &mcp.ClientOptions{ElicitationHandler: a.handle}
}

func yes(string) (*mcp.ElicitResult, error) {
	return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approved": true}}, nil
}

func no(string) (*mcp.ElicitResult, error) { return &mcp.ElicitResult{Action: "decline"}, nil }

// approvalSession starts the server in dir on a transport, "stdio" or
// "http", with flags and extra environment, and connects a client with
// opts. It returns the session and the server's stderr.
func approvalSession(t *testing.T, transport, dir string, flags, env []string, opts *mcp.ClientOptions) (*mcp.ClientSession, *syncBuffer) {
	t.Helper()
	stderr := &syncBuffer{}
	client := mcp.NewClient(&mcp.Implementation{Name: "it", Version: "v1"}, opts)
	ctx := context.Background()
	if transport == "stdio" {
		cmd := exec.Command(runeBin, append([]string{"mcp", "--color=never"}, flags...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), env...)
		cmd.Stderr = stderr
		cs, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
		if err != nil {
			t.Fatalf("connecting over stdio: %v\nstderr:\n%s", err, stderr.String())
		}
		t.Cleanup(func() { _ = cs.Close() })
		return cs, stderr
	}

	addr := net.JoinHostPort("127.0.0.1", freePort(t, "127.0.0.1"))
	serveCtx, cancel := context.WithCancel(ctx)
	args := append([]string{"serve", "--color=never", "--http", "--addr", addr, "--token-file", writeToken(t, t.TempDir())}, flags...)
	cmd := exec.CommandContext(serveCtx, runeBin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
		http.DefaultTransport.(*http.Transport).CloseIdleConnections()
	})
	waitForText(t, stderr, "rune MCP server on http://", 1)
	httpTransport := &mcp.StreamableClientTransport{
		Endpoint:   "http://" + addr,
		HTTPClient: &http.Client{Transport: reloadBearer{bindWarningToken}},
	}
	var cs *mcp.ClientSession
	var err error
	// The startup line is printed just before the listener is bound.
	for deadline := time.Now().Add(reloadTimeout); ; time.Sleep(50 * time.Millisecond) {
		if cs, err = client.Connect(ctx, httpTransport, nil); err == nil || time.Now().After(deadline) {
			break
		}
	}
	if err != nil {
		t.Fatalf("connecting over HTTP: %v\nstderr:\n%s", err, stderr.String())
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, stderr
}

// callText calls a tool and returns the result's text and isError flag.
func callText(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("calling %s: %v", tool, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func readDone(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return strings.TrimSpace(string(data))
}

// wantDone fails the test unless exactly these marker files exist.
func wantDone(t *testing.T, label, dir string, names ...string) {
	t.Helper()
	var got []string
	for _, path := range doneFiles(t, dir) {
		got = append(got, filepath.Base(path))
	}
	slices.Sort(got)
	slices.Sort(names)
	if !slices.Equal(got, names) {
		t.Errorf("%s: marker files = %q, want %q", label, got, names)
	}
}

// notApproved is the result of a request that did not end in an approval:
// the reason, then the other ways to run the task, with the names the flag
// takes.
func notApproved(task, reason, names string) string {
	return fmt.Sprintf("task %q was not approved: %s. Nothing ran. To run it without a request, the operator can add %s to --allow-destructive and restart the server, or run %q themselves.",
		task, reason, names, "rune "+task)
}

var bothTransports = []string{"stdio", "http"}

// Quickstart A (US1, SC-001, SC-004): the person is asked before anything
// runs, sees the task, its arguments with the default, and the author's
// message, and the approved call runs once, with no restart and no terminal.
func TestApprovalMCP_ApprovedCallRuns(t *testing.T) {
	for _, transport := range bothTransports {
		dir := writeRunefile(t, approvalRunefile)
		var ranWhenAsked []string
		who := &asker{}
		who.answer = func(string) (*mcp.ElicitResult, error) {
			ranWhenAsked = doneFiles(t, dir)
			return yes("")
		}
		cs, _ := approvalSession(t, transport, dir, nil, nil, who.options())
		text, isError := callText(t, cs, "apply", map[string]any{"env": "prod"})
		if isError || text != "[exit 0]" {
			t.Errorf("%s: approved apply: isError=%v text=%q", transport, isError, text)
		}
		want := "Approve this Rune task call?\n\nTask: apply\nArguments:\n- env: \"prod\"\n- region: \"eu\"\n" +
			"Destructive tasks it can run:\n- apply: Apply to prod? 3 resources change.\n\nApproving runs this call once."
		if shown := who.shown(); len(shown) != 1 || shown[0] != want {
			t.Errorf("%s: requests = %q, want one: %q", transport, shown, want)
		}
		if len(ranWhenAsked) != 0 {
			t.Errorf("%s: these ran before the person answered: %q", transport, ranWhenAsked)
		}
		if got := readDone(t, dir, "apply.done"); got != "prod-eu" {
			t.Errorf("%s: apply.done = %q, want prod-eu", transport, got)
		}
	}
}

// Quickstart B and C (US1.3, US5.4): every call is asked about, and a later
// call shows its own arguments.
func TestApprovalMCP_AskedEveryCall(t *testing.T) {
	dir := writeRunefile(t, approvalRunefile)
	who := &asker{answer: yes}
	cs, _ := approvalSession(t, "stdio", dir, nil, nil, who.options())
	for range 2 {
		if text, isError := callText(t, cs, "apply", map[string]any{"env": "staging"}); isError {
			t.Fatalf("approved apply refused: %q", text)
		}
	}
	if n := len(who.shown()); n != 2 {
		t.Errorf("two calls were asked about %d times", n)
	}

	who.answer = no
	text, isError := callText(t, cs, "apply", map[string]any{"env": "prod"})
	if want := notApproved("apply", "the request was declined", "apply"); !isError || text != want {
		t.Errorf("declined apply: isError=%v text=%q, want %q", isError, text, want)
	}
	if shown := who.shown(); len(shown) != 3 || !strings.Contains(shown[2], `- env: "prod"`) {
		t.Errorf("third request = %q, want it to show prod", shown)
	}
	if got := readDone(t, dir, "apply.done"); got != "staging-eu" {
		t.Errorf("apply.done = %q, want the approved call's staging-eu", got)
	}
}

// Quickstart D (US1.4): a [confirm] with no text gets the command line's
// question.
func TestApprovalMCP_DefaultQuestion(t *testing.T) {
	dir := writeRunefile(t, approvalRunefile)
	who := &asker{answer: yes}
	cs, _ := approvalSession(t, "stdio", dir, nil, nil, who.options())
	if text, isError := callText(t, cs, "wipe", nil); isError {
		t.Fatalf("approved wipe refused: %q", text)
	}
	if shown := who.shown(); len(shown) != 1 || !strings.Contains(shown[0], "- wipe: Run \"wipe\"?\n") {
		t.Errorf("request = %q, want the default question", shown)
	}
	wantDone(t, "wipe", dir, "wipe.done")
}

// Quickstart K (US1.7): a granted task is not asked about.
func TestApprovalMCP_GrantedNotAsked(t *testing.T) {
	dir := writeRunefile(t, approvalRunefile)
	who := &asker{answer: no}
	cs, _ := approvalSession(t, "stdio", dir, []string{"--allow-destructive", "clean"}, nil, who.options())
	for _, tool := range []string{"release", "clean"} {
		if text, isError := callText(t, cs, tool, nil); isError {
			t.Errorf("granted %s refused: %q", tool, text)
		}
	}
	if n := len(who.shown()); n != 0 {
		t.Errorf("granted calls were asked about %d times", n)
	}
	wantDone(t, "granted", dir, "build.done", "clean.done", "release.done")
}

// Quickstart F, L, M, N, O (US2, SC-002): anything but an explicit yes runs
// nothing, the dependency that comes first included, and says which case it
// was.
func TestApprovalMCP_NotApprovedRunsNothing(t *testing.T) {
	cases := []struct {
		row        string
		transports []string
		answer     func(string) (*mcp.ElicitResult, error)
		want       string
	}{
		{"F decline", bothTransports, no, notApproved("release", "the request was declined", "clean")},
		{
			"L dismiss",
			[]string{"stdio"},
			func(string) (*mcp.ElicitResult, error) { return &mcp.ElicitResult{Action: "cancel"}, nil },
			notApproved("release", "the request was dismissed", "clean"),
		},
		{"M accept false", []string{"stdio"}, func(string) (*mcp.ElicitResult, error) {
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approved": false}}, nil
		}, notApproved("release", "approval was not given", "clean")},
		{
			"N accept with no content",
			[]string{"stdio"},
			func(string) (*mcp.ElicitResult, error) { return &mcp.ElicitResult{Action: "accept"}, nil },
			notApproved("release", "the client returned no valid answer", "clean"),
		},
		{"N unknown action", []string{"stdio"}, func(string) (*mcp.ElicitResult, error) {
			return &mcp.ElicitResult{Action: "sure", Content: map[string]any{"approved": true}}, nil
		}, notApproved("release", "the client returned no valid answer", "clean")},
		{
			"O client error",
			[]string{"stdio"},
			func(string) (*mcp.ElicitResult, error) { return nil, fmt.Errorf("the dialog crashed") },
			notApproved("release", "the client returned no valid answer", "clean"),
		},
	}
	for _, c := range cases {
		for _, transport := range c.transports {
			dir := writeRunefile(t, approvalRunefile)
			who := &asker{answer: c.answer}
			cs, _ := approvalSession(t, transport, dir, nil, nil, who.options())
			text, isError := callText(t, cs, "release", nil)
			label := c.row + " on " + transport
			if !isError || text != c.want {
				t.Errorf("%s:\n got isError=%v %q\nwant %q", label, isError, text, c.want)
			}
			if n := len(who.shown()); n != 1 {
				t.Errorf("%s: asked %d times, want once", label, n)
			}
			wantDone(t, label, dir)
		}
	}
}

// US2.6: a client that goes away while the person is deciding leaves nothing
// run. The client here speaks raw frames, so it can close its side.
func TestApprovalMCP_ClientGoesAway(t *testing.T) {
	dir := writeRunefile(t, approvalRunefile)
	cmd := exec.Command(runeBin, "mcp", "--color=never")
	cmd.Dir = dir
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
	watchdog := time.AfterFunc(reloadTimeout, func() { _ = cmd.Process.Kill() })
	defer watchdog.Stop()
	frames := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{"elicitation":{}},"clientInfo":{"name":"it","version":"0.0.0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"release","arguments":{}}}`,
	}
	if _, err := stdin.Write([]byte(strings.Join(frames, "\n") + "\n")); err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	asked := false
	for sc.Scan() {
		if strings.Contains(sc.Text(), `"method":"elicitation/create"`) {
			asked = true
			break
		}
	}
	if !asked {
		t.Fatal("the server never sent elicitation/create")
	}
	// The client is gone before anybody answered.
	_ = stdin.Close()
	for sc.Scan() {
	}
	_ = cmd.Wait()
	wantDone(t, "client gone", dir)
}

// Quickstart E, G, H, I, J, S3 (US3): one request names every ungranted
// destructive task the call can run, nothing runs before the answer, and a
// granted task is not named.
func TestApprovalMCP_CoversReach(t *testing.T) {
	cases := []struct {
		row        string
		transports []string
		flags      []string
		tool       string
		lines      string
		done       []string
	}{
		{
			"E release", bothTransports, nil, "release", "Task: release\nDestructive tasks it can run:\n- clean: Delete dist?\n\n",
			[]string{"build.done", "clean.done", "release.done"},
		},
		{
			"G publish",
			[]string{"stdio"},
			nil, "publish", "Task: publish\nDestructive tasks it can run:\n- clean: Delete dist?\n\n",
			[]string{"publish.done", "clean.done"},
		},
		{
			"G deploy",
			[]string{"stdio"},
			nil, "deploy", "Task: deploy\nDestructive tasks it can run:\n- wipe: Run \"wipe\"?\n\n",
			[]string{"deploy.done"},
		},
		{
			"H reset", bothTransports, nil, "reset", "Task: reset\nDestructive tasks it can run:\n- reset: Reset?\n- wipe: Run \"wipe\"?\n\n",
			[]string{"reset.done", "wipe.done"},
		},
		{
			"I ship",
			[]string{"stdio"},
			nil, "ship", "Task: ship\nDestructive tasks it can run:\n- _scrub: Scrub?\n\n",
			[]string{"scrub.done", "ship.done"},
		},
		{
			"J full with clean granted",
			[]string{"stdio"},
			[]string{"--allow-destructive", "clean"},
			"full",
			"Task: full\nDestructive tasks it can run:\n- wipe: Run \"wipe\"?\n\n",
			[]string{"build.done", "clean.done", "wipe.done", "full.done"},
		},
		{
			"S3 release with clean denied",
			[]string{"stdio"},
			[]string{"--deny", "clean"},
			"release",
			"Task: release\nDestructive tasks it can run:\n- clean: Delete dist?\n\n",
			[]string{"build.done", "clean.done", "release.done"},
		},
	}
	for _, c := range cases {
		for _, transport := range c.transports {
			label := c.row + " on " + transport
			dir := writeRunefile(t, approvalRunefile)
			var ranWhenAsked []string
			who := &asker{}
			who.answer = func(string) (*mcp.ElicitResult, error) {
				ranWhenAsked = doneFiles(t, dir)
				return yes("")
			}
			cs, _ := approvalSession(t, transport, dir, c.flags, nil, who.options())
			if text, isError := callText(t, cs, c.tool, nil); isError {
				t.Errorf("%s: approved call refused: %q", label, text)
			}
			if shown := who.shown(); len(shown) != 1 || !strings.Contains(shown[0], c.lines) {
				t.Errorf("%s: requests = %q, want one holding %q", label, shown, c.lines)
			}
			if len(ranWhenAsked) != 0 {
				t.Errorf("%s: these ran before the person answered: %q", label, ranWhenAsked)
			}
			wantDone(t, label, dir, c.done...)
		}
	}

	// The private task that was approved as a dependency is still not a tool.
	dir := writeRunefile(t, approvalRunefile)
	cs, _ := approvalSession(t, "stdio", dir, nil, nil, (&asker{answer: yes}).options())
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		if tool.Name == "_scrub" {
			t.Error("_scrub is listed as a tool")
		}
	}
}

// Quickstart S, T, U (US5, FR-004, FR-009): a call that could not run is not
// put to the person, nothing the agent sends counts as approval, and of two
// calls at once only the approved one runs.
func TestApprovalMCP_OneCallOnly(t *testing.T) {
	dir := writeRunefile(t, approvalRunefile)
	who := &asker{answer: yes}
	cs, _ := approvalSession(t, "stdio", dir, nil, nil, who.options())
	bad := []struct {
		row  string
		args map[string]any
		want string
	}{
		{"S unknown argument", map[string]any{"env": "prod", "bogus": 1}, `unknown argument "bogus" for task "apply"`},
		{"S missing argument", map[string]any{}, `task "apply" is missing required argument "env"`},
		{"S invalid value", map[string]any{"env": map[string]any{"a": 1}}, `invalid arguments: parameter "env": an object is not a valid value`},
		{"T approved as an argument", map[string]any{"env": "prod", "approved": true}, `unknown argument "approved" for task "apply"`},
	}
	for _, c := range bad {
		if text, isError := callText(t, cs, "apply", c.args); !isError || text != c.want {
			t.Errorf("%s: isError=%v text=%q, want %q", c.row, isError, text, c.want)
		}
	}
	if n := len(who.shown()); n != 0 {
		t.Errorf("calls that could not run were asked about %d times", n)
	}
	wantDone(t, "calls that could not run", dir)

	// U: approve the call whose text is a, decline the other.
	who.answer = func(message string) (*mcp.ElicitResult, error) {
		if strings.Contains(message, `- text: "a"`) {
			return yes("")
		}
		return no("")
	}
	results := map[string]bool{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, text := range []string{"a", "b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "note", Arguments: map[string]any{"text": text}})
			if err != nil {
				t.Errorf("note %s: %v", text, err)
				return
			}
			mu.Lock()
			results[text] = res.IsError
			mu.Unlock()
		}()
	}
	wg.Wait()
	if results["a"] || !results["b"] {
		t.Errorf("isError by text = %v, want a to run and b to be declined", results)
	}
	if got := readDone(t, dir, "note.done"); got != "a" {
		t.Errorf("note.done = %q, want exactly a", got)
	}
}

// Quickstart W, W2, W3 (US5.5, FR-013): a reload while the person is
// deciding makes the approval unusable. A save that does not load, and a
// server that never reloads, leave it valid: what was shown is what runs.
func TestApprovalMCP_ReloadWhilePending(t *testing.T) {
	cases := []struct {
		row     string
		flags   []string
		content string
		wait    string
		want    string // the refusal, or "" when the call runs
	}{
		{
			"W reload", nil, approvalRunefile + "\n# Added.\nlate:\n    @echo late\n", "reloaded Runefile",
			`task "clean" was not approved: the Runefile changed while the request was pending; call again. Nothing ran.`,
		},
		{"W2 save that does not load", nil, approvalRunefile + "\nbroken: nowhere\n    @echo x\n", "reload failed", ""},
		{"W3 no reload", []string{"--no-reload"}, approvalRunefile + "\n# Added.\nlate:\n    @echo late\n", "", ""},
	}
	for _, c := range cases {
		dir := writeRunefile(t, approvalRunefile)
		var stderr *syncBuffer
		who := &asker{}
		who.answer = func(string) (*mcp.ElicitResult, error) {
			if err := os.WriteFile(filepath.Join(dir, "Runefile"), []byte(c.content), 0o644); err != nil {
				return nil, err
			}
			if c.wait != "" {
				waitForText(t, stderr, c.wait, 1)
			}
			return yes("")
		}
		var cs *mcp.ClientSession
		cs, stderr = approvalSession(t, "stdio", dir, c.flags, nil, who.options())
		text, isError := callText(t, cs, "clean", nil)
		if c.want != "" {
			if !isError || text != c.want {
				t.Errorf("%s: isError=%v text=%q, want %q", c.row, isError, text, c.want)
			}
			wantDone(t, c.row, dir)
			continue
		}
		if isError {
			t.Errorf("%s: approved clean refused: %q", c.row, text)
		}
		wantDone(t, c.row, dir, "clean.done")
	}
}

// Quickstart P, Q, R, S2 (US4): a client that cannot show a form is not
// asked and is told the way out; elicitation with no mode is form support;
// a hidden task is never asked about.
func TestApprovalMCP_ClientCannotBeAsked(t *testing.T) {
	for _, transport := range bothTransports {
		dir := writeRunefile(t, approvalRunefile)
		cs, _ := approvalSession(t, transport, dir, nil, nil, nil)
		text, isError := callText(t, cs, "apply", map[string]any{"env": "prod"})
		checkRefused(t, "P apply on "+transport, dir, text, isError, refusedText("apply")+cannotAskSuffix("apply"))
	}

	dir := writeRunefile(t, approvalRunefile)
	who := &asker{answer: yes}
	urlOnly := &mcp.ClientOptions{
		ElicitationHandler: who.handle,
		Capabilities:       &mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapabilities{URL: &mcp.URLElicitationCapabilities{}}},
	}
	cs, _ := approvalSession(t, "stdio", dir, nil, nil, urlOnly)
	text, isError := callText(t, cs, "release", nil)
	want := `task "release" can run destructive task "clean"; to permit it, add clean to --allow-destructive and restart the server` + cannotAskSuffix("release")
	checkRefused(t, "Q release, URL only", dir, text, isError, want)
	if n := len(who.shown()); n != 0 {
		t.Errorf("a URL-only client was sent %d form requests", n)
	}

	dir = writeRunefile(t, approvalRunefile)
	who = &asker{answer: yes}
	bare := &mcp.ClientOptions{
		ElicitationHandler: who.handle,
		Capabilities:       &mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapabilities{}},
	}
	cs, _ = approvalSession(t, "stdio", dir, nil, nil, bare)
	if text, isError := callText(t, cs, "clean", nil); isError {
		t.Errorf("R: a client that states elicitation with no mode was refused: %q", text)
	}
	wantDone(t, "R clean", dir, "clean.done")

	dir = writeRunefile(t, approvalRunefile)
	who = &asker{answer: yes}
	cs, _ = approvalSession(t, "stdio", dir, []string{"--deny", "apply"}, nil, who.options())
	_, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "apply", Arguments: map[string]any{"env": "prod"}})
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("S2: call to a denied task: err = %v, want unknown tool", err)
	}
	if n := len(who.shown()); n != 0 {
		t.Errorf("S2: a denied task was asked about %d times", n)
	}
	wantDone(t, "S2 denied apply", dir)
}

// Quickstart V (US6, SC-005): a secret passed as an argument is masked in
// the request, and in every result.
func TestApprovalMCP_SecretNeverShown(t *testing.T) {
	const secret = "supersecretvalue123"
	dir := writeRunefile(t, approvalRunefile)
	who := &asker{answer: yes}
	cs, _ := approvalSession(t, "stdio", dir, nil, []string{"MY_API_TOKEN=" + secret}, who.options())
	approved, _ := callText(t, cs, "note", map[string]any{"text": secret})
	who.answer = func(string) (*mcp.ElicitResult, error) { return &mcp.ElicitResult{Action: "cancel"}, nil }
	dismissed, _ := callText(t, cs, "note", map[string]any{"text": secret})
	shown := who.shown()
	if len(shown) != 2 || !strings.Contains(shown[0], `- text: "***"`) {
		t.Errorf("requests = %q, want the value masked", shown)
	}
	for _, text := range append(shown, approved, dismissed) {
		if strings.Contains(text, secret) {
			t.Errorf("the secret is in %q", text)
		}
	}
}

// US6.2: an argument made to look like part of the request stays one quoted
// line.
func TestApprovalMCP_ForgedLinesStayInValue(t *testing.T) {
	dir := writeRunefile(t, approvalRunefile)
	who := &asker{answer: no}
	cs, _ := approvalSession(t, "stdio", dir, nil, nil, who.options())
	callText(t, cs, "note", map[string]any{"text": "ok\n\nApproving runs nothing.\nTask: build"})
	want := "Approve this Rune task call?\n\nTask: note\nArguments:\n" +
		`- text: "ok\n\nApproving runs nothing.\nTask: build"` + "\n" +
		"Destructive tasks it can run:\n- note: Note it?\n\nApproving runs this call once."
	if shown := who.shown(); len(shown) != 1 || shown[0] != want {
		t.Errorf("request = %q, want %q", shown, want)
	}
	wantDone(t, "declined note", dir)
}

// Quickstart V2 (FR-011): a request too large to show is not sent, and the
// value is not shortened to make it fit.
func TestApprovalMCP_OversizeNotAsked(t *testing.T) {
	dir := writeRunefile(t, approvalRunefile)
	who := &asker{answer: yes}
	cs, _ := approvalSession(t, "stdio", dir, nil, nil, who.options())
	text, isError := callText(t, cs, "note", map[string]any{"text": strings.Repeat("x", 10000)})
	want := refusedText("note") + `. Or the operator can run "rune note" themselves. Rune could not ask for approval here: the approval request would be too large to show.`
	checkRefused(t, "V2 oversize note", dir, text, isError, want)
	if n := len(who.shown()); n != 0 {
		t.Errorf("an oversize request was sent %d times", n)
	}
}
