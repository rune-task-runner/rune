package formatter

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/parser"
)

var update = flag.Bool("update", false, "regenerate formatter golden files")

func TestFmtGolden(t *testing.T) {
	matches, _ := filepath.Glob(filepath.Join("..", "..", "testdata", "fmt", "*.rune"))
	if len(matches) == 0 {
		t.Skip("no formatter fixtures yet")
	}
	for _, in := range matches {
		in := in
		t.Run(filepath.Base(in), func(t *testing.T) {
			src, err := os.ReadFile(in)
			if err != nil {
				t.Fatal(err)
			}
			f, diags := parser.Parse(filepath.Base(in), string(src))
			if diags.HasErrors() {
				t.Fatalf("parse: %v", diags)
			}
			got := Format(f)

			golden := strings.TrimSuffix(in, ".rune") + ".fmt"
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
				t.Errorf("format mismatch for %s:\n got:\n%s\nwant:\n%s", in, got, want)
			}
		})
	}
}

// TestFmtIdempotent verifies formatting is stable: formatting already-formatted
// output yields the same text.
func TestFmtIdempotent(t *testing.T) {
	src := "set default:=\"greet\"\nx:=\"dist\"\n# Greet.\ngreet name=\"world\":\n    @echo hi {{name}}\nbuild: greet\n    go build\n"
	f, diags := parser.Parse("Runefile", src)
	if diags.HasErrors() {
		t.Fatalf("parse: %v", diags)
	}
	once := Format(f)

	f2, diags2 := parser.Parse("Runefile", once)
	if diags2.HasErrors() {
		t.Fatalf("re-parse of formatted output failed: %v", diags2)
	}
	twice := Format(f2)
	if once != twice {
		t.Errorf("formatting is not idempotent:\nonce:\n%s\ntwice:\n%s", once, twice)
	}
}

// Spec 033 FR-015: [readonly] formats on its own line beside [network], and
// formatting the output again changes nothing.
func TestFmtReadonly(t *testing.T) {
	f, diags := parser.Parse("Runefile", "[readonly, network]\nt:\n    @echo hi\n")
	if diags.HasErrors() {
		t.Fatalf("parse: %v", diags)
	}
	once := Format(f)
	if !strings.Contains(once, "[readonly]\n[network]\nt:\n") {
		t.Errorf("want [readonly] and [network] on separate lines, got:\n%s", once)
	}
	f2, diags2 := parser.Parse("Runefile", once)
	if diags2.HasErrors() {
		t.Fatalf("re-parse of formatted output failed: %v", diags2)
	}
	if twice := Format(f2); twice != once {
		t.Errorf("formatting is not idempotent:\nonce:\n%s\ntwice:\n%s", once, twice)
	}
}

// Spec 050 research R12: [agent-allow] has one canonical form, and formatting
// never changes a predicate's value.
func TestFormatAgentAllow(t *testing.T) {
	format := func(src string) string {
		t.Helper()
		f, diags := parser.Parse("Runefile", src)
		if diags.HasErrors() {
			t.Fatalf("parse: %v", diags)
		}
		return Format(f)
	}

	once := format("[agent-allow( env = \"staging\" ,region=\"eu-west-1\" )]\ndeploy env region:\n    @echo hi\n")
	if want := "[agent-allow(env=\"staging\", region=\"eu-west-1\")]\n"; !strings.Contains(once, want) {
		t.Errorf("no %q in:\n%s", want, once)
	}
	if twice := format(once); twice != once {
		t.Errorf("formatting is not idempotent:\nonce:\n%s\ntwice:\n%s", once, twice)
	}

	split := format("[confirm, agent-allow(env=\"staging\")]\ndeploy env:\n    @echo hi\n")
	if want := "[confirm]\n[agent-allow(env=\"staging\")]\n"; !strings.Contains(split, want) {
		t.Errorf("no %q in:\n%s", want, split)
	}

	escaped := format("[agent-allow(env=\"a\\\"b\\\\c\")]\ndeploy env:\n    @echo hi\n")
	f, diags := parser.Parse("Runefile", escaped)
	if diags.HasErrors() {
		t.Fatalf("re-parse: %v", diags)
	}
	if got := f.Tasks[0].AgentPolicy(); len(got) != 1 || got[0].Value != `a"b\c` {
		t.Errorf("value after formatting = %+v, want a\"b\\c", got)
	}

	// The formatter runs without the analyzer. A value RUNE2019 rejects must
	// come back as it was, not as another value that passes.
	for _, value := range []string{"staging\u00a0", "a\u200bb", "x\x7fy", "caf\xe9", "a\tb\nc\rd\x00e"} {
		lit := strings.NewReplacer("\t", `\t`, "\n", `\n`, "\r", `\r`, "\x00", `\0`).Replace(value)
		once := format("[agent-allow(env=\"" + lit + "\")]\ndeploy env:\n    @echo hi\n")
		again, errs := parser.Parse("Runefile", once)
		if errs.HasErrors() {
			t.Fatalf("re-parse of %q: %v", once, errs)
		}
		if got := again.Tasks[0].AgentPolicy(); len(got) != 1 || got[0].Value != value {
			t.Errorf("value after formatting = %+v, want %q", got, value)
		}
		if twice := Format(again); twice != once {
			t.Errorf("formatting %q is not idempotent:\nonce:\n%s\ntwice:\n%s", value, once, twice)
		}
	}
}

// Spec 051: [plan] and [applies] have one canonical form each.
func TestFormatPlan(t *testing.T) {
	format := func(src string) string {
		t.Helper()
		f, diags := parser.Parse("Runefile", src)
		if diags.HasErrors() {
			t.Fatalf("parse: %v", diags)
		}
		return Format(f)
	}
	once := format("[plan( file = \"tfplan\" ,summary=\"tfplan.txt\" )]\nplan env:\n    @echo hi\n\n[applies( \"plan\" )]\napply env:\n    @echo $RUNE_PLAN_FILE\n")
	for _, want := range []string{"[plan(file=\"tfplan\", summary=\"tfplan.txt\")]\n", "[applies(\"plan\")]\n"} {
		if !strings.Contains(once, want) {
			t.Errorf("no %q in:\n%s", want, once)
		}
	}
	if twice := format(once); twice != once {
		t.Errorf("formatting is not idempotent:\nonce:\n%s\ntwice:\n%s", once, twice)
	}
}
