package mcpserver

import (
	"context"
	"slices"
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
	if got := srv.refusal(TaskInfo{Name: "logs"}); got != "" {
		t.Errorf("refusal(logs) = %q, want none", got)
	}
}

func TestRefusalDestructiveNeedsGrant(t *testing.T) {
	srv := New(sampleEngine(), Options{AllowDestructiveFlag: grantFlag})
	if got := srv.refusal(TaskInfo{Name: "clean", Destructive: true}); got != refusedClean {
		t.Errorf("refusal(clean) without a grant = %q, want %q", got, refusedClean)
	}
	granted := New(sampleEngine(), Options{AllowDestructive: []string{"clean"}, AllowDestructiveFlag: grantFlag})
	if got := granted.refusal(TaskInfo{Name: "clean", Destructive: true}); got != "" {
		t.Errorf("refusal(clean) with a grant = %q, want none", got)
	}
}

func TestRefusalAllowListNarrows(t *testing.T) {
	srv := New(sampleEngine(), Options{AllowList: []string{"logs"}})
	if got := srv.refusal(TaskInfo{Name: "logs"}); got != "" {
		t.Errorf("allow-listed task refused: %q", got)
	}
	got := srv.refusal(TaskInfo{Name: "greet"})
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
	if got := srv.refusal(TaskInfo{Name: "clean", Destructive: true}); got != want {
		t.Errorf("refusal(clean) = %q, want %q", got, want)
	}
}

// The refusal names the Runefile task, not the tool, so the operator can paste
// it into the flag.
func TestRefusalNamesTaskNotTool(t *testing.T) {
	eng := &fakeEngine{tasks: []TaskInfo{{Name: "tools::clean", Destructive: true}}}
	srv := New(eng, Options{AllowDestructiveFlag: grantFlag})
	want := `task "tools::clean" is destructive; to permit it, add tools::clean to --allow-destructive and restart the server`
	if got := srv.refusal(TaskInfo{Name: "tools::clean", Destructive: true}); got != want {
		t.Errorf("refusal(tools::clean) = %q, want %q", got, want)
	}
}

// Without a flag to name, as on an agent task's own endpoint or in an embedder
// that has no such flag, the refusal offers no remedy that cannot work.
func TestRefusalWithoutFlagNamesNoRemedy(t *testing.T) {
	srv := New(sampleEngine(), Options{})
	want := `task "clean" is destructive and is not allowed in this session`
	if got := srv.refusal(TaskInfo{Name: "clean", Destructive: true}); got != want {
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

const refusedRelease = `task "release" can run destructive task "clean"; to permit it, add clean to --allow-destructive and restart the server`

// Spec 034 FR-004: a task that can reach an ungranted destructive task is
// refused before the engine runs, though it is not destructive itself.
func TestRefusalCoversReach(t *testing.T) {
	release := TaskInfo{Name: "release", DestructiveReach: []string{"clean"}}
	eng := &fakeEngine{tasks: []TaskInfo{release}}
	srv := New(eng, Options{AllowDestructiveFlag: grantFlag})
	if got := srv.refusal(release); got != refusedRelease {
		t.Errorf("refusal(release) = %q, want %q", got, refusedRelease)
	}
	res, err := connect(t, srv).CallTool(context.Background(), &mcp.CallToolParams{Name: "release"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || contentText(res) != refusedRelease {
		t.Errorf("call: isError=%v text=%q, want %q", res.IsError, contentText(res), refusedRelease)
	}
	if eng.lastCall != "" {
		t.Errorf("engine ran %q, but release can reach an ungranted clean", eng.lastCall)
	}
}

// refusalCase is one refusal check: a descriptor served alone, the operator's
// grants and flag, and the text the contract gives (spec 034
// contracts/mcp-tools.md).
type refusalCase struct {
	name   string
	info   TaskInfo
	grants []string
	flag   string
	want   string
}

func (c refusalCase) refusal(grants []string) string {
	srv := New(&fakeEngine{tasks: []TaskInfo{c.info}}, Options{AllowDestructive: grants, AllowDestructiveFlag: c.flag})
	return srv.refusal(c.info)
}

var reachedCases = []refusalCase{
	{
		name: "two reached",
		info: TaskInfo{Name: "release", DestructiveReach: []string{"clean", "wipe"}},
		flag: grantFlag,
		want: `task "release" can run destructive tasks "clean", "wipe"; to permit them, add clean,wipe to --allow-destructive and restart the server`,
	},
	{
		name:   "one of two granted",
		info:   TaskInfo{Name: "release", DestructiveReach: []string{"clean", "wipe"}},
		grants: []string{"clean"},
		flag:   grantFlag,
		want:   `task "release" can run destructive task "wipe"; to permit it, add wipe to --allow-destructive and restart the server`,
	},
	{
		name:   "both granted",
		info:   TaskInfo{Name: "release", DestructiveReach: []string{"clean", "wipe"}},
		grants: []string{"clean", "wipe"},
		flag:   grantFlag,
		want:   "",
	},
	{
		name: "imported task",
		info: TaskInfo{Name: "shipit", DestructiveReach: []string{"tools::clean"}},
		flag: grantFlag,
		want: `task "shipit" can run destructive task "tools::clean"; to permit it, add tools::clean to --allow-destructive and restart the server`,
	},
	{
		name: "one reached, no flag",
		info: TaskInfo{Name: "release", DestructiveReach: []string{"clean"}},
		want: `task "release" can run destructive task "clean"; it is not allowed in this session`,
	},
	{
		name: "two reached, no flag",
		info: TaskInfo{Name: "release", DestructiveReach: []string{"clean", "wipe"}},
		want: `task "release" can run destructive tasks "clean", "wipe"; they are not allowed in this session`,
	},
}

// Spec 034 FR-005, FR-006, FR-008: the refusal names every ungranted task the
// call can reach, in the descriptor's order, and drops a granted one.
func TestRefusalNamesReachedTasks(t *testing.T) {
	for _, c := range reachedCases {
		if got := c.refusal(c.grants); got != c.want {
			t.Errorf("%s: refusal = %q, want %q", c.name, got, c.want)
		}
	}
}

// Spec 034 FR-010: the allowlist comes first, as for a destructive task.
func TestRefusalAllowListBeforeReach(t *testing.T) {
	srv := New(sampleEngine(), Options{AllowList: []string{"logs"}, AllowDestructiveFlag: grantFlag})
	want := `task "release" is not authorized for this session`
	if got := srv.refusal(TaskInfo{Name: "release", DestructiveReach: []string{"clean"}}); got != want {
		t.Errorf("refusal(release) = %q, want %q", got, want)
	}
}

// Spec 034 US3.2: an embedder's own flag name is the one the refusal names.
func TestRefusalEmbedderFlag(t *testing.T) {
	srv := New(sampleEngine(), Options{AllowDestructiveFlag: "--grant"})
	want := `task "release" can run destructive task "clean"; to permit it, add clean to --grant and restart the server`
	if got := srv.refusal(TaskInfo{Name: "release", DestructiveReach: []string{"clean"}}); got != want {
		t.Errorf("refusal(release) = %q, want %q", got, want)
	}
}

var ownAndReachedCases = []refusalCase{
	{
		name: "neither granted",
		info: TaskInfo{Name: "reset", Destructive: true, DestructiveReach: []string{"wipe"}},
		flag: grantFlag,
		want: `task "reset" is destructive and can run destructive task "wipe"; to permit them, add reset,wipe to --allow-destructive and restart the server`,
	},
	{
		name:   "own granted",
		info:   TaskInfo{Name: "reset", Destructive: true, DestructiveReach: []string{"wipe"}},
		grants: []string{"reset"},
		flag:   grantFlag,
		want:   `task "reset" can run destructive task "wipe"; to permit it, add wipe to --allow-destructive and restart the server`,
	},
	{
		name:   "reached granted",
		info:   TaskInfo{Name: "reset", Destructive: true, DestructiveReach: []string{"wipe"}},
		grants: []string{"wipe"},
		flag:   grantFlag,
		want:   `task "reset" is destructive; to permit it, add reset to --allow-destructive and restart the server`,
	},
	{
		name:   "both granted",
		info:   TaskInfo{Name: "reset", Destructive: true, DestructiveReach: []string{"wipe"}},
		grants: []string{"reset", "wipe"},
		flag:   grantFlag,
		want:   "",
	},
	{
		name: "neither granted, no flag",
		info: TaskInfo{Name: "reset", Destructive: true, DestructiveReach: []string{"wipe"}},
		want: `task "reset" is destructive and can run destructive task "wipe"; they are not allowed in this session`,
	},
	{
		name:   "reached granted, no flag",
		info:   TaskInfo{Name: "reset", Destructive: true, DestructiveReach: []string{"wipe"}},
		grants: []string{"wipe"},
		want:   `task "reset" is destructive and is not allowed in this session`,
	},
	{
		name: "own and two reached",
		info: TaskInfo{Name: "full", Destructive: true, DestructiveReach: []string{"clean", "wipe"}},
		flag: grantFlag,
		want: `task "full" is destructive and can run destructive tasks "clean", "wipe"; to permit them, add full,clean,wipe to --allow-destructive and restart the server`,
	},
}

// Spec 034 FR-005: each task is named once, even when an embedder's
// descriptor repeats a name or lists the called task itself.
func TestRefusalNamesEachTaskOnce(t *testing.T) {
	for _, c := range []refusalCase{
		{
			name: "repeated and self",
			info: TaskInfo{Name: "reset", Destructive: true, DestructiveReach: []string{"reset", "wipe", "wipe"}},
			flag: grantFlag,
			want: `task "reset" is destructive and can run destructive task "wipe"; to permit them, add reset,wipe to --allow-destructive and restart the server`,
		},
		{
			name: "repeated",
			info: TaskInfo{Name: "release", DestructiveReach: []string{"clean", "clean"}},
			flag: grantFlag,
			want: `task "release" can run destructive task "clean"; to permit it, add clean to --allow-destructive and restart the server`,
		},
	} {
		if got := c.refusal(c.grants); got != c.want {
			t.Errorf("%s: refusal = %q, want %q", c.name, got, c.want)
		}
	}
}

// Spec 034 FR-005, FR-007, FR-011: a destructive task that reaches another is
// named first in one refusal, a grant drops only its own task, and the text
// for the task alone is today's.
func TestRefusalOwnAndReached(t *testing.T) {
	for _, c := range ownAndReachedCases {
		if got := c.refusal(c.grants); got != c.want {
			t.Errorf("%s: refusal = %q, want %q", c.name, got, c.want)
		}
	}
}

// Spec 034 FR-006, SC-003: adding exactly the names a refusal gives to the
// existing grants lets the same call through.
func TestRefusalOneRestartIsEnough(t *testing.T) {
	for _, c := range append(slices.Clone(reachedCases), ownAndReachedCases...) {
		if c.flag == "" || c.want == "" {
			continue
		}
		_, after, ok := strings.Cut(c.want, "; to permit ")
		if !ok {
			t.Fatalf("%s: no remedy in %q", c.name, c.want)
		}
		_, after, _ = strings.Cut(after, "add ")
		names, _, _ := strings.Cut(after, " to "+c.flag)
		grants := append(slices.Clone(c.grants), strings.Split(names, ",")...)
		if got := c.refusal(grants); got != "" {
			t.Errorf("%s: after adding %s, refusal = %q, want none", c.name, names, got)
		}
	}
}

// Spec 034 FR-014, US3.3: an embedder whose engine reports no reach gets
// today's behavior. The call reaches the engine, which decides at run time.
func TestRefusalNilReachUnchanged(t *testing.T) {
	release := TaskInfo{Name: "release"}
	eng := &fakeEngine{tasks: []TaskInfo{release, {Name: "clean", Destructive: true}}}
	srv := New(eng, Options{AllowDestructiveFlag: grantFlag})
	if got := srv.refusal(release); got != "" {
		t.Errorf("refusal(release) = %q, want none", got)
	}
	res, err := connect(t, srv).CallTool(context.Background(), &mcp.CallToolParams{Name: "release"})
	if err != nil {
		t.Fatal(err)
	}
	if eng.lastCall != "release" {
		t.Errorf("the call did not reach the engine: %q", contentText(res))
	}
}
