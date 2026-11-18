package taskimport

import "testing"

func TestQuote(t *testing.T) {
	for in, want := range map[string]string{
		"plain":       `"plain"`,
		`a "b" \c`:    `"a \"b\" \\c"`,
		"l1\nl2\tx\r": `"l1\nl2\tx\r"`,
		"héllo ✓":     `"héllo ✓"`,
		"":            `""`,
	} {
		got, ok := quote(in)
		if !ok || got != want {
			t.Errorf("quote(%q) = %s, %v, want %s", in, got, ok, want)
		}
	}
	for _, in := range []string{"bell\a", "nul\x00", "esc\x1b[0m", "caf\xe9"} {
		if got, ok := quote(in); ok {
			t.Errorf("quote(%q) = %s, want it rejected", in, got)
		}
	}
}

func TestEmitRunefile(t *testing.T) {
	src := &source{
		vars: []Var{{Name: "out", Expr: `"dist"`}, {Name: "bin", Expr: `out + "/app"`}},
		tasks: []*Task{
			{
				Name: "build", Doc: "Build the app.",
				Attrs:  []string{`group("dev")`, "private"},
				Params: []string{`target="debug"`, "*args"},
				Deps:   []Call{{Name: "gen"}, {Name: "fetch", Args: `"all" target`}},
				Post:   []Call{{Name: "notify"}},
				Body:   []string{"@echo building", "-go build ./..."},
			},
			{Name: "all", Deps: []Call{{Name: "build"}}},
			{Name: "gen", Body: []string{"go generate ./..."}},
		},
	}
	want := `# Imported from justfile by rune import.

out := "dist"
bin := out + "/app"

# Build the app.
[group("dev")]
[private]
build target="debug" *args: gen (fetch "all" target) && notify
    @echo building
    -go build ./...

all: build

gen:
    go generate ./...
`
	if got := emitRunefile(src, Options{SourceName: "justfile"}); got != want {
		t.Errorf("emitRunefile:\n%s\nwant:\n%s", got, want)
	}
}

func TestEmitRunefileWithoutVars(t *testing.T) {
	src := &source{tasks: []*Task{{Name: "a", Body: []string{"echo a"}}}}
	want := "# Imported from sub/Makefile by rune import.\n\na:\n    echo a\n"
	if got := emitRunefile(src, Options{SourceName: "sub/Makefile"}); got != want {
		t.Errorf("emitRunefile = %q, want %q", got, want)
	}
}

func TestEmitReport(t *testing.T) {
	src := &source{
		tasks: []*Task{
			{Source: "build", Name: "build", Line: 4},
			{Source: "test:unit", Name: "test-unit", Line: 9, Status: Review, Notes: []string{"first", "second"}},
			{Source: "gen", Name: "gen", Line: 20, Status: Unsupported, Notes: []string{"uses a backtick command"}},
			{Source: "old", Name: "old", Line: 30, Status: Wrapper, Notes: []string{"a shebang recipe"}},
			{Source: "same", Name: "same", Line: 2},
		},
		items: []Item{
			{Line: 2, Name: "set shell", Note: "not converted"},
			{Line: 1, Name: "import", Note: "not read"},
		},
	}
	want := `justfile:1: unsupported: import: not read
justfile:2: converted: same
justfile:2: unsupported: set shell: not converted
justfile:4: converted: build
justfile:9: review: test:unit -> test-unit: first; second
justfile:20: unsupported: gen: uses a backtick command
justfile:30: wrapper: old: a shebang recipe
summary: 2 converted, 1 review, 3 unsupported, 1 wrappers
`
	got, lines, unsupported := emitReport(src, Options{SourceName: "justfile"})
	if got != want {
		t.Errorf("emitReport:\n%s\nwant:\n%s", got, want)
	}
	if lines != 7 || unsupported != 3 {
		t.Errorf("lines, unsupported = %d, %d, want 7, 3", lines, unsupported)
	}
}

func TestEmitReportComplete(t *testing.T) {
	src := &source{tasks: []*Task{
		{Source: "a", Name: "a", Line: 1},
		{Source: "b", Name: "b", Line: 2, Status: Wrapper, Notes: []string{"x"}},
		{Source: "c", Name: "c", Line: 3, Status: Review, Notes: []string{"y"}},
	}}
	_, lines, unsupported := emitReport(src, Options{SourceName: "f"})
	if lines != 3 || unsupported != 0 {
		t.Errorf("lines, unsupported = %d, %d, want 3, 0: wrappers and review tasks are not unsupported", lines, unsupported)
	}
}

func TestEmitReportQuotesControlCharacters(t *testing.T) {
	src := &source{
		tasks: []*Task{{Source: "a\nb", Name: "a-b", Line: 3}, {Source: "plain name", Name: "plain-name", Line: 4}},
		items: []Item{{Line: 1, Name: "env X\tY", Note: "n"}},
	}
	want := "f:1: unsupported: \"env X\\tY\": n\n" +
		"f:3: converted: \"a\\nb\" -> a-b\n" +
		"f:4: converted: plain name -> plain-name\n" +
		"summary: 2 converted, 0 review, 1 unsupported, 0 wrappers\n"
	if got, _, _ := emitReport(src, Options{SourceName: "f"}); got != want {
		t.Errorf("emitReport:\n%s\nwant:\n%s", got, want)
	}
}

func TestShowName(t *testing.T) {
	for in, want := range map[string]string{"build": "build", "test:unit": "test:unit", "a\nb": `"a\nb"`, "x\x7f": `"x\x7f"`, "": ""} {
		if got := showName(in); got != want {
			t.Errorf("showName(%q) = %s, want %s", in, got, want)
		}
	}
}
