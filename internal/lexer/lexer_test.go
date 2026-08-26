package lexer

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/token"
)

var update = flag.Bool("update", false, "regenerate golden token streams")

// formatTokens renders a token stream one token per line for stable comparison.
func formatTokens(toks []token.Token) string {
	var b strings.Builder
	for _, t := range toks {
		b.WriteString(t.String())
		b.WriteByte('\n')
	}
	return b.String()
}

func kinds(toks []token.Token) []token.Kind {
	out := make([]token.Kind, len(toks))
	for i, t := range toks {
		out[i] = t.Kind
	}
	return out
}

func TestLexTable(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string // expected token.String() values, EOF implied
	}{
		{
			name: "assignment",
			src:  `x := "y"` + "\n",
			want: []string{`IDENT("x")`, "ASSIGN", `STRING("y")`, "NEWLINE", "EOF"},
		},
		{
			name: "setting",
			src:  `set default := "greet"` + "\n",
			want: []string{"SET", `IDENT("default")`, "ASSIGN", `STRING("greet")`, "NEWLINE", "EOF"},
		},
		{
			name: "bare setting",
			src:  "set export\n",
			want: []string{"SET", `IDENT("export")`, "NEWLINE", "EOF"},
		},
		{
			name: "task with no-echo body",
			src:  "greet:\n    @echo hi\n",
			want: []string{`IDENT("greet")`, "COLON", "NEWLINE", "INDENT", "AT", `BODYTEXT("echo hi")`, "NEWLINE", "DEDENT", "EOF"},
		},
		{
			name: "task with dep",
			src:  "build: greet\n    echo build\n",
			want: []string{`IDENT("build")`, "COLON", `IDENT("greet")`, "NEWLINE", "INDENT", `BODYTEXT("echo build")`, "NEWLINE", "DEDENT", "EOF"},
		},
		{
			name: "doc comment then param task",
			src:  "# Say hello.\ngreet name=\"world\":\n    @echo \"hi {{name}}\"\n",
			want: []string{
				`COMMENT("Say hello.")`, "NEWLINE",
				`IDENT("greet")`, `IDENT("name")`, "EQUALS", `STRING("world")`, "COLON", "NEWLINE",
				"INDENT", "AT", `BODYTEXT("echo \"hi {{name}}\"")`, "NEWLINE", "DEDENT", "EOF",
			},
		},
		{
			name: "continue-on-error sigil",
			src:  "clean:\n    -rm -rf dist\n",
			want: []string{`IDENT("clean")`, "COLON", "NEWLINE", "INDENT", "DASH", `BODYTEXT("rm -rf dist")`, "NEWLINE", "DEDENT", "EOF"},
		},
		{
			name: "namespaced dep",
			src:  "deploy: docker::push\n    echo done\n",
			want: []string{
				`IDENT("deploy")`, "COLON", `IDENT("docker")`, "COLONCOLON", `IDENT("push")`, "NEWLINE",
				"INDENT", `BODYTEXT("echo done")`, "NEWLINE", "DEDENT", "EOF",
			},
		},
		{
			name: "operators and executor",
			src:  "analyze (python):\n    print(1)\n",
			want: []string{
				`IDENT("analyze")`, "LPAREN", `IDENT("python")`, "RPAREN", "COLON", "NEWLINE",
				"INDENT", `BODYTEXT("print(1)")`, "NEWLINE", "DEDENT", "EOF",
			},
		},
		{
			name: "expr operators",
			src:  `x := a + b / "c"` + "\n",
			want: []string{`IDENT("x")`, "ASSIGN", `IDENT("a")`, "PLUS", `IDENT("b")`, "SLASH", `STRING("c")`, "NEWLINE", "EOF"},
		},
		{
			name: "attribute line",
			src:  "[private]\nsecret:\n    echo x\n",
			want: []string{
				"LBRACK", `IDENT("private")`, "RBRACK", "NEWLINE",
				`IDENT("secret")`, "COLON", "NEWLINE", "INDENT", `BODYTEXT("echo x")`, "NEWLINE", "DEDENT", "EOF",
			},
		},
		{
			name: "blank lines between items are skipped",
			src:  "a:\n    echo a\n\n\nb:\n    echo b\n",
			want: []string{
				`IDENT("a")`, "COLON", "NEWLINE", "INDENT", `BODYTEXT("echo a")`, "NEWLINE", "DEDENT",
				`IDENT("b")`, "COLON", "NEWLINE", "INDENT", `BODYTEXT("echo b")`, "NEWLINE", "DEDENT", "EOF",
			},
		},
		{
			name: "nested body indentation preserved",
			src:  "analyze (python):\n    if x:\n        print(1)\n",
			want: []string{
				`IDENT("analyze")`, "LPAREN", `IDENT("python")`, "RPAREN", "COLON", "NEWLINE",
				"INDENT", `BODYTEXT("if x:")`, "NEWLINE", `BODYTEXT("    print(1)")`, "NEWLINE", "DEDENT", "EOF",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toks, diags := Lex("Runefile", tt.src)
			if diags.HasErrors() {
				t.Fatalf("unexpected diagnostics: %v", diags)
			}
			var got []string
			for _, tk := range toks {
				got = append(got, tk.String())
			}
			if len(got) != len(tt.want) {
				t.Fatalf("token count = %d, want %d\n got: %v\nwant: %v", len(got), len(tt.want), got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("token[%d] = %s, want %s", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestLexCRLFBodyText guards against a Windows regression: a Runefile authored
// with CRLF line endings must not leak the trailing carriage return into body
// text, which would corrupt the shell command (a literal \r) on Unix.
func TestLexCRLFBodyText(t *testing.T) {
	src := "greet:\r\n    @echo hi\r\n    echo bye\r\n"
	toks, diags := Lex("Runefile", src)
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	var bodies []string
	for _, tk := range toks {
		if tk.Kind == token.BODYTEXT {
			bodies = append(bodies, tk.Lit)
		}
	}
	want := []string{"echo hi", "echo bye"}
	if len(bodies) != len(want) {
		t.Fatalf("got %d BODYTEXT tokens %q, want %d", len(bodies), bodies, len(want))
	}
	for i, b := range bodies {
		if strings.ContainsRune(b, '\r') {
			t.Errorf("BODYTEXT[%d] contains a carriage return: %q", i, b)
		}
		if b != want[i] {
			t.Errorf("BODYTEXT[%d] = %q, want %q", i, b, want[i])
		}
	}
}

func TestLexIndentationErrors(t *testing.T) {
	// A body line mixing tabs and spaces is a located error.
	src := "greet:\n \techo hi\n"
	_, diags := Lex("Runefile", src)
	if !diags.HasErrors() {
		t.Fatal("expected an indentation diagnostic for mixed tabs and spaces")
	}
	if !strings.Contains(diags[0].Message, "inconsistent indentation") {
		t.Errorf("message = %q, want it to mention inconsistent indentation", diags[0].Message)
	}
}

func TestLexSpans(t *testing.T) {
	toks, _ := Lex("Runefile", "x := \"y\"\n")
	// First token IDENT("x") at line 1, col 1.
	if toks[0].Span.Start.Line != 1 || toks[0].Span.Start.Col != 1 {
		t.Errorf("IDENT start = %v, want 1:1", toks[0].Span.Start)
	}
	// ASSIGN starts at col 3.
	if toks[1].Kind != token.ASSIGN || toks[1].Span.Start.Col != 3 {
		t.Errorf("ASSIGN = %v at %v, want ASSIGN at col 3", toks[1].Kind, toks[1].Span.Start)
	}
}

func TestLexGolden(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join("..", "..", "testdata", "lexer", "*.rune"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Skip("no golden fixtures yet")
	}
	for _, in := range matches {
		in := in
		t.Run(filepath.Base(in), func(t *testing.T) {
			src, err := os.ReadFile(in)
			if err != nil {
				t.Fatal(err)
			}
			toks, _ := Lex(filepath.Base(in), string(src))
			got := formatTokens(toks)
			golden := strings.TrimSuffix(in, ".rune") + ".tokens"
			if *update {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("missing golden %s (run with -update): %v", golden, err)
			}
			if got != string(want) {
				t.Errorf("token stream mismatch for %s:\n got:\n%s\nwant:\n%s", in, got, want)
			}
		})
	}
}

var _ = kinds // retained for ad-hoc debugging

// TestLexFailHookOperator covers the || failure-hook operator (spec 022):
// "||" is a single PIPEPIPE token; a lone "|" stays illegal (RUNE1001).
func TestLexFailHookOperator(t *testing.T) {
	toks, diags := Lex("Runefile", "deploy: build && notify || diagnose\n    @echo hi\n")
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	var got []string
	for _, tk := range toks {
		got = append(got, tk.String())
	}
	want := []string{
		`IDENT("deploy")`, "COLON", `IDENT("build")`, "AMPAMP", `IDENT("notify")`,
		"PIPEPIPE", `IDENT("diagnose")`, "NEWLINE",
		"INDENT", "AT", `BODYTEXT("echo hi")`, "NEWLINE", "DEDENT", "EOF",
	}
	if len(got) != len(want) {
		t.Fatalf("token count = %d, want %d\n got: %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("token[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}

// TestLexLonePipeIllegal: a single "|" has no meaning and must emit RUNE1001,
// exactly as a lone "&" does.
func TestLexLonePipeIllegal(t *testing.T) {
	toks, diags := Lex("Runefile", "deploy: build | diagnose\n    @echo hi\n")
	if !diags.HasErrors() {
		t.Fatalf("expected RUNE1001 diagnostics, got none (tokens: %v)", toks)
	}
	found := false
	for i := range diags {
		if diags[i].Code == "RUNE1001" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected code RUNE1001, got %v", diags)
	}
}
