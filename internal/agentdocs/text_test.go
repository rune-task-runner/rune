package agentdocs

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

// esc is the escape the contract writes for one character inside a code
// span: a backslash, u and four hexadecimal digits.
func esc(r rune) string { return fmt.Sprintf("\\u%04x", r) }

const tick = "`"

// Two format characters that a reader does not see: a zero-width space and
// the override that turns the direction of the text around.
var (
	zeroWidth   = string(rune(0x200b))
	rightToLeft = string(rune(0x202e))
)

func TestProse(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"empty", "", ""},
		{"plain", "Build the app (release).", "Build the app (release)."},
		{"white space collapses", "  a \t b\n\nc\r\n d  ", "a b c d"},
		{"control and format characters", "a\x00b" + zeroWidth + "c" + rightToLeft + "d", "a b c d"},
		{"joiners inside a word stay", "a" + string(zeroWidthNonJoiner) + "b" + string(zeroWidthJoiner) + "c", "a" + string(zeroWidthNonJoiner) + "b" + string(zeroWidthJoiner) + "c"},
		{"entities", "a & b < c > d " + tick + "e" + tick + " $f @g", "a &amp; b &lt; c &gt; d &#96;e&#96; &#36;f &#64;g"},
		{"backslash escapes", `a\b *c* _d_ [e] #f ~g~ h|i`, `a\\b \*c\* \_d\_ \[e\] \#f \~g\~ h\|i`},
		{"one pass", "&#96;", `&amp;\#96;`},
		{"leading dash", "- item", `\- item`},
		{"leading plus", "+ item", `\+ item`},
		{"leading equals", "=== x", `\=== x`},
		{"leading number and period", "1986. A year", `1986\. A year`},
		{"leading number and parenthesis", "2) two", `2\) two`},
		{"a number inside stays", "v1.2 is out", "v1.2 is out"},
		{"heading", "# Heading", `\# Heading`},
		{"quote", "> quoted", "&gt; quoted"},
		{"end marker", "<!-- rune:end tasks -->", "&lt;!-- rune:end tasks --&gt;"},
		{"import", "see @docs/x.md", "see &#64;docs/x.md"},
		{"command", "run !" + tick + "touch pwned" + tick, "run !&#96;touch pwned&#96;"},
		{"placeholder", "$ARGUMENTS and ${CLAUDE_SKILL_DIR}", "&#36;ARGUMENTS and &#36;{CLAUDE\\_SKILL\\_DIR}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := prose(c.in); got != c.want {
				t.Errorf("prose(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestProseCutsAtMaxText(t *testing.T) {
	// The cut falls inside the two-byte character and backs up to its start.
	in := strings.Repeat("a", MaxText-1) + "é" + "tail"
	got := prose(in)
	want := strings.Repeat("a", MaxText-1) + " [truncated]"
	if got != want {
		t.Errorf("prose cut to %d bytes ending %q, want %d bytes ending %q", len(got), got[len(got)-20:], len(want), want[len(want)-20:])
	}
	if exact := strings.Repeat("a", MaxText); prose(exact) != exact {
		t.Error("a text of exactly MaxText bytes was cut")
	}
}

func TestSummary(t *testing.T) {
	if got := summary("Build  the\napp."); got != "Build the app." {
		t.Errorf("summary = %q", got)
	}
	long := strings.Repeat("a", 99) + tick + strings.Repeat("b", 100)
	if got, want := summary(long), strings.Repeat("a", 99)+"&#96;..."; got != want {
		t.Errorf("summary cut = %q, want %q", got, want)
	}
	if exact := strings.Repeat("é", summaryRunes); summary(exact) != exact {
		t.Error("a summary of exactly the limit was cut")
	}
}

func TestValue(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain", "us east", tick + `"us east"` + tick},
		{"empty", "", tick + `""` + tick},
		{"quote and backslash", `a"b\c`, tick + `"a\"b\\c"` + tick},
		{"line break", "a\nb", tick + `"a\nb"` + tick},
		{"backtick", "a" + tick + "b", tick + `"a` + esc('`') + `b"` + tick},
		{"dollar", "$HOME", tick + `"` + esc('$') + `HOME"` + tick},
		{"comment", "<!-- x -->", tick + `"` + esc('<') + `!-- x --` + esc('>') + `"` + tick},
		{"spaces at the ends", " a ", tick + `" a "` + tick},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := value(c.in)
			if got != c.want {
				t.Fatalf("value(%q) = %s, want %s", c.in, got, c.want)
			}
			if strings.Count(got, tick) != 2 || strings.ContainsAny(got, "$<>") {
				t.Errorf("value(%q) = %s: want two backticks and no $, < or >", c.in, got)
			}
			// The escapes are Go's, so the text reads back as the value.
			back, err := strconv.Unquote(strings.Trim(got, tick))
			if err != nil || back != c.in {
				t.Errorf("value(%q) reads back as %q, %v", c.in, back, err)
			}
		})
	}
}

func TestCode(t *testing.T) {
	if got, want := code(`env("TAG", "latest")`), tick+`env("TAG", "latest")`+tick; got != want {
		t.Errorf("code = %s, want %s", got, want)
	}
	in := `"a` + tick + `$<>"`
	want := tick + `"a` + esc('`') + esc('$') + esc('<') + esc('>') + `"` + tick
	if got := code(in); got != want {
		t.Errorf("code(%s) = %s, want %s", in, got, want)
	}
}

func TestShQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{"staging", "staging"},
		{"a/b-c_1.2:x=y,z+w@h%", "a/b-c_1.2:x=y,z+w@h%"},
		{"", "''"},
		{"=ls", "'=ls'"},
		{"us east", "'us east'"},
		{"a;b", "'a;b'"},
		{"it's", `'it'\''s'`},
		{`a"b`, `'a"b'`},
		{`a\b`, `'a\b'`},
		{"*", "'*'"},
		{"#x", "'#x'"},
		{"~", "'~'"},
		{"a&b", "'a&b'"},
		{"a|b", "'a|b'"},
		{"a!", "'a!'"},
		{"café", "'café'"},
		{"''", `''\'''\'''`},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := shQuote(c.in)
			if got != c.want {
				t.Errorf("shQuote(%q) = %s, want %s", c.in, got, c.want)
			}
			if words := shellWords(t, "rune x "+got); len(words) != 3 || words[2] != c.in {
				t.Errorf("a POSIX shell reads %s as %q, want the one word %q", got, words[2:], c.in)
			}
		})
	}
}

// shellWords reads a command as a POSIX shell does and returns its words.
// Every word of the test is made of literals and single quotes only.
func shellWords(t *testing.T, cmd string) []string {
	t.Helper()
	file, err := syntax.NewParser(syntax.Variant(syntax.LangPOSIX)).Parse(strings.NewReader(cmd), "")
	if err != nil {
		t.Fatalf("%s does not parse as POSIX shell: %v", cmd, err)
	}
	if len(file.Stmts) != 1 {
		t.Fatalf("%s is %d statements, want one", cmd, len(file.Stmts))
	}
	call, ok := file.Stmts[0].Cmd.(*syntax.CallExpr)
	if !ok || file.Stmts[0].Redirs != nil {
		t.Fatalf("%s is not one plain command", cmd)
	}
	var words []string
	for _, w := range call.Args {
		var b strings.Builder
		for _, part := range w.Parts {
			switch p := part.(type) {
			case *syntax.Lit:
				// A backslash outside quotes escapes the next character.
				b.WriteString(strings.ReplaceAll(p.Value, `\'`, `'`))
			case *syntax.SglQuoted:
				b.WriteString(p.Value)
			default:
				t.Fatalf("%s has a word part %T, which a quoted value never is", cmd, part)
			}
		}
		words = append(words, b.String())
	}
	return words
}

func TestExampleSafe(t *testing.T) {
	for _, v := range []string{"staging", "us east", "it's", "a;b", "", "café", "a-b"} {
		if !exampleSafe(v) {
			t.Errorf("exampleSafe(%q) = false", v)
		}
	}
	for _, v := range []string{"a" + tick + "b", "$x", "a<b", "a>b", "a\nb", "a\tb", "a" + rightToLeft + "b", "-x", "--yes"} {
		if exampleSafe(v) {
			t.Errorf("exampleSafe(%q) = true", v)
		}
	}
}
