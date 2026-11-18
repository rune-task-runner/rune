package taskimport

import (
	"slices"
	"testing"
)

func TestJustString(t *testing.T) {
	ok := map[string]string{
		`'raw \n text'`: `raw \n text`,
		`"a\tb\n"`:      "a\tb\n",
		`"q\"q\\"`:      `q"q\`,
		`"cr\r"`:        "cr\r",
		`''`:            "",
	}
	for in, want := range ok {
		got, valid := justString(in)
		if !valid || got != want {
			t.Errorf("justString(%s) = %q, %v, want %q", in, got, valid, want)
		}
	}
	for _, in := range []string{`"\u{1F600}"`, `"\x"`, `'open`, `"open`, `"""triple"""`, `'''triple'''`, `x'~/a'`, `name`, `"a" "b"`, `"dangling\`} {
		if got, valid := justString(in); valid {
			t.Errorf("justString(%s) = %q, want it rejected", in, got)
		}
	}
}

func TestJustExpr(t *testing.T) {
	tests := []struct {
		in     string
		expr   string
		refs   []string
		reason string
	}{
		{`"dist"`, `"dist"`, nil, ""},
		{`'a' + b + "c"`, `"a" + b + "c"`, []string{"b"}, ""},
		{`out / "app"`, `out + "/" + "app"`, []string{"out"}, ""},
		{`"x"   # trailing comment`, `"x"`, nil, ""},
		{`"has # hash"`, `"has # hash"`, nil, ""},
		{"`git rev-parse HEAD`", "", nil, "its value runs a command (backtick), which import never runs"},
		{`env_var("HOME")`, "", nil, "its value calls a function or is not built from string literals, variables, + and /"},
		{`if a == "b" { "c" } else { "d" }`, "", nil, "its value calls a function or is not built from string literals, variables, + and /"},
		{`"a" +`, "", nil, "its value calls a function or is not built from string literals, variables, + and /"},
		{`"bad\q"`, "", nil, "its value calls a function or is not built from string literals, variables, + and /"},
		{``, "", nil, "its value calls a function or is not built from string literals, variables, + and /"},
		{"\"bell\a\"", "", nil, "its value has a control character"},
	}
	for _, tt := range tests {
		expr, refs, reason := justExpr(tt.in)
		if expr != tt.expr || !slices.Equal(refs, tt.refs) || reason != tt.reason {
			t.Errorf("justExpr(%s) = %q, %q, %q\nwant %q, %q, %q", tt.in, expr, refs, reason, tt.expr, tt.refs, tt.reason)
		}
	}
}

func TestJustHeader(t *testing.T) {
	tests := []struct {
		in                 string
		quiet              bool
		name, params, deps string
		ok                 bool
	}{
		{`build:`, false, "build", "", "", true},
		{`@quiet target="x": a b`, true, "quiet", `target="x"`, "a b", true},
		{`serve url="http://x:80" +rest: (dep "a:b") && c`, false, "serve", `url="http://x:80" +rest`, `(dep "a:b") && c`, true},
		{`_p-1 a='it''s':`, false, "_p-1", `a='it''s'`, "", true},
		{`name := "value"`, false, "", "", "", false},
		{`no colon here`, false, "", "", "", false},
		{`9bad:`, false, "", "", "", false},
		{`open "quote:`, false, "", "", "", false},
	}
	for _, tt := range tests {
		quiet, name, params, deps, ok := justHeader(tt.in)
		if quiet != tt.quiet || name != tt.name || params != tt.params || deps != tt.deps || ok != tt.ok {
			t.Errorf("justHeader(%q) = %v %q %q %q %v", tt.in, quiet, name, params, deps, ok)
		}
	}
}

func TestJustParams(t *testing.T) {
	tests := []struct {
		in     string
		params []string
		names  []string
		reason string
	}{
		{``, nil, nil, ""},
		{`env target="debug" opt='a b'`, []string{"env", `target="debug"`, `opt="a b"`}, []string{"env", "target", "opt"}, ""},
		{`+files`, []string{"+files"}, []string{"files"}, ""},
		{`first *rest`, []string{"first", "*rest"}, []string{"first", "rest"}, ""},
		{`v=version`, nil, nil, `parameter "v" has a default that is not a string literal`},
		{"v=`date`", nil, nil, `parameter "v" has a default that is not a string literal`},
		{`$env`, nil, nil, `parameter "$env" is exported to the environment`},
		{`*rest="x"`, nil, nil, `variadic parameter "rest" has a default`},
		{`if`, nil, nil, `parameter "if" is not a valid Rune name`},
		{`a.b`, nil, nil, `parameter "a.b" is not a valid Rune name`},
		{`a a`, nil, nil, `parameter "a" is declared twice`},
		{`*rest last`, nil, nil, `a parameter follows the variadic parameter`},
		{`a="x" b`, nil, nil, `the required parameter "b" follows one with a default`},
		{`a="x" *rest`, []string{`a="x"`, "*rest"}, []string{"a", "rest"}, ""},
	}
	for _, tt := range tests {
		params, names, reason := justParams(tt.in)
		if !slices.Equal(params, tt.params) || !slices.Equal(names, tt.names) || reason != tt.reason {
			t.Errorf("justParams(%q) = %q, %q, %q\nwant %q, %q, %q", tt.in, params, names, reason, tt.params, tt.names, tt.reason)
		}
	}
}

func TestJustDeps(t *testing.T) {
	known := func(name string) bool { return name == "target" }
	tests := []struct {
		in     string
		deps   []Call
		post   []Call
		reason string
	}{
		{``, nil, nil, ""},
		{`a b`, []Call{{Name: "a"}, {Name: "b"}}, nil, ""},
		{`a && b c`, []Call{{Name: "a"}}, []Call{{Name: "b"}, {Name: "c"}}, ""},
		{`(build "rel ease" target) && (notify 'done')`, []Call{{Name: "build", Args: `"rel ease" target`}}, []Call{{Name: "notify", Args: `"done"`}}, ""},
		{`&& after`, nil, []Call{{Name: "after"}}, ""},
		{`a # trailing comment`, []Call{{Name: "a"}}, nil, ""},
		{`a # don't (yet`, []Call{{Name: "a"}}, nil, ""},
		{`(build "x # y") # it's built`, []Call{{Name: "build", Args: `"x # y"`}}, nil, ""},
		{`# only a comment`, nil, nil, ""},
		{`(build other)`, nil, nil, `the argument "other" of dependency "build" is not a string literal, a parameter or a variable`},
		{"(build `date`)", nil, nil, "the argument \"`date`\" of dependency \"build\" is not a string literal, a parameter or a variable"},
		{`(build`, nil, nil, `the dependency list is not understood: (build`},
		{`()`, nil, nil, `the dependency list is not understood: ()`},
		{`a && b && c`, nil, nil, `the dependency list is not understood: a && b && c`},
	}
	for _, tt := range tests {
		deps, post, reason := justDeps(tt.in, known)
		if !slices.Equal(deps, tt.deps) || !slices.Equal(post, tt.post) || reason != tt.reason {
			t.Errorf("justDeps(%q) = %v, %v, %q\nwant %v, %v, %q", tt.in, deps, post, reason, tt.deps, tt.post, tt.reason)
		}
	}
}

func TestJustAttrs(t *testing.T) {
	tests := []struct {
		in     string
		attrs  []string
		reason string
	}{
		{`[private]`, []string{"private"}, ""},
		{`[private, no-cd]`, []string{"private", "no-cd"}, ""},
		{`[confirm]`, []string{"confirm"}, ""},
		{`[confirm("Really, sure?")]`, []string{`confirm("Really, sure?")`}, ""},
		{`[group('dev'), doc("Build it"), working-directory("sub dir")]`, []string{`group("dev")`, `doc("Build it")`, `working-directory("sub dir")`}, ""},
		{`[linux, macos, windows, unix, no-exit-message]`, []string{"linux", "macos", "windows", "unix", "no-exit-message"}, ""},
		{`[script("bash")]`, nil, `the attribute [script("bash")] is not converted`},
		{`[private, positional-arguments]`, []string{"private"}, `the attribute [positional-arguments] is not converted`},
		{`[group: 'dev']`, nil, `the attribute [group: 'dev'] is not converted`},
		{`[group(name)]`, nil, `the attribute [group(name)] is not converted`},
		{`[private("x")]`, nil, `the attribute [private("x")] is not converted`},
		{`[group]`, nil, `the attribute [group] is not converted`},
	}
	for _, tt := range tests {
		attrs, reason := justAttrs(tt.in)
		if !slices.Equal(attrs, tt.attrs) || reason != tt.reason {
			t.Errorf("justAttrs(%q) = %q, %q\nwant %q, %q", tt.in, attrs, reason, tt.attrs, tt.reason)
		}
	}
}

func TestJustFields(t *testing.T) {
	got := justFields(`a "b c" (d 'e f') g='h i'  j`)
	want := []string{"a", `"b c"`, `(d 'e f')`, `g='h i'`, "j"}
	if !slices.Equal(got, want) {
		t.Errorf("justFields = %q, want %q", got, want)
	}
	if got := justFields(`open "quote`); got != nil {
		t.Errorf("justFields with an open quote = %q, want nil", got)
	}
	if got := justFields(`(open paren`); got != nil {
		t.Errorf("justFields with an open parenthesis = %q, want nil", got)
	}
}

func TestReadJustErrors(t *testing.T) {
	tests := []struct {
		name, text string
		line       int
		msg        string
	}{
		{"unknown top-level text", "build:\n    echo a\n\n???\n", 4, "this line is not a task, a variable or a setting: ???"},
		{"a body without a recipe", "    echo a\n", 1, "an indented line outside a task"},
		{"mixed indentation", "a:\n    echo 1\n  echo 2\n", 3, "the line is indented less than the first line of the task"},
		{"too few arguments", "a: b\n    echo a\n\nb p:\n    echo b\n", 1, `task "a" calls "b" with 0 argument(s), which it does not accept`},
		{"too many arguments", "b p='x':\n    echo b\n\na: b && (b 'x' 'y')\n    echo a\n", 4, `task "a" calls "b" with 2 argument(s), which it does not accept`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := readJust(tt.text)
			pe, ok := err.(*ParseError)
			if !ok || pe.Line != tt.line || pe.Msg != tt.msg {
				t.Errorf("readJust error = %v, want line %d: %s", err, tt.line, tt.msg)
			}
		})
	}
}

func TestJustArity(t *testing.T) {
	tests := []struct {
		params []string
		lo, hi int
	}{
		{nil, 0, 0},
		{[]string{"a", `b="x"`}, 1, 2},
		{[]string{"a", "+rest"}, 2, -1},
		{[]string{`a="x"`, "*rest"}, 0, -1},
	}
	for _, tt := range tests {
		if lo, hi := justArity(tt.params); lo != tt.lo || hi != tt.hi {
			t.Errorf("justArity(%q) = %d, %d, want %d, %d", tt.params, lo, hi, tt.lo, tt.hi)
		}
	}
}

func TestReadJustAcceptsFittingCalls(t *testing.T) {
	text := "all: (b 'x') (c) (c 'p' 'q') (stub 'anything')\n\nb p q='d':\n    echo b\n\nc *rest:\n    echo c\n\nstub:\n    echo `x`{{nope}}\n"
	if _, err := readJust(text); err != nil {
		t.Errorf("readJust: %v, want the calls accepted", err)
	}
}

func TestJustCode(t *testing.T) {
	tests := []struct{ in, want string }{
		{`a b`, `a b`},
		{`a b # note`, `a b `},
		{`(a "x # y") # note`, `(a "x # y") `},
		{`(a 'x # y')#note`, `(a 'x # y')`},
		{`(a "q \" # y") # n`, `(a "q \" # y") `},
		{`# all`, ``},
		{``, ``},
	}
	for _, tt := range tests {
		if got := justCode(tt.in); got != tt.want {
			t.Errorf("justCode(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestJustStatement(t *testing.T) {
	lines := []string{
		`set shell := [`, `  "bash", # with ] in a comment`, `  "-c",`, `]`,
		`x := "one line"`,
		`y := ("a" +`, `  "b")`,
		`z := "bracket [ in a string" # and ( in a comment`,
		`open := [`,
	}
	tests := []struct {
		at   int
		text string
		last int
	}{
		{0, `set shell := [ "bash", # with ] in a comment "-c", ]`, 3},
		{4, `x := "one line"`, 4},
		{5, `y := ("a" + "b")`, 6},
		{7, `z := "bracket [ in a string" # and ( in a comment`, 7},
		{8, `open := [`, 8},
	}
	for _, tt := range tests {
		if text, last := justStatement(lines, tt.at); text != tt.text || last != tt.last {
			t.Errorf("justStatement at %d = %q, %d, want %q, %d", tt.at, text, last, tt.text, tt.last)
		}
	}
}

func TestReadJustMultiLineStatements(t *testing.T) {
	text := "set shell := [\n  \"bash\",\n  \"-c\",\n]\n\nflags := (\"-v\" +\n  \" -x\")\n\nbuild:\n    echo build\n"
	src, err := readJust(text)
	if err != nil {
		t.Fatalf("readJust: %v", err)
	}
	if len(src.tasks) != 1 || src.tasks[0].Line != 9 {
		t.Errorf("tasks = %+v, want build at line 9", src.tasks)
	}
	if len(src.items) != 2 || src.items[0].Name != "set shell" || src.items[1].Name != "variable flags" {
		t.Errorf("items = %+v, want the setting and the variable", src.items)
	}
}

func TestJustEscape(t *testing.T) {
	for in, want := range map[string]string{
		"echo hi":                  "echo hi",
		"echo {{name}}":            "echo {{name}}",
		"echo {{{{literal}}":       "echo {{{{literal}}",
		"echo {{ a }}}}":           "echo {{ a }}}}",
		"echo {{a}}}}}}":           "echo {{a}}}}}}}}}}",
		"echo '{a:{b:{c:{d:1}}}}'": "echo '{a:{b:{c:{d:1}}}}}}}}'",
		"}}}}{{a}}}}}":             "}}}}}}}}{{a}}}}}",
	} {
		if got := justEscape(in); got != want {
			t.Errorf("justEscape(%q) = %q, want %q", in, got, want)
		}
	}
}

// A body keeps four closing braces in a row: Rune would read them as two.
func TestReadJustClosingBraces(t *testing.T) {
	text := "v := \"1\"\n\nshow:\n    echo '{a:{b:{c:{d:{{v}}}}}}' '{a:{b:{c:{}}}}'\n"
	res, err := Convert([]byte(text), Options{Format: FormatJust, SourceName: "justfile"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"echo '{a:{b:{c:{d:{{v}}}}}}}}}}' '{a:{b:{c:{}}}}}}}}'"}; res.Tasks[0].Status != Converted || !slices.Equal(res.Tasks[0].Body, want) {
		t.Errorf("show is %v with body %q, want converted with %q", res.Tasks[0].Status, res.Tasks[0].Body, want)
	}
	checkRunefile(t, res.Runefile)
}

// A task can read an exported variable as a shell variable, which no
// reference in its body shows, so the export lowers every task to review.
func TestReadJustExportAffectsEveryTask(t *testing.T) {
	res, err := Convert([]byte("export FOO := \"bar\"\n\nshow:\n    echo $FOO\n"), Options{Format: FormatJust, SourceName: "justfile"})
	if err != nil {
		t.Fatal(err)
	}
	want := "justfile:1: unsupported: variable FOO: it is exported to the environment of every task, and no imported task has it\n" +
		"justfile:3: review: show: see justfile:1\n" +
		"summary: 0 converted, 1 review, 1 unsupported, 0 wrappers\n"
	if res.Report != want {
		t.Errorf("report:\n%s\nwant:\n%s", res.Report, want)
	}
}
