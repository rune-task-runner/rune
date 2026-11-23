package taskimport

import (
	"slices"
	"strings"
	"testing"
)

func mustFinish(t *testing.T, src *source, opts Options) {
	t.Helper()
	if err := finish(src, opts); err != nil {
		t.Fatalf("finish: %v", err)
	}
}

func taskNamed(src *source, name string) *Task {
	for _, t := range src.tasks {
		if t.Source == name {
			return t
		}
	}
	return nil
}

func TestFinishMapsNamesAndCalls(t *testing.T) {
	src := &source{tasks: []*Task{
		{Source: "ci", Line: 1, Deps: []Call{{Name: "test:unit"}}, Post: []Call{{Name: "docs:build", Args: `"x"`}}},
		{Source: "test:unit", Line: 3},
		{Source: "docs:build", Line: 5},
	}}
	mustFinish(t, src, Options{Format: FormatTask, SourceName: "Taskfile.yml"})
	ci := taskNamed(src, "ci")
	if ci.Deps[0].Name != "test-unit" || ci.Post[0].Name != "docs-build" || ci.Post[0].Args != `"x"` {
		t.Errorf("calls = %+v && %+v, want the mapped names", ci.Deps, ci.Post)
	}
	if got := taskNamed(src, "test:unit").Name; got != "test-unit" {
		t.Errorf("Name = %q, want test-unit", got)
	}
}

func TestFinishUndefinedCallBecomesStub(t *testing.T) {
	src := &source{tasks: []*Task{
		{Source: "a", Line: 2, Deps: []Call{{Name: "lib:gen"}}, Post: []Call{{Name: "lib:gen"}}},
		{Source: "b", Line: 6, Deps: []Call{{Name: "lib:gen"}}},
	}}
	mustFinish(t, src, Options{Format: FormatTask, SourceName: "Taskfile.yml", Wrap: true})
	if len(src.tasks) != 3 {
		t.Fatalf("tasks = %d, want 3: one stub for the undefined task", len(src.tasks))
	}
	stub := src.tasks[2]
	want := Task{
		Source: "lib:gen", Name: "lib-gen", Line: 2,
		Doc:    "Not imported (Taskfile.yml:2): not defined in Taskfile.yml",
		Params: []string{"*args"},
		Body:   []string{`@echo 'rune import: "lib:gen" (Taskfile.yml:2) was not imported: not defined in Taskfile.yml' >&2; exit 1`},
		Status: Unsupported, Notes: []string{"not defined in Taskfile.yml"}, NoWrap: true,
	}
	if stub.Source != want.Source || stub.Name != want.Name || stub.Line != want.Line || stub.Doc != want.Doc ||
		!slices.Equal(stub.Params, want.Params) || !slices.Equal(stub.Body, want.Body) ||
		stub.Status != want.Status || !slices.Equal(stub.Notes, want.Notes) || !stub.NoWrap {
		t.Errorf("stub = %+v\nwant   %+v", *stub, want)
	}
}

func TestFinishUnsupportedBecomesStub(t *testing.T) {
	src := &source{tasks: []*Task{{
		Source: "gen", Line: 20, Doc: "Generate.", Status: Unsupported,
		Attrs:  []string{`group("x")`, "private"},
		Params: []string{"p"}, Deps: []Call{{Name: "nope"}}, Body: []string{"echo x"},
		Notes: []string{"it's a {{backtick}}", "second"},
	}}}
	mustFinish(t, src, Options{Format: FormatJust, SourceName: "justfile"})
	if len(src.tasks) != 1 {
		t.Fatalf("tasks = %d, want 1: the calls of an unsupported task create no stub", len(src.tasks))
	}
	got := src.tasks[0]
	if got.Doc != "Not imported (justfile:20): it's a {{backtick}}; second" {
		t.Errorf("Doc = %q", got.Doc)
	}
	if !slices.Equal(got.Attrs, []string{"private"}) || !slices.Equal(got.Params, []string{"*args"}) || got.Deps != nil || got.Post != nil {
		t.Errorf("stub keeps only private and *args: %+v", *got)
	}
	wantBody := `@echo 'rune import: "gen" (justfile:20) was not imported: it'\''s a {{{{backtick}}; second' >&2; exit 1`
	if !slices.Equal(got.Body, []string{wantBody}) {
		t.Errorf("Body = %q\nwant   %q", got.Body, wantBody)
	}
	if got.Status != Unsupported {
		t.Errorf("Status = %v, want unsupported", got.Status)
	}
}

func TestFinishDowngrade(t *testing.T) {
	src := &source{
		tasks: []*Task{
			{Source: "a", Line: 5},
			{Source: "b", Line: 7, Status: Review, Notes: []string{"n"}},
			{Source: "c", Line: 9, Status: Unsupported, Notes: []string{"u"}},
		},
		items: []Item{
			{Line: 1, Name: "set shell", Note: "x", Global: true},
			{Line: 2, Name: "alias", Note: "y"},
			{Line: 3, Name: "set export", Note: "z", Global: true},
		},
	}
	mustFinish(t, src, Options{Format: FormatJust, SourceName: "justfile"})
	a, b, c := src.tasks[0], src.tasks[1], src.tasks[2]
	if a.Status != Review || !slices.Equal(a.Notes, []string{"see justfile:1", "see justfile:3"}) {
		t.Errorf("a = %v %q, want review with both references", a.Status, a.Notes)
	}
	if b.Status != Review || !slices.Equal(b.Notes, []string{"n", "see justfile:1", "see justfile:3"}) {
		t.Errorf("b = %v %q, want its note and both references", b.Status, b.Notes)
	}
	if c.Status != Unsupported || !slices.Equal(c.Notes, []string{"u"}) {
		t.Errorf("c = %v %q, want it unchanged", c.Status, c.Notes)
	}
}

func TestFinishWrappers(t *testing.T) {
	tests := []struct {
		format Format
		runner string
		body   string
	}{
		{FormatJust, "just", `just --justfile ../justfile test:unit {{quote_each(args)}}`},
		{FormatMake, "make", `make -f ../justfile test:unit {{quote_each(args)}}`},
		{FormatTask, "task", `task --taskfile ../justfile test:unit -- {{quote_each(args)}}`},
		{FormatNPM, "npm", `npm run test:unit -- {{quote_each(args)}}`},
	}
	for _, tt := range tests {
		t.Run(string(tt.format), func(t *testing.T) {
			src := &source{tasks: []*Task{
				{Source: "test:unit", Line: 3, Doc: "Unit tests.", Status: Unsupported, Attrs: []string{"private"}, Notes: []string{"why"}},
				{Source: "internal", Line: 8, Status: Unsupported, Notes: []string{"why"}, NoWrap: true},
				{Source: "ok", Line: 9},
			}}
			mustFinish(t, src, Options{Format: tt.format, SourceName: "justfile", RunnerPath: "../justfile", Wrap: true})
			w := src.tasks[0]
			if w.Status != Wrapper {
				t.Fatalf("Status = %v, want wrapper", w.Status)
			}
			command := tt.runner + " test:unit"
			if tt.format == FormatNPM {
				command = "npm run test:unit"
			}
			if want := `Runs "` + command + `" (wrapper, needs ` + tt.runner + `). Unit tests.`; w.Doc != want {
				t.Errorf("Doc = %q, want %q", w.Doc, want)
			}
			if !slices.Equal(w.Body, []string{tt.body}) || !slices.Equal(w.Params, []string{"*args"}) || !slices.Equal(w.Attrs, []string{"private"}) {
				t.Errorf("wrapper = %+v, want body %q", *w, tt.body)
			}
			if !slices.Equal(w.Notes, []string{"why"}) {
				t.Errorf("Notes = %q, want the reason kept", w.Notes)
			}
			if src.tasks[1].Status != Unsupported {
				t.Errorf("a task the runner cannot run stays a failing task, got %v", src.tasks[1].Status)
			}
			if src.tasks[2].Status != Converted || src.tasks[2].Body != nil {
				t.Errorf("a converted task is untouched: %+v", *src.tasks[2])
			}
		})
	}
}

func TestFinishWrapperQuotesNames(t *testing.T) {
	src := &source{tasks: []*Task{{Source: "odd name", Line: 1, Status: Unsupported, Notes: []string{"why"}}}}
	mustFinish(t, src, Options{Format: FormatMake, SourceName: "My Makefile", RunnerPath: "My Makefile", Wrap: true})
	if want := `make -f 'My Makefile' 'odd name' {{quote_each(args)}}`; src.tasks[0].Body[0] != want {
		t.Errorf("Body = %q, want %q", src.tasks[0].Body[0], want)
	}
}

func TestFindCycle(t *testing.T) {
	tests := []struct {
		name  string
		tasks []*Task
		line  int
		msg   string
	}{
		{
			name: "no cycle, shared dependency",
			tasks: []*Task{
				{Source: "a", Deps: []Call{{Name: "b"}, {Name: "c"}}},
				{Source: "b", Deps: []Call{{Name: "c"}}},
				{Source: "c", Post: []Call{{Name: "gone"}}},
			},
		},
		{
			name: "two tasks",
			tasks: []*Task{
				{Source: "a", Line: 1, Deps: []Call{{Name: "b"}}},
				{Source: "b", Line: 4, Deps: []Call{{Name: "a"}}},
			},
			line: 1, msg: "dependency cycle: a -> b -> a",
		},
		{
			name:  "a task that calls itself after its body",
			tasks: []*Task{{Source: "a", Line: 3, Post: []Call{{Name: "a"}}}},
			line:  3, msg: "dependency cycle: a -> a",
		},
		{
			name: "a cycle below the first task",
			tasks: []*Task{
				{Source: "top", Line: 1, Deps: []Call{{Name: "x"}}},
				{Source: "x", Line: 2, Deps: []Call{{Name: "y"}}},
				{Source: "y", Line: 3, Deps: []Call{{Name: "x"}}},
			},
			line: 2, msg: "dependency cycle: x -> y -> x",
		},
		{
			name: "an unsupported task ends the path",
			tasks: []*Task{
				{Source: "a", Deps: []Call{{Name: "b"}}},
				{Source: "b", Status: Unsupported, Deps: []Call{{Name: "a"}}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := findCycle(tt.tasks)
			if tt.msg == "" {
				if err != nil {
					t.Fatalf("findCycle: %v, want none", err)
				}
				return
			}
			pe, ok := err.(*ParseError)
			if !ok || pe.Line != tt.line || pe.Msg != tt.msg {
				t.Errorf("findCycle = %v, want line %d: %s", err, tt.line, tt.msg)
			}
		})
	}
}

func TestFinishReportsCycle(t *testing.T) {
	src := &source{tasks: []*Task{{Source: "a", Line: 2, Deps: []Call{{Name: "a"}}}}}
	if err := finish(src, Options{Format: FormatMake, SourceName: "Makefile"}); err == nil {
		t.Error("finish accepted a task that depends on itself")
	}
}

func TestFinishRuneCommandNames(t *testing.T) {
	src := &source{tasks: []*Task{
		{Source: "help", Line: 1},
		{Source: "serve", Line: 2, Status: Review, Notes: []string{"n"}},
		{Source: "log", Line: 3, Status: Unsupported, Notes: []string{"u"}},
		{Source: "build", Line: 4},
		{Source: "agent", Line: 5},
	}}
	mustFinish(t, src, Options{Format: FormatMake, SourceName: "Makefile", Wrap: true})
	help, serve, log, build, agent := src.tasks[0], src.tasks[1], src.tasks[2], src.tasks[3], src.tasks[4]
	if agent.Status != Review || !slices.Equal(agent.Notes, []string{"agent is also a rune command: run the task with rune -- agent"}) {
		t.Errorf("agent = %v %q", agent.Status, agent.Notes)
	}
	if help.Name != "help" || help.Status != Review || !slices.Equal(help.Notes, []string{"help is also a rune command: run the task with rune -- help"}) {
		t.Errorf("help = %q %v %q", help.Name, help.Status, help.Notes)
	}
	if serve.Status != Review || !slices.Equal(serve.Notes, []string{"n", "serve is also a rune command: run the task with rune -- serve"}) {
		t.Errorf("serve = %v %q", serve.Status, serve.Notes)
	}
	if log.Status != Wrapper || !slices.Equal(log.Notes, []string{"u", "log is also a rune command: run the task with rune -- log"}) {
		t.Errorf("log = %v %q, want a wrapper with both notes", log.Status, log.Notes)
	}
	if build.Status != Converted || build.Notes != nil {
		t.Errorf("build = %v %q, want it untouched", build.Status, build.Notes)
	}
}

// rune suggest is a command (spec 058), so a task named suggest is run with
// rune -- suggest, and the importer says so.
func TestFinishSuggestIsARuneCommand(t *testing.T) {
	src := &source{tasks: []*Task{{Source: "suggest", Line: 1}}}
	mustFinish(t, src, Options{Format: FormatMake, SourceName: "Makefile"})
	if got := src.tasks[0]; got.Status != Review || !slices.Equal(got.Notes, []string{"suggest is also a rune command: run the task with rune -- suggest"}) {
		t.Errorf("suggest = %v %q", got.Status, got.Notes)
	}
}

func TestFinishConfirmWithDependencies(t *testing.T) {
	const note = "the source runner asks before the dependencies run; Rune asks after them"
	src := &source{tasks: []*Task{
		{Source: "a", Line: 1, Attrs: []string{`confirm("Sure?")`}, Deps: []Call{{Name: "d"}}},
		{Source: "b", Line: 2, Attrs: []string{"confirm"}, Deps: []Call{{Name: "d"}}},
		{Source: "c", Line: 3, Attrs: []string{"confirm"}, Post: []Call{{Name: "d"}}},
		{Source: "d", Line: 4, Attrs: []string{"private"}, Deps: nil},
		{Source: "e", Line: 5, Attrs: []string{"confirm"}, Deps: []Call{{Name: "d"}}, Status: Unsupported, Notes: []string{"u"}},
	}}
	mustFinish(t, src, Options{Format: FormatJust, SourceName: "justfile"})
	for i, want := range []Status{Review, Review, Converted, Converted, Unsupported} {
		if got := src.tasks[i].Status; got != want {
			t.Errorf("%s = %v, want %v", src.tasks[i].Source, got, want)
		}
	}
	if !slices.Equal(src.tasks[0].Notes, []string{note}) {
		t.Errorf("Notes = %q", src.tasks[0].Notes)
	}
}

// A name with a line break cannot be written on the body line of a wrapper:
// the rest of the name would be lines of the Runefile.
func TestFinishWrapperRefusesControlCharacters(t *testing.T) {
	src := &source{tasks: []*Task{{Source: "a\nevil:\n    echo ", Line: 1, Status: Unsupported, Notes: []string{"why"}}}}
	mustFinish(t, src, Options{Format: FormatNPM, SourceName: "package.json", RunnerPath: "package.json", Wrap: true})
	task := src.tasks[0]
	if task.Status != Unsupported {
		t.Errorf("Status = %v, want unsupported", task.Status)
	}
	if want := []string{"why", "its name has a control character, so no wrapper can name it"}; !slices.Equal(task.Notes, want) {
		t.Errorf("Notes = %q, want %q", task.Notes, want)
	}
	text := emitRunefile(src, Options{SourceName: "package.json"})
	if strings.Contains(text, "\nevil:") {
		t.Errorf("the name starts a line of the Runefile:\n%s", text)
	}
	checkRunefile(t, text)
}
