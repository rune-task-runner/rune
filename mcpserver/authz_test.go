package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	refusedClean = `task "clean" is destructive; to permit it, add clean to --allow-destructive and restart the server`
	notAuthGreet = `task "greet" is not authorized for this session`
	grantFlag    = "--allow-destructive"
)

func TestRefusalNonDestructiveCallable(t *testing.T) {
	srv := New(sampleEngine(), Options{})
	if got := srv.refusal("logs"); got != "" {
		t.Errorf("refusal(logs) = %q, want none", got)
	}
}

func TestRefusalDestructiveNeedsGrant(t *testing.T) {
	srv := New(sampleEngine(), Options{AllowDestructiveFlag: grantFlag})
	if got := srv.refusal("clean"); got != refusedClean {
		t.Errorf("refusal(clean) without a grant = %q, want %q", got, refusedClean)
	}
	granted := New(sampleEngine(), Options{AllowDestructive: []string{"clean"}, AllowDestructiveFlag: grantFlag})
	if got := granted.refusal("clean"); got != "" {
		t.Errorf("refusal(clean) with a grant = %q, want none", got)
	}
}

func TestRefusalAllowListNarrows(t *testing.T) {
	srv := New(sampleEngine(), Options{AllowList: []string{"logs"}})
	if got := srv.refusal("logs"); got != "" {
		t.Errorf("allow-listed task refused: %q", got)
	}
	got := srv.refusal("greet")
	if got != notAuthGreet {
		t.Errorf("refusal(greet) = %q, want %q", got, notAuthGreet)
	}
	if strings.Contains(got, "--allow-destructive") {
		t.Errorf("an allowlist refusal must not point at --allow-destructive: %q", got)
	}
}

// The allowlist is checked first, so a granted destructive task outside it is
// refused the same way as any other task outside it (research R5).
func TestRefusalAllowListBeforeGrant(t *testing.T) {
	srv := New(sampleEngine(), Options{AllowList: []string{"logs"}, AllowDestructive: []string{"clean"}})
	want := `task "clean" is not authorized for this session`
	if got := srv.refusal("clean"); got != want {
		t.Errorf("refusal(clean) = %q, want %q", got, want)
	}
}

// The refusal names the Runefile task, not the tool, so the operator can paste
// it into the flag.
func TestRefusalNamesTaskNotTool(t *testing.T) {
	eng := &fakeEngine{tasks: []TaskInfo{{Name: "tools::clean", Destructive: true}}}
	srv := New(eng, Options{AllowDestructiveFlag: grantFlag})
	want := `task "tools::clean" is destructive; to permit it, add tools::clean to --allow-destructive and restart the server`
	if got := srv.refusal("tools::clean"); got != want {
		t.Errorf("refusal(tools::clean) = %q, want %q", got, want)
	}
}

// Without a flag to name, as on an agent task's own endpoint or in an embedder
// that has no such flag, the refusal offers no remedy that cannot work.
func TestRefusalWithoutFlagNamesNoRemedy(t *testing.T) {
	srv := New(sampleEngine(), Options{})
	want := `task "clean" is destructive and is not allowed in this session`
	if got := srv.refusal("clean"); got != want {
		t.Errorf("refusal(clean) = %q, want %q", got, want)
	}
}

func TestDestructiveCallRefusedAtRuntime(t *testing.T) {
	eng := sampleEngine()
	srv := New(eng, Options{AllowDestructiveFlag: grantFlag})
	cs := connect(t, srv)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "clean"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Error("destructive call without a grant should be an error result")
	}
	if got := contentText(res); got != refusedClean {
		t.Errorf("refusal text = %q, want %q", got, refusedClean)
	}
	if eng.lastCall == "clean" {
		t.Error("engine must NOT run a refused destructive task")
	}
}
