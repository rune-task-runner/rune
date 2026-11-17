package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rune-task-runner/rune/internal/eval"
	"github.com/rune-task-runner/rune/mcpserver"
)

// Spec 049: a person's approval of one call reaches the engine's own
// [confirm] gate, for the tasks the request named and for that call only.
// Every body here writes a marker file, so "ran" is read from the disk.

func ranIn(t *testing.T, a *mcpAdapter, name string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(a.workDir, name+".done"))
	return err == nil
}

// asked records what an adapter put to the person and answers with err.
type asked struct {
	requests []mcpserver.ApprovalRequest
	// before runs when the person is asked, before any answer.
	before func()
	err    error
}

func (p *asked) approve(_ context.Context, req mcpserver.ApprovalRequest) error {
	p.requests = append(p.requests, req)
	if p.before != nil {
		p.before()
	}
	return p.err
}

const approvalSrc = `# Builds.
build:
    @echo built > build.done

[confirm("Apply to prod?")]
apply env region="eu":
    @echo {{env}}-{{region}} > apply.done

[confirm("Delete dist?")]
clean:
    @echo cleaned > clean.done

[confirm]
wipe:
    @echo wiped > wipe.done

release: build clean
    @echo released > release.done

both: clean wipe
    @echo both > both.done

[parallel]
together: clean wipe
    @echo together > together.done

publish: && clean
    @echo published > publish.done

deploy: || wipe
    @echo deployed > deploy.done

redeploy: || wipe
    @echo redeployed > redeploy.done
    @exit 1

ship: _scrub
    @echo shipped > ship.done

[private]
[confirm("Scrub?")]
_scrub:
    @echo scrubbed > _scrub.done

pack +files:
    @echo packed > pack.done

lint *paths:
    @echo linted > lint.done
`

func TestCallApprovedRunsConfirmBody(t *testing.T) {
	a := adapterFor(t, approvalSrc)
	ranWhenAsked := true
	who := &asked{before: func() { ranWhenAsked = ranIn(t, a, "apply") }}
	res, err := a.CallApproved(context.Background(), "apply", map[string]string{"env": "prod"}, nil, []string{"apply"}, who.approve)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != ExitSuccess {
		t.Errorf("exit code = %d, stderr %q", res.ExitCode, res.Stderr)
	}
	if ranWhenAsked {
		t.Error("apply had run when the person was asked")
	}
	want := mcpserver.ApprovalRequest{
		Args:          []mcpserver.ApprovalArg{{Name: "env", Values: []string{"prod"}}, {Name: "region", Values: []string{"eu"}}},
		Confirmations: []mcpserver.Confirmation{{Task: "apply", Message: "Apply to prod?"}},
	}
	if len(who.requests) != 1 || !reflect.DeepEqual(who.requests[0], want) {
		t.Errorf("request = %+v, want %+v", who.requests, want)
	}
	// The body ran with no terminal and no --yes: the approval passed the
	// engine's own gate (FR-014, FR-028).
	got, err := os.ReadFile(filepath.Join(a.workDir, "apply.done"))
	if err != nil || string(got) != "prod-eu\n" {
		t.Errorf("apply.done = %q, %v, want prod-eu", got, err)
	}

	// A [confirm] with no text reports an empty message: the server words
	// the default question.
	who = &asked{}
	if _, err := a.CallApproved(context.Background(), "wipe", nil, nil, []string{"wipe"}, who.approve); err != nil {
		t.Fatal(err)
	}
	if got := who.requests[0].Confirmations; !slices.Equal(got, []mcpserver.Confirmation{{Task: "wipe"}}) {
		t.Errorf("confirmations for a [confirm] with no text = %+v", got)
	}
}

func TestCallApprovedStopsOnError(t *testing.T) {
	a := adapterFor(t, approvalSrc)
	sentinel := errors.New(`task "release" was not approved: the request was declined. Nothing ran.`)
	who := &asked{err: sentinel}
	res, err := a.CallApproved(context.Background(), "release", nil, nil, []string{"clean"}, who.approve)
	if err != sentinel { //nolint:errorlint // the contract is the same error value, unwrapped
		t.Errorf("error = %v, want the approve error itself", err)
	}
	if !reflect.DeepEqual(res, mcpserver.Result{}) {
		t.Errorf("result = %+v, want none", res)
	}
	for _, name := range []string{"build", "clean", "release"} {
		if ranIn(t, a, name) {
			t.Errorf("%s ran although the call was not approved", name)
		}
	}
}

func TestCallApprovedBindsFirst(t *testing.T) {
	a := adapterFor(t, approvalSrc)
	who := &asked{}
	_, err := a.CallApproved(context.Background(), "apply", map[string]string{}, nil, []string{"apply"}, who.approve)
	if err == nil || err.Error() != `task "apply" is missing required argument "env"` {
		t.Errorf("error = %v, want the missing-argument error", err)
	}
	if len(who.requests) != 0 {
		t.Error("the person was asked about a call that could not run")
	}
}

func TestApprovalRequest(t *testing.T) {
	a := adapterFor(t, approvalSrc)
	got := a.approvalRequest(a.tasks["apply"], eval.Args{Values: map[string]string{"env": "prod", "region": "us"}}, []string{"apply", "clean", "wipe", "gone", "build"})
	want := mcpserver.ApprovalRequest{
		Args: []mcpserver.ApprovalArg{{Name: "env", Values: []string{"prod"}}, {Name: "region", Values: []string{"us"}}},
		// A name that is no task, and a task with no [confirm], get no entry.
		Confirmations: []mcpserver.Confirmation{{Task: "apply", Message: "Apply to prod?"}, {Task: "clean", Message: "Delete dist?"}, {Task: "wipe"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("approvalRequest = %+v, want %+v", got, want)
	}

	// A variadic reports its elements, so the person sees where one ends.
	got = a.approvalRequest(a.tasks["pack"], eval.Args{
		Values: map[string]string{"files": "a b c"},
		Lists:  map[string][]string{"files": {"a b", "c"}},
	}, nil)
	if want := []mcpserver.ApprovalArg{{Name: "files", Values: []string{"a b", "c"}}}; !reflect.DeepEqual(got.Args, want) {
		t.Errorf("variadic args = %+v, want %+v", got.Args, want)
	}
	got = a.approvalRequest(a.tasks["lint"], eval.Args{Values: map[string]string{"paths": ""}, Lists: map[string][]string{"paths": {}}}, nil)
	if len(got.Args) != 1 || got.Args[0].Name != "paths" || len(got.Args[0].Values) != 0 {
		t.Errorf("empty variadic = %+v, want paths with no value", got.Args)
	}
	if got := a.approvalRequest(a.tasks["build"], eval.Args{}, nil); len(got.Args) != 0 || len(got.Confirmations) != 0 {
		t.Errorf("request for a task with no parameter = %+v", got)
	}
}

func TestCallApprovedVariadic(t *testing.T) {
	a := adapterFor(t, approvalSrc)
	// pack is not destructive itself; the adapter does not judge that.
	who := &asked{}
	_, err := a.CallApproved(context.Background(), "pack", map[string]string{"files": "a b c"}, map[string][]string{"files": {"a b", "c"}}, nil, who.approve)
	if err != nil {
		t.Fatal(err)
	}
	if want := []mcpserver.ApprovalArg{{Name: "files", Values: []string{"a b", "c"}}}; !reflect.DeepEqual(who.requests[0].Args, want) {
		t.Errorf("args = %+v, want %+v", who.requests[0].Args, want)
	}
}

// An approval covers the tasks the request named and no other (FR-012,
// FR-014).
func TestCallApprovedCoversOnlyNamed(t *testing.T) {
	a := adapterFor(t, approvalSrc)
	builtWhenAsked := true
	who := &asked{before: func() { builtWhenAsked = ranIn(t, a, "build") }}
	res, err := a.CallApproved(context.Background(), "release", nil, nil, []string{"clean"}, who.approve)
	if err != nil || res.ExitCode != ExitSuccess {
		t.Fatalf("release: %v, exit %d, stderr %q", err, res.ExitCode, res.Stderr)
	}
	if builtWhenAsked {
		t.Error("build ran before the person answered")
	}
	for _, name := range []string{"build", "clean", "release"} {
		if !ranIn(t, a, name) {
			t.Errorf("%s did not run after approval", name)
		}
	}

	a = adapterFor(t, approvalSrc)
	res, err = a.CallApproved(context.Background(), "both", nil, nil, []string{"clean"}, (&asked{}).approve)
	if err != nil {
		t.Fatal(err)
	}
	if !ranIn(t, a, "clean") || ranIn(t, a, "wipe") || ranIn(t, a, "both") || res.ExitCode == ExitSuccess {
		t.Errorf("both with only clean approved: clean=%v wipe=%v both=%v exit=%d, want only clean to run",
			ranIn(t, a, "clean"), ranIn(t, a, "wipe"), ranIn(t, a, "both"), res.ExitCode)
	}

	a = adapterFor(t, approvalSrc)
	res, err = a.CallApproved(context.Background(), "clean", nil, nil, nil, (&asked{}).approve)
	if err != nil {
		t.Fatal(err)
	}
	if ranIn(t, a, "clean") || res.ExitCode == ExitSuccess {
		t.Errorf("clean ran on an approval that named nothing: exit %d", res.ExitCode)
	}
}

func TestCallApprovedHooksAndPrivate(t *testing.T) {
	cases := []struct {
		call      string
		tasks     []string
		ran       []string
		notRan    []string
		exit      int
		confirmed []mcpserver.Confirmation
	}{
		{"publish", []string{"clean"}, []string{"publish", "clean"}, nil, ExitSuccess, []mcpserver.Confirmation{{Task: "clean", Message: "Delete dist?"}}},
		{"deploy", []string{"wipe"}, []string{"deploy"}, []string{"wipe"}, ExitSuccess, []mcpserver.Confirmation{{Task: "wipe"}}},
		{"redeploy", []string{"wipe"}, []string{"redeploy", "wipe"}, nil, 1, []mcpserver.Confirmation{{Task: "wipe"}}},
		{"ship", []string{"_scrub"}, []string{"_scrub", "ship"}, nil, ExitSuccess, []mcpserver.Confirmation{{Task: "_scrub", Message: "Scrub?"}}},
		{"together", []string{"clean", "wipe"}, []string{"clean", "wipe", "together"}, nil, ExitSuccess, []mcpserver.Confirmation{{Task: "clean", Message: "Delete dist?"}, {Task: "wipe"}}},
	}
	for _, c := range cases {
		a := adapterFor(t, approvalSrc)
		ranWhenAsked := true
		who := &asked{before: func() { ranWhenAsked = ranIn(t, a, c.call) }}
		res, err := a.CallApproved(context.Background(), c.call, nil, nil, c.tasks, who.approve)
		if err != nil {
			t.Fatalf("%s: %v", c.call, err)
		}
		if ranWhenAsked {
			t.Errorf("%s ran before the person answered", c.call)
		}
		if res.ExitCode != c.exit {
			t.Errorf("%s: exit %d, want %d (stderr %q)", c.call, res.ExitCode, c.exit, res.Stderr)
		}
		for _, name := range c.ran {
			if !ranIn(t, a, name) {
				t.Errorf("%s: %s did not run", c.call, name)
			}
		}
		for _, name := range c.notRan {
			if ranIn(t, a, name) {
				t.Errorf("%s: %s ran", c.call, name)
			}
		}
		if !slices.Equal(who.requests[0].Confirmations, c.confirmed) {
			t.Errorf("%s: confirmations = %+v, want %+v", c.call, who.requests[0].Confirmations, c.confirmed)
		}
	}
}

func TestApprovedDoesNotOutliveCall(t *testing.T) {
	a := adapterFor(t, approvalSrc)
	if _, err := a.CallApproved(context.Background(), "clean", nil, nil, []string{"clean"}, (&asked{}).approve); err != nil {
		t.Fatal(err)
	}
	if !ranIn(t, a, "clean") {
		t.Fatal("approved clean did not run")
	}
	if err := os.Remove(filepath.Join(a.workDir, "clean.done")); err != nil {
		t.Fatal(err)
	}
	res, err := a.CallLists(context.Background(), "clean", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ranIn(t, a, "clean") || res.ExitCode == ExitSuccess {
		t.Errorf("a later call ran clean on the earlier approval: exit %d", res.ExitCode)
	}
	if a.grants != nil {
		t.Errorf("an approval changed the adapter's grants: %v", a.grants)
	}
}

// FR-010, FR-020: what the person is shown holds no secret value.
func TestApprovalRequestMasksSecrets(t *testing.T) {
	const secret = "supersecretvalue123"
	src := "[confirm(\"Use " + secret + "?\")]\nnote text *more:\n    @echo noted > note.done\n"
	a := adapterFor(t, src)
	a.baseEnv = []string{"MY_API_TOKEN=" + secret}
	a.maskSet = deriveMaskSet(a.baseEnv, a.tasks, a.settings.Secrets, a.settings.Unmasked)
	who := &asked{}
	args := map[string]string{"text": "pre " + secret + " post", "more": secret + " x"}
	lists := map[string][]string{"more": {secret, "x"}}
	if _, err := a.CallApproved(context.Background(), "note", args, lists, []string{"note"}, who.approve); err != nil {
		t.Fatal(err)
	}
	want := mcpserver.ApprovalRequest{
		Args:          []mcpserver.ApprovalArg{{Name: "text", Values: []string{"pre *** post"}}, {Name: "more", Values: []string{"***", "x"}}},
		Confirmations: []mcpserver.Confirmation{{Task: "note", Message: "Use ***?"}},
	}
	if !reflect.DeepEqual(who.requests[0], want) {
		t.Errorf("request = %+v, want %+v", who.requests[0], want)
	}

	// An adapter with no mask set passes values through and does not panic.
	plain := adapterFor(t, src)
	who = &asked{}
	if _, err := plain.CallApproved(context.Background(), "note", map[string]string{"text": "hello"}, nil, []string{"note"}, who.approve); err != nil {
		t.Fatal(err)
	}
	if got := who.requests[0].Args[0].Values; !slices.Equal(got, []string{"hello"}) {
		t.Errorf("unmasked value = %q", got)
	}
}

// End to end through the server: a client that can show a form approves, and
// the [confirm] body runs with no terminal attached (FR-028).
func TestServerApprovedCallRunsConfirmBody(t *testing.T) {
	a := adapterFor(t, approvalSrc)
	var shown []string
	opts := &mcp.ClientOptions{ElicitationHandler: func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		shown = append(shown, req.Params.Message)
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approved": true}}, nil
	}}
	cs := clientSession(t, mcpserver.New(a, serveMCPOptions("test", "rune", nil, nil, nil, a.limit(), "", false)), opts)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "apply", Arguments: map[string]any{"env": "prod"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("approved apply refused: %q", resultText(res))
	}
	want := "Approve this Rune task call?\n\nTask: apply\nArguments:\n- env: \"prod\"\n- region: \"eu\"\nDestructive tasks it can run:\n- apply: Apply to prod?\n\nApproving runs this call once."
	if len(shown) != 1 || shown[0] != want {
		t.Errorf("request = %q, want %q", shown, want)
	}
	if !ranIn(t, a, "apply") {
		t.Error("apply did not run after approval")
	}
}

// The request shows the question the command line asks for a [confirm] with
// no text. The server words it; this holds the two to the same words.
func TestServerDefaultQuestionMatchesCommandLine(t *testing.T) {
	a := adapterFor(t, approvalSrc)
	var shown []string
	opts := &mcp.ClientOptions{ElicitationHandler: func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		shown = append(shown, req.Params.Message)
		return &mcp.ElicitResult{Action: "decline"}, nil
	}}
	cs := clientSession(t, mcpserver.New(a, serveMCPOptions("test", "rune", nil, nil, nil, a.limit(), "", false)), opts)
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "wipe"}); err != nil {
		t.Fatal(err)
	}
	want := "- wipe: " + confirmQuestion(a.tasks["wipe"], "") + "\n"
	if len(shown) != 1 || !strings.Contains(shown[0], want) {
		t.Errorf("request = %q, want it to hold %q", shown, want)
	}
}
