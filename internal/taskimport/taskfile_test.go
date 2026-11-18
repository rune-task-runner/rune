package taskimport

import (
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestTaskTemplate(t *testing.T) {
	good := map[string]bool{"OUT": true}
	bad := map[string]int{"NOW": 4}
	tests := []struct {
		in, out string
		args    bool
		reason  string
	}{
		{`go build ./...`, `go build ./...`, false, ""},
		{`go build -o {{.OUT}}/app`, `go build -o {{OUT}}/app`, false, ""},
		{`echo {{ .OUT }} {{.OUT}}`, `echo {{OUT}} {{OUT}}`, false, ""},
		{`go test {{.CLI_ARGS}}`, `go test {{quote_each(args)}}`, true, ""},
		{`echo {{.NOW}}`, "", false, `it uses the variable "NOW" of line 4, which was not converted`},
		{`echo {{.MISSING}}`, "", false, `it uses {{.MISSING}}, which the Taskfile does not set`},
		{`echo {{.TASK}}`, "", false, `it uses {{.TASK}}, which the Taskfile does not set`},
		{`echo {{OS}}`, "", false, `the template {{OS}} is not a plain variable`},
		{`echo {{.OUT | upper}}`, "", false, `the template {{.OUT | upper}} is not a plain variable`},
		{`echo {{if .X}}a{{end}}`, "", false, `the template {{if .X}} is not a plain variable`},
		{`echo {{.OUT`, "", false, `a template is not closed: echo {{.OUT`},
		{`echo '{a:{b:{c:{d:{{.OUT}}}}}}' '{a:{b:{}}}}'`, `echo '{a:{b:{c:{d:{{OUT}}}}}}}}}}' '{a:{b:{}}}}}}}}'`, false, ""},
	}
	for _, tt := range tests {
		out, args, reason := taskTemplate(tt.in, good, bad)
		if out != tt.out || args != tt.args || reason != tt.reason {
			t.Errorf("taskTemplate(%q) = %q, %v, %q\nwant %q, %v, %q", tt.in, out, args, reason, tt.out, tt.args, tt.reason)
		}
	}
}

func yamlNode(t *testing.T, text string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Content[0]
}

func TestTaskScalar(t *testing.T) {
	for text, want := range map[string]string{
		`hello`: "hello", `"quoted text"`: "quoted text", `3`: "3", `1.5`: "1.5", `true`: "true", `'no'`: "no",
	} {
		got, ok := taskScalar(yamlNode(t, text))
		if !ok || got != want {
			t.Errorf("taskScalar(%s) = %q, %v, want %q", text, got, ok, want)
		}
	}
	for _, text := range []string{`{sh: date}`, `[a, b]`, `~`} {
		if got, ok := taskScalar(yamlNode(t, text)); ok {
			t.Errorf("taskScalar(%s) = %q, want it rejected", text, got)
		}
	}
}

func TestTaskTrue(t *testing.T) {
	for _, text := range []string{`true`, `True`, `TRUE`, `yes`, `on`} {
		if !taskTrue(yamlNode(t, text)) {
			t.Errorf("taskTrue(%s) = false, want true", text)
		}
	}
	for _, text := range []string{`false`, `no`, `1`, `text`, `~`, `[true]`, `{a: true}`} {
		if taskTrue(yamlNode(t, text)) {
			t.Errorf("taskTrue(%s) = true, want false", text)
		}
	}
}

// Task reads these keys as booleans, in any spelling YAML has for true.
func TestReadTaskfileBooleans(t *testing.T) {
	text := "version: '3'\nsilent: yes\ntasks:\n  a:\n    internal: True\n    ignore_error: TRUE\n    cmds:\n      - 'false'\n" +
		"  b:\n    cmds:\n      - cmd: echo b\n        ignore_error: on\n"
	res, err := Convert([]byte(text), Options{Format: FormatTask, SourceName: "Taskfile.yml"})
	if err != nil {
		t.Fatal(err)
	}
	a, b := res.Tasks[0], res.Tasks[1]
	if !slices.Equal(a.Attrs, []string{"private"}) || !slices.Equal(a.Body, []string{"@-false"}) {
		t.Errorf("a has attributes %q and body %q, want private and @-false", a.Attrs, a.Body)
	}
	if !slices.Equal(b.Body, []string{"@-echo b"}) {
		t.Errorf("b has body %q, want @-echo b", b.Body)
	}
}

// The parameter that takes {{.CLI_ARGS}} is named args, and it would hide a
// variable of that name.
func TestReadTaskfileArgsVariable(t *testing.T) {
	text := "version: '3'\nvars:\n  args: fromvar\ntasks:\n" +
		"  both: echo {{.args}} {{.CLI_ARGS}}\n  variable: echo {{.args}}\n  arguments: echo {{.CLI_ARGS}}\n"
	res, err := Convert([]byte(text), Options{Format: FormatTask, SourceName: "Taskfile.yml"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Status{"both": Unsupported, "variable": Converted, "arguments": Converted}
	for _, task := range res.Tasks {
		if task.Status != want[task.Source] {
			t.Errorf("%s is %v with %q, want %v", task.Source, task.Status, task.Notes, want[task.Source])
		}
	}
	checkRunefile(t, res.Runefile)
}

func TestTaskValue(t *testing.T) {
	tests := []struct {
		in     string
		expr   string
		refs   []string
		reason string
	}{
		{`dist`, `"dist"`, nil, ""},
		{``, `""`, nil, ""},
		{`{{.OUT}}/app`, `OUT + "/app"`, []string{"OUT"}, ""},
		{`a-{{ .A }}-{{.B}}`, `"a-" + A + "-" + B`, []string{"A", "B"}, ""},
		{`{{.CLI_ARGS}}`, "", nil, "its value is not built from text and plain variables"},
		{`{{now}}`, "", nil, "its value is not built from text and plain variables"},
		{`{{.OPEN`, "", nil, "its value is not built from text and plain variables"},
		{"bell\a", "", nil, "its value has a control character"},
	}
	for _, tt := range tests {
		expr, refs, reason := taskValue(tt.in)
		if expr != tt.expr || len(refs) != len(tt.refs) || reason != tt.reason {
			t.Errorf("taskValue(%q) = %q, %q, %q\nwant %q, %q, %q", tt.in, expr, refs, reason, tt.expr, tt.refs, tt.reason)
		}
	}
}

func TestReadTaskfileErrors(t *testing.T) {
	tests := []struct {
		name, text string
		line       int
		msg        string
	}{
		{"not yaml", "version: '3'\ntasks:\n  a: [unclosed\n", 2, "did not find expected ',' or ']'"},
		{"not a mapping", "- a\n- b\n", 1, "the Taskfile is not a mapping"},
		{"empty", "", 1, "the Taskfile is not a mapping"},
		{"no version", "tasks:\n  a: echo a\n", 1, "the Taskfile has no version"},
		{"an old version", "version: '2'\ntasks:\n  a: echo a\n", 1, `Taskfile version "2" is not supported (want 3)`},
		{"tasks is not a mapping", "version: '3'\ntasks: [a]\n", 2, "tasks is not a mapping"},
		{"a task defined twice", "version: '3'\ntasks:\n  a: echo 1\n  a: echo 2\n", 4, `task "a" is defined twice`},
		{"a cycle", "version: '3'\ntasks:\n  a:\n    deps: [b]\n  b:\n    cmds:\n      - task: a\n", 3, "dependency cycle: a -> b -> a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Convert([]byte(tt.text), Options{Format: FormatTask, SourceName: "Taskfile.yml"})
			pe, ok := err.(*ParseError)
			if !ok || pe.Line != tt.line || (tt.msg != "" && pe.Msg != tt.msg) {
				t.Errorf("error = %v, want line %d: %s", err, tt.line, tt.msg)
			}
		})
	}
}

func TestTaskVersion(t *testing.T) {
	for _, v := range []string{"3", "3.0", "3.44.1"} {
		if !taskVersion(v) {
			t.Errorf("taskVersion(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"2", "2.6", "30", "", "v3"} {
		if taskVersion(v) {
			t.Errorf("taskVersion(%q) = true, want false", v)
		}
	}
}

// A command of several lines with a template that is not closed is reported
// by its first line: the other lines must not become lines of the Runefile.
func TestReadTaskfileUnclosedTemplateOverLines(t *testing.T) {
	text := "version: '3'\ntasks:\n  a:\n    cmds:\n      - |\n        echo {{.FOO |\n        upper}}\n"
	res, err := Convert([]byte(text), Options{Format: FormatTask, SourceName: "Taskfile.yml"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a template is not closed: echo {{.FOO |"}; res.Tasks[0].Status != Unsupported || !slices.Equal(res.Tasks[0].Notes, want) {
		t.Errorf("a is %v with %q, want unsupported with %q", res.Tasks[0].Status, res.Tasks[0].Notes, want)
	}
	if got := strings.Count(res.Report, "\n"); got != 2 {
		t.Errorf("the report has %d lines, want 2:\n%s", got, res.Report)
	}
	checkRunefile(t, res.Runefile)
}
