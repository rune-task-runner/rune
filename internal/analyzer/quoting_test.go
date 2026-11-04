package analyzer

import (
	"reflect"
	"regexp"
	"testing"

	"github.com/rune-task-runner/rune/internal/diag"
	"github.com/rune-task-runner/rune/internal/parser"
)

// quotingParams are the fixture's shared parameters
// (contracts/diagnostic-rune2017.md).
const quotingParams = `env version n:number ok:boolean mode:enum("a","b") *names`

func checkQuoting(t *testing.T, src string) diag.List {
	t.Helper()
	f, pdiags := parser.Parse("Runefile", src)
	if pdiags.HasErrors() {
		t.Fatalf("unexpected parse errors: %v", pdiags)
	}
	if errs := Analyze(f); errs.HasErrors() {
		t.Fatalf("fixture does not analyze cleanly: %v", errs)
	}
	return CheckQuoting(f)
}

var warnedName = regexp.MustCompile(`^parameter "([^"]+)"`)

// warnedParams lists the parameter each RUNE2017 warning names, in order.
func warnedParams(t *testing.T, diags diag.List) []string {
	t.Helper()
	var out []string
	for _, d := range diags {
		if d.Code != diag.CodeUnquotedParam || d.Severity != diag.Warning {
			t.Errorf("unexpected diagnostic %+v", d)
			continue
		}
		out = append(out, warnedName.FindStringSubmatch(d.Message)[1])
	}
	return out
}

func shellTask(line string) string {
	return "t " + quotingParams + ":\n    " + line + "\n"
}

// Spec 030 SC-004: rows 1 to 28 of contracts/diagnostic-rune2017.md.
func TestCheckQuotingFixture(t *testing.T) {
	tests := []struct {
		row  string
		src  string
		want []string
	}{
		{"1", shellTask(`./deploy.sh {{env}}`), []string{"env"}},
		{"2", shellTask(`printf "[%s]\n" {{names}}`), []string{"names"}},
		{"3", shellTask(`echo "hello {{env}}"`), []string{"env"}},
		{"4", shellTask(`echo '{{env}}'`), []string{"env"}},
		{"5", shellTask(`./deploy.sh {{quote(env)}}`), nil},
		{"6", shellTask(`printf "[%s]\n" {{quote_each(names)}}`), nil},
		{"7", shellTask(`echo {{quote("v" + version)}}`), nil},
		{"8", shellTask(`echo {{"--env=" + quote(env)}}`), nil},
		{"9", shellTask(`echo {{"--env=" + env}}`), []string{"env"}},
		{"10", shellTask(`echo {{trim(env)}}`), []string{"env"}},
		{"11", shellTask(`echo {{ if env == "prod" { "--strict" } else { "" } }}`), nil},
		{"12", shellTask(`echo {{ if mode == "a" { env } else { "" } }}`), []string{"env"}},
		{"13", shellTask(`echo {{env + env}}`), []string{"env"}},
		{"14", shellTask(`echo {{env}} {{env}}`), []string{"env", "env"}},
		{"15", shellTask(`echo {{n}} {{ok}} {{mode}}`), nil},
		{"17", "version := \"1\"\n\nt env:\n    echo {{version}}\n", nil},
		{"18", "t env (python):\n    print(\"{{env}}\")\n", nil},
		{"19 node", "t env (node):\n    console.log(\"{{env}}\")\n", nil},
		{"19 agent", "t env (agent):\n    Deploy to {{env}}.\n", nil},
		{"19 custom", "t env (ruby):\n    puts \"{{env}}\"\n", nil},
		{"20", "[script(\"bash -eu\")]\nt env:\n    echo {{env}}\n", []string{"env"}},
		{"21", "[script(\"python3\")]\nt env:\n    print(\"{{env}}\")\n", nil},
		{"22", "set shell := [\"bash\", \"-eu\"]\n\nt env:\n    echo {{env}}\n", []string{"env"}},
		{"23", "set shell := [\"pwsh\", \"-Command\"]\n\nt env:\n    echo {{env}}\n", nil},
		{"24", "set shell := [\"nix\", \"develop\", \"-c\", \"bash\"]\n\nt env:\n    echo {{env}}\n", []string{"env"}},
		{"25", "t env: (deploy env)\n    @echo t\n\ndeploy x:\n    @echo d\n", nil},
		{"26", "[confirm(\"Deploy {{env}}?\")]\nt env:\n    @echo hi\n", nil},
		{"27", shellTask(`echo {{path_exists(env)}}`), []string{"env"}},
		{"28", "t env (bash):\n    echo {{env}}\n", []string{"env"}},
		{"16", "t flags:raw:\n    go test {{flags}}\n", nil},
		{"16 variadic", "t *args:raw:\n    ./tool {{args}}\n", nil},
		// Beyond the table: first-appearance order, the documented miss for a
		// non-literal shell, and a path parameter.
		{"order", shellTask(`echo {{env + version + env}}`), []string{"env", "version"}},
		{"non-literal shell", "set shell := [env(\"SHELL\"), \"-eu\"]\n\nt env:\n    echo {{env}}\n", nil},
		{"path kind", "t dir:path:\n    ls {{dir}}\n", []string{"dir"}},
	}
	for _, tt := range tests {
		t.Run(tt.row, func(t *testing.T) {
			if got := warnedParams(t, checkQuoting(t, tt.src)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("warned %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCheckQuotingMessageAndSpan(t *testing.T) {
	tests := []struct {
		src, interp, want string
	}{
		{
			shellTask(`./deploy.sh {{env}}`), "{{env}}",
			`parameter "env" is interpolated into a shell command unquoted; write {{quote(env)}} outside any shell quotes, or declare env:raw if word splitting is intended`,
		},
		{
			shellTask(`printf "[%s]\n" {{names}}`), "{{names}}",
			`parameter "names" is interpolated into a shell command unquoted; write {{quote_each(names)}} outside any shell quotes, or declare *names:raw if word splitting is intended`,
		},
		{
			"t +names:\n    echo {{ names }}\n", "{{ names }}",
			`parameter "names" is interpolated into a shell command unquoted; write {{quote_each(names)}} outside any shell quotes, or declare +names:raw if word splitting is intended`,
		},
	}
	for _, tt := range tests {
		diags := checkQuoting(t, tt.src)
		if len(diags) != 1 {
			t.Fatalf("%q: got %v, want one warning", tt.src, diags)
		}
		d := diags[0]
		if d.Message != tt.want {
			t.Errorf("message = %q\nwant      %q", d.Message, tt.want)
		}
		if got := tt.src[d.Span.Start.Offset:d.Span.End.Offset]; got != tt.interp {
			t.Errorf("span covers %q, want %q", got, tt.interp)
		}
	}
}

// RUNE2017 is an analyze-time warning; the run path never reports it.
func TestAnalyzeExcludesQuotingWarning(t *testing.T) {
	if d := hasCode(analyzeDiags(t, shellTask(`./deploy.sh {{env}}`)), diag.CodeUnquotedParam); d != nil {
		t.Errorf("Analyze must not emit RUNE2017; got %+v", d)
	}
}

// raw is a known kind, so it gets no RUNE2012; a task named raw still gets the
// RUNE2016 shadowing warning.
func TestRawKindAnalyzes(t *testing.T) {
	diags := analyzeDiags(t, "t x:raw:\n    go test {{x}}\n")
	if d := hasCode(diags, diag.CodeUnknownParamType); d != nil {
		t.Errorf("raw reported as unknown: %+v", d)
	}
	shadow := analyzeDiags(t, "raw:\n    @echo r\n\nt x:raw:\n    @echo {{x}}\n")
	if hasCode(shadow, diag.CodeKindShadowsTask) == nil {
		t.Errorf("want RUNE2016 for a task named raw, got %v", shadow)
	}
}
