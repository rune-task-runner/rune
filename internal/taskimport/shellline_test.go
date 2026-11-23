package taskimport

import (
	"slices"
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/eval"
	"github.com/rune-task-runner/rune/internal/token"
)

func TestParseLine(t *testing.T) {
	for _, line := range []string{
		`echo hi`,
		`go build -o {{quote(out)}} ./...`,
		`echo "{{name}}" && ls`,
		`echo '{{{{ literal }}'`,
		`if true; then echo a; fi`,
	} {
		if _, err := parseLine(line); err != nil {
			t.Errorf("parseLine(%q) failed: %v", line, err)
		}
	}
	for _, line := range []string{`echo "open`, `if true; then`, `echo $(`, `cat <<EOF`} {
		if _, err := parseLine(line); err == nil {
			t.Errorf("parseLine(%q) succeeded, want an error", line)
		}
	}
}

func TestChangesState(t *testing.T) {
	stateful := []string{
		`cd sub`, `pushd x`, `popd`, `export A=1`, `unset A`, `set -e`, `shopt -s nullglob`,
		`alias l=ls`, `unalias l`, `source env.sh`, `. env.sh`, `eval "$X"`, `exec ls`, `exit 0`,
		`umask 022`, `trap 'x' EXIT`, `readonly A=1`, `declare A=1`, `typeset A=1`, `local A=1`,
		`read line`, `A=1`, `A=1 B=2`, `$CMD arg`, `"cd" sub`, `{{tool}} run`,
		`for f in *; do echo "$f"; done`, `f() { echo x; }`, `let x=1`, `((x++))`,
		`echo ${X:=default}`, `echo $((x+1))`, `true && cd sub`, `if true; then cd sub; fi`,
		`cd sub | cat`, `{ cd sub; }`, `command cd sub`, `builtin export A=1`, `shift`, `ulimit -n 10`,
		`\cd sub`, `c\d sub`, `wait`, `printf -v out '%s' x`, `mapfile lines`, `getopts ab opt`, `hash -r`, `: ${X:=v}`,
	}
	for _, line := range stateful {
		f, err := parseLine(line)
		if err != nil {
			t.Fatalf("parseLine(%q): %v", line, err)
		}
		if !changesState(f) {
			t.Errorf("changesState(%q) = false, want true", line)
		}
	}
	stateless := []string{
		`echo a && ls | wc -l`, `(cd sub && ls)`, `A=1 cmd`, `echo $(cd sub; pwd)`,
		`go test ./... > out.txt 2>&1`, `if test -f x; then echo yes; fi`, `! grep -q x file`,
		`echo "$HOME" ${X:-d}`, `while false; do echo x; done`, `cat <(cd sub; ls)`,
		`printf '%s\n' "$x"`, `printf -- '-v'`, `pwd`, `true`, `[ -f x ] && test -d y`, `: comment`, `type ls`,
	}
	for _, line := range stateless {
		f, err := parseLine(line)
		if err != nil {
			t.Fatalf("parseLine(%q): %v", line, err)
		}
		if changesState(f) {
			t.Errorf("changesState(%q) = true, want false", line)
		}
	}
}

func TestShellBody(t *testing.T) {
	tests := []struct {
		name     string
		lines    []cmdLine
		isolated bool
		want     []string
		reason   string
	}{
		{
			name:     "prefixes and blank lines",
			lines:    []cmdLine{{text: " echo a "}, {text: ""}, {text: "echo b", noEcho: true}, {text: "false", ignore: true}, {text: "x", noEcho: true, ignore: true}},
			isolated: true,
			want:     []string{"echo a", "@echo b", "-false", "@-x"},
		},
		{
			name:     "a state change before another line is wrapped",
			lines:    []cmdLine{{text: "cd sub", noEcho: true}, {text: "make"}},
			isolated: true,
			want:     []string{"@(cd sub)", "make"},
		},
		{
			name:     "the last line is not wrapped",
			lines:    []cmdLine{{text: "echo a"}, {text: "cd sub"}},
			isolated: true,
			want:     []string{"echo a", "cd sub"},
		},
		{
			name:  "one shell for the whole body wraps nothing",
			lines: []cmdLine{{text: "cd sub"}, {text: "make"}},
			want:  []string{"cd sub", "make"},
		},
		{
			name:     "a line that is not a complete command",
			lines:    []cmdLine{{text: `echo "open`}},
			isolated: true,
			reason:   `not a complete shell command on one line: echo "open`,
		},
		{
			name:     "a command that starts with a prefix character",
			lines:    []cmdLine{{text: "-n x"}},
			isolated: true,
			reason:   `the command starts with "-", which Rune reads as a line prefix: -n x`,
		},
		{
			name:     "a state change with a trailing comment cannot be wrapped",
			lines:    []cmdLine{{text: "cd sub # go there"}, {text: "ls"}},
			isolated: true,
			reason:   `changes shell state and cannot be wrapped in a subshell: cd sub # go there`,
		},
		{
			name:     "a line break inside a command",
			lines:    []cmdLine{{text: "echo a\necho b"}},
			isolated: true,
			reason:   "the command spans several lines: echo a",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := shellBody(tt.lines, tt.isolated)
			if reason != tt.reason {
				t.Fatalf("reason = %q, want %q", reason, tt.reason)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("body = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEscapeBraces(t *testing.T) {
	for in, want := range map[string]string{
		"echo hi":                      "echo hi",
		"docker ps --format '{{.ID}}'": "docker ps --format '{{{{.ID}}'",
		"{{a}} {{b}}":                  "{{{{a}} {{{{b}}",
		"{ {x} }":                      "{ {x} }",
		"{{{":                          "{{{{{",
		"}}":                           "}}",
		"}}}":                          "}}}",
		"}}}}":                         "}}}}}}}}",
		"}}}}}":                        "}}}}}}}}}",
		`'{"a":{"b":{"c":{"d":1}}}}'`:  `'{"a":{"b":{"c":{"d":1}}}}}}}}'`,
	} {
		if got := EscapeBraces(in); got != want {
			t.Errorf("EscapeBraces(%q) = %q, want %q", in, got, want)
		}
	}
}

// Rune prints escaped text as the source had it: four closing braces are
// not read as two.
func TestEscapeBracesRoundTrip(t *testing.T) {
	ev := eval.New(eval.NewScope(nil, nil))
	for _, in := range []string{
		"echo hi", "{", "{{", "{{{", "{{{{", "}", "}}", "}}}", "}}}}", "}}}}}", "}}}}}}", "}}}}}}}}",
		"{{a}}", "{{a}}}}", "{{{{a}}}}", `curl -d '{"a":{"b":{"c":{"d":1}}}}' x`, "}}{{", "{{}}}}{{",
	} {
		got, err := ev.Interpolate(EscapeBraces(in), token.Span{})
		if err != nil || got != in {
			t.Errorf("Rune reads EscapeBraces(%q) as %q, %v", in, got, err)
		}
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"build":     "build",
		"a/b.c-d_e": "a/b.c-d_e",
		"":          "''",
		"a b":       "'a b'",
		"it's":      `'it'\''s'`,
		"test:unit": "test:unit",
		"$x":        "'$x'",
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
	if got := shellQuote("a{{b"); !strings.Contains(got, "{{{{") {
		t.Errorf("shellQuote keeps the braces unescaped for Rune: %q", got)
	}
}
