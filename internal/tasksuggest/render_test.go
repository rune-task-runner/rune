package tasksuggest

import (
	"strings"
	"testing"
	"time"

	"github.com/rune-task-runner/rune/internal/observe"
)

func TestTaskText(t *testing.T) {
	s := &Suggestion{Name: "go-test", Description: "Run the parser tests", Body: "go test -run TestX ./pkg/..."}
	if got, want := s.Task("\n"), "# Run the parser tests\ngo-test:\n    go test -run TestX ./pkg/...\n"; got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	s.Dir = "web"
	s.Param = &Param{Name: "run", Default: "TestX"}
	s.Body = "go test -run {{quote(run)}} ./pkg/..."
	want := "# Run the parser tests\r\n[working-directory(\"web\")]\r\ngo-test run=\"TestX\":\r\n    go test -run {{quote(run)}} ./pkg/...\r\n"
	if got := s.Task("\r\n"); got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

// With set working-directory, a task names its directory unless it is that
// one, and the root is ".".
func TestTaskTextDefaultDir(t *testing.T) {
	s := &Suggestion{Name: "x", Description: "X.", Body: "make", defaultDir: "sub"}
	for dir, want := range map[string]string{
		"":    "# X.\n[working-directory(\".\")]\nx:\n    make\n",
		"sub": "# X.\nx:\n    make\n",
		"web": "# X.\n[working-directory(\"web\")]\nx:\n    make\n",
	} {
		s.Dir = dir
		if got := s.Task("\n"); got != want {
			t.Errorf("dir %q: got %q, want %q", dir, got, want)
		}
	}
	rep := Build(records(times(3, call{cmd: "make"})...), nil, Options{DefaultDir: "sub"})
	if len(rep.Suggestions) != 1 || !strings.Contains(rep.Suggestions[0].Task("\n"), `[working-directory(".")]`) {
		t.Errorf("Build does not pass the directory on: %+v", rep.Suggestions)
	}
}

// A family's name stops before the word that differs: that word is the
// parameter, and naming the task after its default would mislead.
func TestFamilyNameLeavesOutTheParameter(t *testing.T) {
	rep := build(join(times(2, call{cmd: "make a"}), times(2, call{cmd: "make b"})))
	if len(rep.Suggestions) != 1 || rep.Suggestions[0].Name != "make" || rep.Suggestions[0].Param == nil {
		t.Errorf("suggestions %+v", rep.Suggestions)
	}
}

func TestBodyEscapesBraces(t *testing.T) {
	rep := build(times(3, call{cmd: "echo {{x}} }}"}))
	// Rune reads {{{{ as two opening braces; }} alone is plain text.
	if got := rep.Suggestions[0].Body; got != "echo {{{{x}} }}" {
		t.Errorf("body %q", got)
	}
}

func TestAppend(t *testing.T) {
	a := &Suggestion{Name: "a", Description: "A", Body: "make a"}
	b := &Suggestion{Name: "b", Description: "B", Body: "make b"}
	tests := []struct {
		name, old, want string
	}{
		{"empty", "", "# A\na:\n    make a\n\n# B\nb:\n    make b\n"},
		{"white space only", "\n\n", "\n\n# A\na:\n    make a\n\n# B\nb:\n    make b\n"},
		{"final line feed", "x:\n    y\n", "x:\n    y\n\n# A\na:\n    make a\n\n# B\nb:\n    make b\n"},
		{"no final line feed", "x:\n    y", "x:\n    y\n\n# A\na:\n    make a\n\n# B\nb:\n    make b\n"},
		{"windows", "x:\r\n    y", "x:\r\n    y\r\n\r\n# A\r\na:\r\n    make a\r\n\r\n# B\r\nb:\r\n    make b\r\n"},
	}
	for _, tt := range tests {
		got := string(Append([]byte(tt.old), []*Suggestion{a, b}))
		if got != tt.want {
			t.Errorf("%s:\n got %q\nwant %q", tt.name, got, tt.want)
		}
		if !strings.HasPrefix(got, tt.old) {
			t.Errorf("%s: the old bytes are not kept", tt.name)
		}
	}
}

func TestListing(t *testing.T) {
	calls := join(
		times(12, call{cmd: "go test -run TestX ./pkg/...", desc: "Run the parser tests", session: "a", at: t0.Add(72 * time.Hour)}),
		times(2, call{cmd: "go test -run TestX ./pkg/...", session: "b", outcome: Failed, exit: 1, at: t0.Add(72 * time.Hour)}),
		times(5, call{cmd: "go test -run TestA ./...", cwd: "web", at: t0}),
		times(3, call{cmd: "go test -run TestB ./...", cwd: "web", at: t0}),
		times(1, call{cmd: "go test -run TestC ./...", cwd: "web", at: t0}),
		times(5, call{cmd: "go test $(go list ./...)", at: t0}),
		times(10, call{cmd: "go vet ./...", at: t0}),
		[]call{{cmd: "make", outcome: Attempt}},
	)
	rep := build(calls, TaskInfo{Name: "vet", Body: "go vet ./..."})
	rep.Dropped = map[string]int64{observe.ReasonHereDocument: 2, observe.ReasonTooLong: 1, "zero": 0}
	rep.Damaged = 2
	ids := map[string]string{}
	for _, s := range rep.Suggestions {
		ids[s.Body] = s.ID
	}
	want := "Recorded 38 commands in 3 sessions, 2026-11-20 to 2026-11-23.\n" +
		"1 attempt has no recorded outcome.\n" +
		"\n" +
		"[" + ids["go test -run TestX ./pkg/..."] + "] the agent ran this 14 times (12 succeeded, 2 failed) in 2 sessions, last on 2026-11-23:\n" +
		"    go test -run TestX ./pkg/...\n" +
		"  suggested task:\n" +
		"    # Run the parser tests\n" +
		"    go-test:\n" +
		"        go test -run TestX ./pkg/...\n" +
		"\n" +
		"[" + ids["go test -run {{quote(run)}} ./..."] + "] the agent ran this 9 times with 3 values (9 succeeded) in 1 session, last on 2026-11-20, in web:\n" +
		"    go test -run TestA ./...   (5 runs)\n" +
		"    go test -run TestB ./...   (3 runs)\n" +
		"    go test -run TestC ./...   (1 run)\n" +
		"  suggested task:\n" +
		"    # Run go test.\n" +
		"    [working-directory(\"web\")]\n" +
		"    go-test-2 run=\"TestA\":\n" +
		"        go test -run {{quote(run)}} ./...\n" +
		"\n" +
		"For review, not turned into a task:\n" +
		"  ran 5 times: go test $(go list ./...)\n" +
		"    reason: it uses a command substitution\n" +
		"\n" +
		"Already tasks: vet (10 runs).\n" +
		"Not recorded: 3 events (here-document 2, too-long 1).\n" +
		"Skipped 2 damaged lines in the store.\n" +
		"\n" +
		"Apply with: rune suggest apply ID...\n"
	if got := rep.Listing("/p", 3); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestListingShapes(t *testing.T) {
	t.Run("nothing recorded", func(t *testing.T) {
		rep := build(nil)
		want := "Nothing recorded for /p.\nTurn recording on with: rune agent observe claude\n"
		if got := rep.Listing("/p", 3); got != want {
			t.Errorf("got %q", got)
		}
		rep.Dropped = map[string]int64{observe.ReasonNoProject: 1}
		if got := rep.Listing("/p", 3); !strings.HasSuffix(got, "\nNot recorded: 1 event (no-project 1).\n") {
			t.Errorf("with drops: %q", got)
		}
	})
	t.Run("nothing listed", func(t *testing.T) {
		rep := build([]call{{cmd: "make", outcome: Succeeded}})
		want := "Recorded 1 command in 1 session, on 2026-11-20.\n\nNo command ran 3 times or more. Lower the bar with --min-runs.\n"
		if got := rep.Listing("/p", 3); got != want {
			t.Errorf("got %q", got)
		}
	})
	t.Run("everything is already a task", func(t *testing.T) {
		rep := build(times(3, call{cmd: "go vet ./..."}), TaskInfo{Name: "vet", Body: "go vet ./..."})
		want := "Recorded 3 commands in 1 session, on 2026-11-20.\n\nAlready tasks: vet (3 runs).\n"
		if got := rep.Listing("/p", 3); got != want {
			t.Errorf("got %q", got)
		}
	})
	t.Run("attempts", func(t *testing.T) {
		rep := build([]call{{cmd: "a"}, {cmd: "b"}})
		if got := rep.Listing("/p", 3); !strings.Contains(got, "\n2 attempts have no recorded outcome.\n") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("a review in a directory and every outcome", func(t *testing.T) {
		rep := build(join(
			times(3, call{cmd: "for x in 1; do y; done", cwd: "web"}),
			[]call{{cmd: "make", outcome: Interrupted}, {cmd: "make", outcome: Started}, {cmd: "make", outcome: Succeeded}},
		))
		got := rep.Listing("/p", 3)
		for _, want := range []string{"  ran 3 times: for x in 1; do y; done (in web)\n", "(1 succeeded, 1 interrupted, 1 started in the background)"} {
			if !strings.Contains(got, want) {
				t.Errorf("no %q in\n%s", want, got)
			}
		}
		if strings.Contains(got, "Apply with") != (len(rep.Suggestions) > 0) {
			t.Errorf("apply line:\n%s", got)
		}
	})
	t.Run("many values", func(t *testing.T) {
		var calls []call
		for _, v := range []string{"a", "b", "c", "d", "e", "f", "g"} {
			calls = append(calls, call{cmd: "make " + v, outcome: Succeeded})
		}
		got := build(calls).Listing("/p", 3)
		if !strings.Contains(got, "    and 2 more\n") || strings.Count(got, "(1 run)") != 5 {
			t.Errorf("got\n%s", got)
		}
	})
}

func TestCount(t *testing.T) {
	if count(1, "run", "runs") != "1 run" || count(0, "run", "runs") != "0 runs" || count(2, "run", "runs") != "2 runs" {
		t.Error("count")
	}
}
