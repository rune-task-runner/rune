package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rune-task-runner/rune/internal/planstore"
)

// Spec 051, issue #49: a task with [plan] stores its plan, and a task with
// [applies] runs only with that stored plan, after the person at the client
// approved it, once. These tests drive the built binary with an SDK client.
// The letters are those of specs/051-mcp-plan-apply-binding/quickstart.md.

// planApplyRunefile is the quickstart scratch Runefile. The stand-in for the
// tool is written in Rune's own shell, so it runs on every host: a plan holds
// the state serial it was made for, and the apply fails when the serial has
// moved, which is the tool's stale-plan check. Every body writes a marker.
const planApplyRunefile = `# Plan the change.
[plan(file="tfplan", summary="tfplan.txt")]
plan env:enum("staging","prod"):
    @read -r serial < state.serial; echo "plan env={{env}} serial=$serial" > tfplan; echo "Plan: 1 to add for {{env}}" > tfplan.txt; echo planned >> plan.done

# Apply the reviewed plan.
[applies("plan")]
apply env:enum("staging","prod"):
    @read -r serial < state.serial; read -r line < "$RUNE_PLAN_FILE"; case "$line" in *"serial=$serial") ;; *) echo "Saved plan is stale" >&2; exit 1 ;; esac; echo "$line" > applied.txt; echo $((serial + 1)) > state.serial; echo applied >> apply.done
`

const applyNotRun = `task "apply" was not run: `

var prodArgs = map[string]any{"env": "prod"}

// planProject writes a Runefile and the stand-in's state.
func planProject(t *testing.T, runefile string) string {
	t.Helper()
	dir := writeRunefile(t, runefile)
	writeFile(t, filepath.Join(dir, "state.serial"), "1\n")
	return dir
}

func plansDir(dir string) string { return filepath.Join(dir, ".rune", "plans") }

// storedPlans reads every record of the store, oldest first.
func storedPlans(t *testing.T, dir string) []planstore.Record {
	t.Helper()
	entries, err := os.ReadDir(plansDir(dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var recs []planstore.Record
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(plansDir(dir), entry.Name(), "record.json"))
		if err != nil {
			continue
		}
		var rec planstore.Record
		if err := json.Unmarshal(data, &rec); err != nil {
			t.Fatalf("record of %s: %v", entry.Name(), err)
		}
		recs = append(recs, rec)
	}
	slices.SortFunc(recs, func(x, y planstore.Record) int { return x.Created.Compare(y.Created) })
	return recs
}

func planEvents(t *testing.T, dir string) []planstore.Event {
	t.Helper()
	f, err := os.Open(filepath.Join(plansDir(dir), "journal.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var events []planstore.Event
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var ev planstore.Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			t.Fatalf("journal line %q: %v", sc.Text(), err)
		}
		events = append(events, ev)
	}
	return events
}

// makePlan calls the plan tool and returns the record it stored.
func makePlan(t *testing.T, cs *mcp.ClientSession, dir string, args map[string]any) planstore.Record {
	t.Helper()
	text, isErr := callText(t, cs, "plan", args)
	if isErr {
		t.Fatalf("plan: %s", text)
	}
	recs := storedPlans(t, dir)
	if len(recs) == 0 {
		t.Fatalf("plan stored no record: %s", text)
	}
	rec := recs[len(recs)-1]
	if want := "plan " + rec.ID + ": sha256:" + rec.Artifact.SHA256; !strings.Contains(text, want) {
		t.Errorf("plan result = %q, want the plan line %q", text, want)
	}
	return rec
}

// wantApplyRefused fails unless the call was refused with the text, nobody
// was asked, and the apply body did not run.
func wantApplyRefused(t *testing.T, label, dir string, who *asker, text string, isErr bool, want string) {
	t.Helper()
	if !isErr || text != want {
		t.Errorf("%s: isError=%v text=%q\nwant %q", label, isErr, text, want)
	}
	if who != nil && len(who.shown()) != 0 {
		t.Errorf("%s: the person was asked %d times", label, len(who.shown()))
	}
	if _, err := os.Stat(filepath.Join(dir, "apply.done")); err == nil {
		t.Errorf("%s: the apply body ran", label)
	}
}

func rewriteStoredFile(t *testing.T, dir string, rec planstore.Record, file, content string) {
	t.Helper()
	path := filepath.Join(plansDir(dir), rec.ID, file)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Quickstart A, B, C and AB (US1, US3, US7, SC-001): the person is shown the
// plan, the apply runs the stored bytes once, and the journal links the
// three steps without holding the plan.
func TestPlanApplyMCP_ApprovedApplyRunsStoredPlan(t *testing.T) {
	for _, transport := range bothTransports {
		t.Run(transport, func(t *testing.T) {
			dir := planProject(t, planApplyRunefile)
			who := &asker{answer: yes}
			cs, _ := approvalSession(t, transport, dir, nil, nil, who.options())

			rec := makePlan(t, cs, dir, prodArgs)
			for _, name := range []string{"tfplan", "tfplan.txt"} {
				if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("%s is still in the workspace: %v", name, err)
				}
			}
			artifact, err := os.ReadFile(filepath.Join(plansDir(dir), rec.ID, "artifact"))
			if err != nil || string(artifact) != "plan env=prod serial=1\n" {
				t.Fatalf("stored artifact = %q, %v", artifact, err)
			}

			text, isErr := callText(t, cs, "apply", prodArgs)
			if isErr || !strings.HasSuffix(text, "[exit 0]") {
				t.Fatalf("apply: isError=%v text=%q", isErr, text)
			}
			want := "Approve this Rune task call?\n\nTask: apply\nArguments:\n- env: \"prod\"\n" +
				"Plan: " + rec.ID + " made by \"plan\" at " + rec.Created.UTC().Format(time.RFC3339) + "\n" +
				fmt.Sprintf("Artifact: sha256:%s (%d bytes)\n", rec.Artifact.SHA256, len(artifact)) +
				"Summary:\n  Plan: 1 to add for prod\n" +
				"Destructive tasks it can run:\n- apply: Run \"apply\"?\n\nApproving applies this plan once."
			if shown := who.shown(); len(shown) != 1 || shown[0] != want {
				t.Errorf("requests = %q\nwant one: %q", shown, want)
			}
			if got := readDone(t, dir, "applied.txt"); got != "plan env=prod serial=1" {
				t.Errorf("the apply body read %q, want the stored plan", got)
			}
			if got := readDone(t, dir, "plan.done"); got != "planned" {
				t.Errorf("plan.done = %q: the plan body ran again", got)
			}
			if _, err := os.Stat(filepath.Join(plansDir(dir), rec.ID, "artifact")); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("the artifact is still stored after the apply: %v", err)
			}

			// C: one approval is one attempt.
			text, isErr = callText(t, cs, "apply", prodArgs)
			if !isErr || !strings.HasPrefix(text, applyNotRun+"plan "+rec.ID+" was already applied at ") {
				t.Errorf("second apply: isError=%v text=%q", isErr, text)
			}
			if len(who.shown()) != 1 || readDone(t, dir, "apply.done") != "applied" {
				t.Errorf("second apply: %d requests, apply.done %q", len(who.shown()), readDone(t, dir, "apply.done"))
			}

			// AB: the journal and the store.
			events := planEvents(t, dir)
			var names []string
			for _, ev := range events {
				names = append(names, ev.Event)
				if ev.Plan != rec.ID || ev.Digest != rec.Artifact.SHA256 || ev.Surface != "mcp" {
					t.Errorf("event %s = %+v", ev.Event, ev)
				}
			}
			wantNames := []string{"plan-created", "approval-requested", "approval-given", "apply-started", "apply-finished", "apply-refused"}
			if !slices.Equal(names, wantNames) {
				t.Fatalf("journal = %v, want %v", names, wantNames)
			}
			if events[3].Attempt == "" || events[3].Attempt != events[4].Attempt || events[4].State != "applied" {
				t.Errorf("apply events = %+v, %+v", events[3], events[4])
			}
			journal, _ := os.ReadFile(filepath.Join(plansDir(dir), "journal.jsonl"))
			record, _ := os.ReadFile(filepath.Join(plansDir(dir), rec.ID, "record.json"))
			for _, stored := range []string{"serial=1", "1 to add"} {
				if bytes.Contains(journal, []byte(stored)) || bytes.Contains(record, []byte(stored)) {
					t.Errorf("the journal or the record holds %q of the plan or its summary", stored)
				}
			}
			if runtime.GOOS != "windows" {
				for _, path := range []string{plansDir(dir), filepath.Join(plansDir(dir), rec.ID)} {
					if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o700 {
						t.Errorf("%s: mode %v, %v, want 0700", path, info.Mode().Perm(), err)
					}
				}
				for _, path := range []string{filepath.Join(plansDir(dir), "journal.jsonl"), filepath.Join(plansDir(dir), rec.ID, "record.json")} {
					if info, err := os.Stat(path); err != nil || info.Mode().Perm()&0o077 != 0 {
						t.Errorf("%s: mode %v, %v, want nothing for group or others", path, info.Mode().Perm(), err)
					}
				}
			}
		})
	}
}

// Quickstart D, E, F, AD, L (US2): a call for anything but the reviewed plan
// is refused before the person is asked.
func TestPlanApplyMCP_RefusedBeforeAsking(t *testing.T) {
	for _, transport := range bothTransports {
		t.Run(transport, func(t *testing.T) {
			t.Run("no plan", func(t *testing.T) {
				dir := planProject(t, planApplyRunefile)
				who := &asker{answer: yes}
				cs, _ := approvalSession(t, transport, dir, nil, nil, who.options())
				text, isErr := callText(t, cs, "apply", prodArgs)
				wantApplyRefused(t, "D", dir, who, text, isErr, applyNotRun+`there is no plan for env="prod"; run "plan" with the same arguments first`)

				makePlan(t, cs, dir, map[string]any{"env": "staging"})
				text, isErr = callText(t, cs, "apply", prodArgs)
				wantApplyRefused(t, "E", dir, who, text, isErr, applyNotRun+`there is no plan for env="prod"; run "plan" with the same arguments first`)
			})

			for _, file := range []string{"artifact", "summary"} {
				t.Run("the stored "+file+" was overwritten", func(t *testing.T) {
					dir := planProject(t, planApplyRunefile)
					who := &asker{answer: yes}
					cs, _ := approvalSession(t, transport, dir, nil, nil, who.options())
					rec := makePlan(t, cs, dir, prodArgs)
					rewriteStoredFile(t, dir, rec, file, "other bytes\n")
					text, isErr := callText(t, cs, "apply", prodArgs)
					wantApplyRefused(t, "F, AD", dir, who, text, isErr,
						fmt.Sprintf(`%sthe stored %s of plan %s does not have the recorded digest; run "plan" again`, applyNotRun, file, rec.ID))
				})
			}

			t.Run("the stored artifact is a link", func(t *testing.T) {
				if runtime.GOOS == "windows" {
					t.Skip("symbolic links need privileges on Windows")
				}
				dir := planProject(t, planApplyRunefile)
				who := &asker{answer: yes}
				cs, _ := approvalSession(t, transport, dir, nil, nil, who.options())
				rec := makePlan(t, cs, dir, prodArgs)
				path := filepath.Join(plansDir(dir), rec.ID, "artifact")
				writeFile(t, filepath.Join(dir, "elsewhere"), "plan env=prod serial=1\n")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(dir, "elsewhere"), path); err != nil {
					t.Fatal(err)
				}
				text, isErr := callText(t, cs, "apply", prodArgs)
				wantApplyRefused(t, "G", dir, who, text, isErr,
					fmt.Sprintf(`%sthe stored artifact of plan %s is not a regular file; run "plan" again`, applyNotRun, rec.ID))
			})

			t.Run("the Runefile changed", func(t *testing.T) {
				dir := planProject(t, planApplyRunefile)
				who := &asker{answer: yes}
				cs, _ := approvalSession(t, transport, dir, nil, nil, who.options())
				rec := makePlan(t, cs, dir, prodArgs)
				writeFile(t, filepath.Join(dir, "Runefile"), planApplyRunefile+"\n# Added after the plan.\nextra:\n    @echo extra\n")
				for deadline := time.Now().Add(reloadTimeout); ; time.Sleep(50 * time.Millisecond) {
					if _, ok := policyTools(t, cs)["extra"]; ok {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("the server did not reload the Runefile")
					}
				}
				text, isErr := callText(t, cs, "apply", prodArgs)
				wantApplyRefused(t, "H", dir, who, text, isErr,
					fmt.Sprintf(`%splan %s no longer matches: the Runefile definition changed since it was made; run "plan" again`, applyNotRun, rec.ID))
			})

			t.Run("a new plan replaces the old one", func(t *testing.T) {
				dir := planProject(t, planApplyRunefile)
				who := &asker{answer: yes}
				cs, _ := approvalSession(t, transport, dir, nil, nil, who.options())
				first := makePlan(t, cs, dir, prodArgs)
				second := makePlan(t, cs, dir, prodArgs)
				if _, err := os.Stat(filepath.Join(plansDir(dir), first.ID, "artifact")); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("L: the replaced plan keeps its artifact: %v", err)
				}
				if text, isErr := callText(t, cs, "apply", prodArgs); isErr {
					t.Fatalf("L: apply: %s", text)
				}
				if shown := who.shown(); len(shown) != 1 || !strings.Contains(shown[0], "Plan: "+second.ID+" made by") {
					t.Errorf("L: requests = %q, want one about plan %s", shown, second.ID)
				}
			})
		})
	}
}

// Quickstart I, K, M, N, O, Q, R, S (US2, US3): the refusals that do not
// depend on the transport.
func TestPlanApplyMCP_RefusalsOnStdio(t *testing.T) {
	t.Run("the tool changed", func(t *testing.T) {
		tools := t.TempDir()
		name := "faketool"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		if err := os.WriteFile(filepath.Join(tools, name), []byte("version one"), 0o755); err != nil {
			t.Fatal(err)
		}
		runefile := strings.Replace(planApplyRunefile, `summary="tfplan.txt")]`, `summary="tfplan.txt", tool="faketool")]`, 1)
		dir := planProject(t, runefile)
		who := &asker{answer: yes}
		path := "PATH=" + tools + string(os.PathListSeparator) + os.Getenv("PATH")
		cs, _ := approvalSession(t, "stdio", dir, nil, []string{path}, who.options())
		rec := makePlan(t, cs, dir, prodArgs)
		if rec.Binding.Tool != "faketool" || rec.Binding.ToolDigest == "" {
			t.Fatalf("binding = %+v, want the tool's identity", rec.Binding)
		}
		if err := os.WriteFile(filepath.Join(tools, name), []byte("version two"), 0o755); err != nil {
			t.Fatal(err)
		}
		text, isErr := callText(t, cs, "apply", prodArgs)
		wantApplyRefused(t, "I", dir, who, text, isErr,
			fmt.Sprintf(`%splan %s no longer matches: the tool "faketool" changed since it was made; run "plan" again`, applyNotRun, rec.ID))
	})

	t.Run("the plan expired", func(t *testing.T) {
		dir := planProject(t, planApplyRunefile)
		who := &asker{answer: yes}
		cs, _ := approvalSession(t, "stdio", dir, nil, nil, who.options())
		rec := makePlan(t, cs, dir, prodArgs)
		rec.Expires = rec.Created.Add(-time.Minute)
		data, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(plansDir(dir), rec.ID, "record.json"), string(data))
		text, isErr := callText(t, cs, "apply", prodArgs)
		wantApplyRefused(t, "K", dir, who, text, isErr,
			fmt.Sprintf(`%splan %s expired at %s; run "plan" again`, applyNotRun, rec.ID, rec.Expires.UTC().Format(time.RFC3339)))
	})

	t.Run("a file at the workspace path is not applied", func(t *testing.T) {
		dir := planProject(t, planApplyRunefile)
		who := &asker{answer: yes}
		cs, _ := approvalSession(t, "stdio", dir, nil, nil, who.options())
		makePlan(t, cs, dir, prodArgs)
		writeFile(t, filepath.Join(dir, "tfplan"), "plan env=prod serial=1 and something nobody reviewed\n")
		if text, isErr := callText(t, cs, "apply", prodArgs); isErr {
			t.Fatalf("M: apply: %s", text)
		}
		if got := readDone(t, dir, "applied.txt"); got != "plan env=prod serial=1" {
			t.Errorf("M: the body read %q, want the stored plan", got)
		}
	})

	t.Run("a plan file that is a link is not stored", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("needs ln")
		}
		runefile := "[plan(file=\"tfplan\", summary=\"tfplan.txt\")]\nplan:\n    @echo x > real; ln -s real tfplan; echo y > tfplan.txt; echo planned >> plan.done\n"
		dir := planProject(t, runefile)
		cs, _ := approvalSession(t, "stdio", dir, nil, nil, nil)
		text, isErr := callText(t, cs, "plan", nil)
		if !isErr || !strings.Contains(text, `plan file "tfplan" of task "plan" is a symbolic link`) {
			t.Errorf("N: isError=%v text=%q", isErr, text)
		}
		if recs := storedPlans(t, dir); len(recs) != 0 {
			t.Errorf("N: a record was stored: %+v", recs)
		}
	})

	t.Run("a stale plan fails in the tool and is final", func(t *testing.T) {
		dir := planProject(t, planApplyRunefile)
		who := &asker{answer: yes}
		cs, _ := approvalSession(t, "stdio", dir, nil, nil, who.options())
		rec := makePlan(t, cs, dir, prodArgs)
		writeFile(t, filepath.Join(dir, "state.serial"), "2\n")

		text, isErr := callText(t, cs, "apply", prodArgs)
		if !isErr || !strings.Contains(text, "Saved plan is stale") || !strings.HasSuffix(text, "[exit 1]") {
			t.Fatalf("O: isError=%v text=%q, want the tool's own refusal", isErr, text)
		}
		var outcome struct {
			State string `json:"state"`
			Exit  int    `json:"exit"`
		}
		data, err := os.ReadFile(filepath.Join(plansDir(dir), rec.ID, "outcome.json"))
		if err != nil || json.Unmarshal(data, &outcome) != nil || outcome.State != "failed" || outcome.Exit != 1 {
			t.Errorf("O: outcome = %s, %v, want failed with exit 1", data, err)
		}

		text, isErr = callText(t, cs, "apply", prodArgs)
		if !isErr || !strings.HasPrefix(text, applyNotRun+"the apply of plan "+rec.ID+" failed at ") ||
			!strings.HasSuffix(text, `; check the target, then run "plan" again and review the new plan`) || len(who.shown()) != 1 {
			t.Errorf("Q: isError=%v text=%q, %d requests", isErr, text, len(who.shown()))
		}

		makePlan(t, cs, dir, prodArgs)
		if text, isErr := callText(t, cs, "apply", prodArgs); isErr {
			t.Fatalf("R: apply of the new plan: %s", text)
		}
		shown := who.shown()
		if len(shown) != 2 || !strings.Contains(shown[1], "\nNote: the previous apply of this target failed at ") ||
			!strings.Contains(shown[1], ". Check the target before approving.\nSummary:\n") {
			t.Errorf("R: second request = %q, want the note about the failed apply", shown)
		}
		if got := readDone(t, dir, "apply.done"); got != "applied" {
			t.Errorf("R: apply.done = %q, want one line", got)
		}
	})

	t.Run("an attempt that did not finish", func(t *testing.T) {
		dir := planProject(t, planApplyRunefile)
		who := &asker{answer: yes}
		cs, _ := approvalSession(t, "stdio", dir, nil, nil, who.options())
		rec := makePlan(t, cs, dir, prodArgs)
		writeFile(t, filepath.Join(plansDir(dir), rec.ID, "claim"), "left by a killed process")
		text, isErr := callText(t, cs, "apply", prodArgs)
		if !isErr || !strings.HasPrefix(text, applyNotRun+"an apply of plan "+rec.ID+" started at ") ||
			!strings.Contains(text, " and did not finish, so its outcome is unknown; ") || len(who.shown()) != 0 {
			t.Errorf("S: isError=%v text=%q, %d requests", isErr, text, len(who.shown()))
		}
		if _, err := os.Stat(filepath.Join(dir, "apply.done")); err == nil {
			t.Error("S: the apply body ran")
		}
	})
}

// Quickstart P (SC-003): of two approved calls for one plan, one runs.
func TestPlanApplyMCP_ConcurrentCalls(t *testing.T) {
	for _, transport := range bothTransports {
		t.Run(transport, func(t *testing.T) {
			dir := planProject(t, planApplyRunefile)
			who := &asker{answer: yes}
			cs, _ := approvalSession(t, transport, dir, nil, nil, who.options())
			rec := makePlan(t, cs, dir, prodArgs)

			texts := make([]string, 2)
			failed := make([]bool, 2)
			var wg sync.WaitGroup
			for i := range texts {
				wg.Go(func() {
					res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "apply", Arguments: prodArgs})
					if err != nil {
						texts[i], failed[i] = err.Error(), true
						return
					}
					texts[i], failed[i] = policyText(res), res.IsError
				})
			}
			wg.Wait()
			ran := 0
			for i, text := range texts {
				switch {
				case !failed[i]:
					ran++
				case strings.Contains(text, "plan "+rec.ID+" was already used by another call"),
					strings.Contains(text, "plan "+rec.ID+" was already applied at "),
					strings.Contains(text, "an apply of plan "+rec.ID+" started at "):
				default:
					t.Errorf("unexpected result: %q", text)
				}
			}
			if ran != 1 || readDone(t, dir, "apply.done") != "applied" {
				t.Errorf("%d calls ran and apply.done is %q, want exactly one", ran, readDone(t, dir, "apply.done"))
			}
		})
	}
}

// Quickstart T (US3): a request that is not approved runs nothing and leaves
// the plan for a later approved call.
func TestPlanApplyMCP_DeclineLeavesThePlan(t *testing.T) {
	for _, transport := range bothTransports {
		t.Run(transport, func(t *testing.T) {
			dir := planProject(t, planApplyRunefile)
			answers := []func(string) (*mcp.ElicitResult, error){no, yes}
			var mu sync.Mutex
			who := &asker{}
			who.answer = func(message string) (*mcp.ElicitResult, error) {
				mu.Lock()
				next := answers[0]
				answers = answers[1:]
				mu.Unlock()
				return next(message)
			}
			cs, _ := approvalSession(t, transport, dir, nil, nil, who.options())
			makePlan(t, cs, dir, prodArgs)

			text, isErr := callText(t, cs, "apply", prodArgs)
			want := `task "apply" was not approved: the request was declined. Nothing ran. To run it without a request, the operator can run "rune apply" themselves.`
			if !isErr || text != want {
				t.Errorf("declined: isError=%v text=%q\nwant %q", isErr, text, want)
			}
			if _, err := os.Stat(filepath.Join(dir, "apply.done")); err == nil {
				t.Fatal("the body ran after a decline")
			}
			if text, isErr := callText(t, cs, "apply", prodArgs); isErr {
				t.Errorf("the later approved call: %s", text)
			}
			if len(who.shown()) != 2 || readDone(t, dir, "apply.done") != "applied" {
				t.Errorf("%d requests, apply.done %q", len(who.shown()), readDone(t, dir, "apply.done"))
			}
		})
	}
}

// Quickstart U, V, W, Y and the tool (US4): the approval cannot be skipped
// with a grant, is not asked for a value the policy refuses, and a client
// that cannot be asked gets the command to run.
func TestPlanApplyMCP_ApprovalCannotBeSkipped(t *testing.T) {
	for _, transport := range bothTransports {
		t.Run(transport, func(t *testing.T) {
			t.Run("a grant approves no plan", func(t *testing.T) {
				dir := planProject(t, planApplyRunefile)
				who := &asker{answer: yes}
				cs, stderr := approvalSession(t, transport, dir, []string{"--allow-destructive", "apply"}, nil, who.options())
				makePlan(t, cs, dir, prodArgs)
				if text, isErr := callText(t, cs, "apply", prodArgs); isErr {
					t.Fatalf("U: apply: %s", text)
				}
				if len(who.shown()) != 1 {
					t.Errorf("U: %d requests, want the person asked although the task is granted", len(who.shown()))
				}
				warning := `warning: --allow-destructive apply approves no plan: task "apply" asks the person at the client on every call`
				if got := stderr.String(); !strings.Contains(got, warning) || strings.Contains(got, "is not destructive") {
					t.Errorf("U: startup output = %q, want %q", got, warning)
				}
			})

			t.Run("a client that cannot be asked", func(t *testing.T) {
				dir := planProject(t, planApplyRunefile)
				cs, _ := approvalSession(t, transport, dir, nil, nil, nil)
				text, isErr := callText(t, cs, "apply", prodArgs)
				wantApplyRefused(t, "V without a plan", dir, nil, text, isErr, applyNotRun+`there is no plan for env="prod"; run "plan" with the same arguments first`)

				makePlan(t, cs, dir, prodArgs)
				text, isErr = callText(t, cs, "apply", prodArgs)
				want := `task "apply" applies a plan and runs only when the person at the client approves that plan.` +
					` The operator can run "rune apply" themselves.` +
					` Rune could not ask for approval here: the client does not support MCP form elicitation.`
				wantApplyRefused(t, "V", dir, nil, text, isErr, want)
			})

			t.Run("the value policy comes first", func(t *testing.T) {
				runefile := strings.Replace(planApplyRunefile, `[applies("plan")]`, "[applies(\"plan\")]\n[agent-allow(env=\"staging\")]", 1)
				dir := planProject(t, runefile)
				who := &asker{answer: yes}
				cs, _ := approvalSession(t, transport, dir, nil, nil, who.options())
				makePlan(t, cs, dir, prodArgs)
				text, isErr := callText(t, cs, "apply", prodArgs)
				wantApplyRefused(t, "W", dir, who, text, isErr,
					`task "apply" is not allowed for agents with env="prod"; agent policy requires env="staging"`)
			})

			t.Run("one request covers the plan and the confirmation", func(t *testing.T) {
				runefile := strings.Replace(planApplyRunefile, `[applies("plan")]`, `[confirm("Really apply?"), applies("plan")]`, 1)
				dir := planProject(t, runefile)
				who := &asker{answer: yes}
				cs, _ := approvalSession(t, transport, dir, nil, nil, who.options())
				rec := makePlan(t, cs, dir, prodArgs)
				if text, isErr := callText(t, cs, "apply", prodArgs); isErr {
					t.Fatalf("Y: apply: %s", text)
				}
				shown := who.shown()
				if len(shown) != 1 || !strings.Contains(shown[0], "Plan: "+rec.ID+" made by") || !strings.Contains(shown[0], "- apply: Really apply?\n") {
					t.Errorf("Y: requests = %q, want one with the plan and the message", shown)
				}
			})

			t.Run("the tool an agent sees", func(t *testing.T) {
				dir := planProject(t, planApplyRunefile)
				cs, _ := approvalSession(t, transport, dir, nil, nil, nil)
				res, err := cs.ListTools(context.Background(), nil)
				if err != nil {
					t.Fatal(err)
				}
				for _, tool := range res.Tools {
					schema, _ := json.Marshal(tool.InputSchema)
					switch tool.Name {
					case "apply":
						if tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint {
							t.Error("apply is not advertised as destructive")
						}
						want := `Plan approval: applies the plan made by task "plan". Every call asks the person at the client to approve that plan.`
						if !strings.HasSuffix(tool.Description, want) {
							t.Errorf("apply description = %q", tool.Description)
						}
						if want := `{"additionalProperties":false,"properties":{"env":{"enum":["staging","prod"],"type":"string"}},"required":["env"],"type":"object"}`; string(schema) != want {
							t.Errorf("apply schema = %s, want only the task's parameters", schema)
						}
					case "plan":
						if tool.Annotations.DestructiveHint != nil && *tool.Annotations.DestructiveHint {
							t.Error("plan is advertised as destructive")
						}
						if strings.Contains(tool.Description, "Plan approval") {
							t.Errorf("plan description = %q", tool.Description)
						}
					}
				}
			})
		})
	}
}

// Quickstart X (US4): a body that runs rune inside an agent run cannot apply
// a plan, with or without --yes.
func TestPlanApplyMCP_BodyRunsRune(t *testing.T) {
	runefile := planApplyRunefile + "\n# Apply from a body.\nscript:\n    @echo scripted > script.done\n    @\"${RUNE_BIN:-rune}\" --yes apply prod\n"
	dir := planProject(t, runefile)
	cs, _ := approvalSession(t, "stdio", dir, nil, []string{"RUNE_BIN=" + runeBin}, nil)
	makePlan(t, cs, dir, prodArgs)
	text, isErr := callText(t, cs, "script", nil)
	want := `task "apply" applies a plan and cannot run inside an agent run: a person has to approve the plan in the call that applies it`
	if !isErr || !strings.Contains(text, want) {
		t.Errorf("isError=%v text=%q, want %q", isErr, text, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "apply.done")); err == nil {
		t.Error("the apply body ran")
	}
	if recs := storedPlans(t, dir); len(recs) != 1 {
		t.Errorf("records = %d", len(recs))
	}
}

// Quickstart AE (US3 scenario 10): a plan replaced while the person is asked
// is refused at the claim, and that is not a failed apply.
func TestPlanApplyMCP_ReplacedWhilePending(t *testing.T) {
	dir := planProject(t, planApplyRunefile)
	who := &asker{}
	replaced := false
	who.answer = func(message string) (*mcp.ElicitResult, error) {
		if !replaced {
			replaced = true
			// A person plans again from the command line while the request
			// is on the screen.
			cmd := exec.Command(runeBin, "plan", "prod")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), noAgentMark)
			if out, err := cmd.CombinedOutput(); err != nil {
				return nil, fmt.Errorf("rune plan prod: %w: %s", err, out)
			}
		}
		return yes(message)
	}
	cs, _ := approvalSession(t, "stdio", dir, nil, nil, who.options())
	first := makePlan(t, cs, dir, prodArgs)

	text, isErr := callText(t, cs, "apply", prodArgs)
	want := fmt.Sprintf(`%splan %s was replaced by a newer plan; call "apply" again`, applyNotRun, first.ID)
	if !isErr || text != want {
		t.Fatalf("isError=%v text=%q\nwant %q", isErr, text, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "apply.done")); err == nil {
		t.Fatal("the apply body ran")
	}
	if _, err := os.Stat(filepath.Join(plansDir(dir), first.ID, "outcome.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused claim is recorded as an outcome: %v", err)
	}

	if text, isErr := callText(t, cs, "apply", prodArgs); isErr {
		t.Fatalf("the next apply: %s", text)
	}
	shown := who.shown()
	second := storedPlans(t, dir)[1]
	if len(shown) != 2 || !strings.Contains(shown[1], "Plan: "+second.ID+" made by") || strings.Contains(shown[1], "\nNote: ") {
		t.Errorf("requests = %q, want the second about plan %s with no note", shown, second.ID)
	}
}

// Quickstart J (US2 scenario 7): the configuration belongs to a load. The
// command line reads the changed dotenv file and refuses. The server that
// made the plan still runs with what it loaded.
func TestPlanApplyMCP_DotenvChanged(t *testing.T) {
	runefile := "set dotenv := \".env\"\n\n" + planApplyRunefile
	dir := planProject(t, runefile)
	writeFile(t, filepath.Join(dir, ".env"), "REGION=eu\n")
	who := &asker{answer: yes}
	cs, _ := approvalSession(t, "stdio", dir, nil, nil, who.options())
	rec := makePlan(t, cs, dir, prodArgs)
	writeFile(t, filepath.Join(dir, ".env"), "REGION=us\n")

	r := run(t, dir, []string{noAgentMark}, "--yes", "apply", "prod")
	want := fmt.Sprintf(`plan %s no longer matches: the configuration changed since it was made; run "plan" again`, rec.ID)
	if r.code == 0 || !strings.Contains(r.stderr, want) {
		t.Errorf("command line: exit %d, stderr %q, want %q", r.code, r.stderr, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "apply.done")); err == nil {
		t.Fatal("the apply body ran")
	}
	if text, isErr := callText(t, cs, "apply", prodArgs); isErr || len(who.shown()) != 1 {
		t.Errorf("the server that made the plan: isError=%v text=%q, %d requests", isErr, text, len(who.shown()))
	}
}

// Quickstart AC (US9, SC-008): a Runefile with neither attribute never makes
// a store.
func TestPlanApplyMCP_NoStoreWithoutAttributes(t *testing.T) {
	dir := writeRunefile(t, "# Build it.\nbuild:\n    @echo built > build.done\n")
	cs, _ := approvalSession(t, "stdio", dir, nil, nil, nil)
	if text, isErr := callText(t, cs, "build", nil); isErr {
		t.Fatalf("build: %s", text)
	}
	if r := run(t, dir, []string{noAgentMark}, "build"); r.code != 0 {
		t.Fatalf("rune build: exit %d, stderr %q", r.code, r.stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, ".rune")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a Runefile with no plan made a store: %v", err)
	}
}

// Quickstart AB: version control is not offered anything from the store.
func TestPlanApplyMCP_StoreIsIgnoredByGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := planProject(t, planApplyRunefile)
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return string(out)
	}
	git("init", "-q")
	cs, _ := approvalSession(t, "stdio", dir, nil, nil, nil)
	makePlan(t, cs, dir, prodArgs)
	if status := git("status", "--porcelain", "--untracked-files=all"); strings.Contains(status, ".rune/plans/") {
		t.Errorf("git offers the store for commit:\n%s", status)
	}
}
