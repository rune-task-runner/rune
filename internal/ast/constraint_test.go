package ast

import (
	"strings"
	"testing"
)

// TestConstraintCheck pins the runtime validation rules for every constraint
// kind (spec 023 research R2): the single source of truth used by the CLI,
// dependency, and MCP invocation paths (FR-005).
func TestConstraintCheck(t *testing.T) {
	enum := &Constraint{Kind: KindEnum, KindName: "enum", Values: []string{"staging", "prod"}}
	cases := []struct {
		name    string
		c       *Constraint
		value   string
		ok      bool
		errWant string // substring the accepted-set clause must carry
	}{
		{"string accepts anything", &Constraint{Kind: KindString, KindName: "string"}, "whatever", true, ""},
		{"string accepts empty", &Constraint{Kind: KindString, KindName: "string"}, "", true, ""},
		{"number integer", &Constraint{Kind: KindNumber, KindName: "number"}, "2", true, ""},
		{"number decimal", &Constraint{Kind: KindNumber, KindName: "number"}, "1.5", true, ""},
		{"number negative", &Constraint{Kind: KindNumber, KindName: "number"}, "-3.25", true, ""},
		{"number rejects text", &Constraint{Kind: KindNumber, KindName: "number"}, "abc", false, "number"},
		{"number rejects empty", &Constraint{Kind: KindNumber, KindName: "number"}, "", false, "number"},
		{"number exponent", &Constraint{Kind: KindNumber, KindName: "number"}, "1e5", true, ""},
		// "integers and decimals" (docs/runefile.md): the extra spellings
		// strconv.ParseFloat tolerates are rejected.
		{"number rejects NaN", &Constraint{Kind: KindNumber, KindName: "number"}, "NaN", false, "number"},
		{"number rejects Inf", &Constraint{Kind: KindNumber, KindName: "number"}, "Inf", false, "number"},
		{"number rejects infinity", &Constraint{Kind: KindNumber, KindName: "number"}, "-infinity", false, "number"},
		{"number rejects hex float", &Constraint{Kind: KindNumber, KindName: "number"}, "0x1p4", false, "number"},
		{"number rejects underscores", &Constraint{Kind: KindNumber, KindName: "number"}, "1_000", false, "number"},
		{"boolean true", &Constraint{Kind: KindBoolean, KindName: "boolean"}, "true", true, ""},
		{"boolean false", &Constraint{Kind: KindBoolean, KindName: "boolean"}, "false", true, ""},
		{"boolean rejects yes", &Constraint{Kind: KindBoolean, KindName: "boolean"}, "yes", false, `"true" or "false"`},
		{"boolean rejects True", &Constraint{Kind: KindBoolean, KindName: "boolean"}, "True", false, `"true" or "false"`},
		{"path non-empty", &Constraint{Kind: KindPath, KindName: "path"}, "./dist", true, ""},
		{"path rejects empty", &Constraint{Kind: KindPath, KindName: "path"}, "", false, "path"},
		{"enum member", enum, "staging", true, ""},
		{"enum rejects non-member", enum, "production", false, `"staging", "prod"`},
		{"enum is case-sensitive", enum, "Prod", false, `"staging", "prod"`},
		{"enum rejects empty unless listed", enum, "", false, `"staging", "prod"`},
		{"nil constraint accepts anything", nil, "anything", true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.c.Check(tc.value)
			if tc.ok && err != nil {
				t.Fatalf("Check(%q) = %v, want nil", tc.value, err)
			}
			if !tc.ok {
				if err == nil {
					t.Fatalf("Check(%q) = nil, want error", tc.value)
				}
				if !strings.Contains(err.Error(), tc.errWant) {
					t.Errorf("error %q missing accepted-set clause %q", err, tc.errWant)
				}
			}
		})
	}
}

// TestConstraintKindFromName pins the closed kind-name set (contextual
// keywords, contracts/grammar.md).
func TestConstraintKindFromName(t *testing.T) {
	for name, want := range map[string]ConstraintKind{
		"string": KindString, "number": KindNumber, "boolean": KindBoolean,
		"path": KindPath, "enum": KindEnum,
	} {
		got, ok := ConstraintKindFromName(name)
		if !ok || got != want {
			t.Errorf("ConstraintKindFromName(%q) = %v, %v", name, got, ok)
		}
	}
	if _, ok := ConstraintKindFromName("bogus"); ok {
		t.Error("bogus accepted as a kind")
	}
	if _, ok := ConstraintKindFromName("String"); ok {
		t.Error("kind names must be case-sensitive")
	}
}
