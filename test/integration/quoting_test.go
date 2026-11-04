package integration

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Spec 030 (contracts/runefile-surface.md): quote_each passes each element of
// a variadic as its own quoted shell word, from the command line and over MCP.
const quoteEachRunefile = `c *names:
    @printf "[%s]\n" {{quote_each(names)}}

n *names:
    @set -- {{quote_each(names)}}; echo "$#"

p +names:
    @printf "[%s]\n" {{quote_each(names)}}
`

func TestQuoteEach_CLI(t *testing.T) {
	dir := writeRunefile(t, quoteEachRunefile)
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"c", "yq", "x; echo pwned", "tofu"}, "[yq]\n[x; echo pwned]\n[tofu]\n"},
		{[]string{"c", "a b", "", "it's"}, "[a b]\n[]\n[it's]\n"},
		{[]string{"n"}, "0\n"},
		{[]string{"n", "a b", "c"}, "2\n"},
		{[]string{"p", "yq"}, "[yq]\n"},
	}
	for _, tt := range tests {
		r := run(t, dir, nil, tt.args...)
		if r.code != 0 || r.stdout != tt.want {
			t.Errorf("rune %q: stdout = %q, exit %d; want %q, 0 (stderr %q)", tt.args, r.stdout, r.code, tt.want, r.stderr)
		}
	}
}

func TestQuoteEach_SetShellBash(t *testing.T) {
	requireBash(t)
	dir := writeRunefile(t, "set shell := [\"bash\", \"-eu\"]\n\n"+quoteEachRunefile)
	r := run(t, dir, nil, "c", "yq", "x; echo pwned", "tofu")
	if want := "[yq]\n[x; echo pwned]\n[tofu]\n"; r.code != 0 || r.stdout != want {
		t.Errorf("stdout = %q, exit %d; want %q, 0 (stderr %q)", r.stdout, r.code, want, r.stderr)
	}
}

// toolText extracts the text content of a tools/call reply.
func toolText(t *testing.T, reply string) string {
	t.Helper()
	var msg struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(reply), &msg); err != nil || len(msg.Result.Content) == 0 {
		t.Fatalf("unexpected tools/call reply %q: %v", reply, err)
	}
	return msg.Result.Content[0].Text
}

func TestQuoteEach_MCP(t *testing.T) {
	dir := writeRunefile(t, quoteEachRunefile)
	tests := []struct {
		task, args, want string
	}{
		{"c", `{"names": ["yq", "x; echo pwned", "tofu"]}`, "[yq]\n[x; echo pwned]\n[tofu]\n[exit 0]"},
		{"n", `{"names": "a b"}`, "1\n[exit 0]"},
		{"n", `{"names": ""}`, "0\n[exit 0]"},
	}
	for _, tt := range tests {
		reply, stderr := mcpCallArgs(t, dir, tt.task, tt.args)
		if got := toolText(t, reply); got != tt.want {
			t.Errorf("tools/call %s %s = %q, want %q (stderr %q)", tt.task, tt.args, got, tt.want, stderr)
		}
	}
}

// Spec 030 Story 2: the issue's repro prints exactly what v0.6.0 printed.
func TestQuoting_OldFormsUnchanged(t *testing.T) {
	dir := writeRunefile(t, `a *names:
    @printf "[%s]\n" {{names}}

b *names:
    @printf "[%s]\n" {{quote(names)}}

commit +msg:
    @printf '%s\n' {{quote(msg)}}
`)
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"a", "yq", "x; echo pwned", "tofu"}, "[yq]\n[x]\npwned tofu\n"},
		{[]string{"b", "yq", "x; echo pwned", "tofu"}, "[yq x; echo pwned tofu]\n"},
		{[]string{"commit", "fix", "the", "build"}, "fix the build\n"},
	}
	for _, tt := range tests {
		r := run(t, dir, nil, tt.args...)
		if r.code != 0 || r.stdout != tt.want {
			t.Errorf("rune %q: stdout = %q, exit %d; want %q, 0 (stderr %q)", tt.args, r.stdout, r.code, tt.want, r.stderr)
		}
	}
}

// Spec 030 Story 5: forwarding keeps elements, and an empty list forwarded
// into a + parameter still runs the target, with no elements.
func TestQuoting_Forwarding(t *testing.T) {
	dir := writeRunefile(t, `n *names:
    @set -- {{quote_each(names)}}; echo "$#"

ci *pkgs: (n pkgs)
    @echo done

plus *pkgs: (need pkgs)
    @echo after

need +pkgs:
    @set -- {{quote_each(pkgs)}}; echo "$#"
`)
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"ci", "a b", "c"}, "2\ndone\n"},
		{[]string{"plus"}, "0\nafter\n"},
	}
	for _, tt := range tests {
		r := run(t, dir, nil, tt.args...)
		if r.code != 0 || r.stdout != tt.want {
			t.Errorf("rune %q: stdout = %q, exit %d; want %q, 0 (stderr %q)", tt.args, r.stdout, r.code, tt.want, r.stderr)
		}
	}
}

// Spec 030 Story 3: rune analyze reports RUNE2017 as a warning and exits 0,
// and running the task prints no warning.
func TestQuoting_Analyze(t *testing.T) {
	dir := writeRunefile(t, `a *names:
    @printf "[%s]\n" {{names}}

b *names:
    @printf "[%s]\n" {{quote(names)}}

c *names:
    @printf "[%s]\n" {{quote_each(names)}}

n *names:
    @set -- {{quote_each(names)}}; echo "$#"

ci *pkgs: (n pkgs)
    @echo done

plus *pkgs: (need pkgs)
    @echo after

need +pkgs:
    @set -- {{quote_each(pkgs)}}; echo "$#"
`)
	r := run(t, dir, nil, "analyze", "--json")
	if r.code != 0 {
		t.Fatalf("analyze --json exit %d; stdout %q stderr %q", r.code, r.stdout, r.stderr)
	}
	var report struct {
		Diagnostics []struct {
			Code     string `json:"code"`
			Severity string `json:"severity"`
			Message  string `json:"message"`
			Range    struct {
				Start struct {
					Line int `json:"line"`
				} `json:"start"`
			} `json:"range"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &report); err != nil {
		t.Fatalf("analyze --json output: %v\n%s", err, r.stdout)
	}
	var found []string
	for _, d := range report.Diagnostics {
		if d.Code == "RUNE2017" {
			found = append(found, fmt.Sprintf("%s line %d: %s", d.Severity, d.Range.Start.Line, d.Message))
		}
	}
	want := `warning line 2: parameter "names" is interpolated into a shell command unquoted; write {{quote_each(names)}} outside any shell quotes, or declare *names:raw if word splitting is intended`
	if len(found) != 1 || found[0] != want {
		t.Errorf("RUNE2017 findings = %q, want only %q", found, want)
	}

	if r := run(t, dir, nil, "analyze"); r.code != 0 || !strings.Contains(r.stdout+r.stderr, "RUNE2017") {
		t.Errorf("analyze: exit %d, output %q", r.code, r.stdout+r.stderr)
	}
	if r := run(t, dir, nil, "a", "x"); r.code != 0 || strings.Contains(r.stderr, "RUNE2017") || strings.Contains(r.stderr, "unquoted") {
		t.Errorf("rune a x: exit %d, stderr %q", r.code, r.stderr)
	}
}

// Spec 030 Story 4: raw splits into words as {{p}} always did, and agents are
// told so.
func TestQuoting_RawKind(t *testing.T) {
	dir := writeRunefile(t, `# Test with flags.
[param-doc("flags", "Extra go test flags")]
test flags:raw="-v -race":
    @printf "[%s]\n" {{flags}}
`)
	if r := run(t, dir, nil, "test"); r.code != 0 || r.stdout != "[-v]\n[-race]\n" {
		t.Errorf("rune test: stdout %q, exit %d, stderr %q", r.stdout, r.code, r.stderr)
	}
	reply := mcpToolsList(t, dir)
	if want := `"flags":{"description":"Extra go test flags. Split into shell words without quoting.","type":"string"}`; !strings.Contains(reply, want) {
		t.Errorf("tools/list missing %s\nreply: %s", want, reply)
	}
	if r := run(t, dir, nil, "analyze", "--json"); r.code != 0 || strings.Contains(r.stdout, "RUNE2017") || strings.Contains(r.stdout, "RUNE2012") {
		t.Errorf("analyze: exit %d, %s", r.code, r.stdout)
	}
}
