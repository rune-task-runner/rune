package language

import (
	"slices"
	"testing"
)

func TestRegistryLookupAndValidity(t *testing.T) {
	if !IsValid(BuiltinFunction, "env") {
		t.Error("env should be a valid builtin function")
	}
	if !IsValid(BuiltinSetting, "working-directory") {
		t.Error("working-directory should be a valid setting")
	}
	if !IsValid(BuiltinAttribute, "parallel") {
		t.Error("parallel should be a valid attribute")
	}
	if !IsValid(BuiltinExecutor, "python") {
		t.Error("python should be a valid executor")
	}
	if IsValid(BuiltinAttribute, "nonexistent") {
		t.Error("unknown attribute should be invalid")
	}
	if b, ok := Lookup(BuiltinFunction, "os_family"); !ok || b.Signature == "" {
		t.Errorf("os_family lookup failed: %+v ok=%v", b, ok)
	}
}

func TestRegistryPrefixMatch(t *testing.T) {
	m := MatchKind(BuiltinSetting, "wor")
	if len(m) != 1 || m[0].Name != "working-directory" {
		t.Errorf("MatchKind(setting,\"wor\") = %+v, want working-directory", m)
	}
	if got := MatchKind(BuiltinExecutor, "py"); len(got) != 1 || got[0].Name != "python" {
		t.Errorf("MatchKind(executor,\"py\") = %+v, want python", got)
	}
}

// Spec 051: completion and hover read the registry, so both attributes have
// to be in it with a signature an author can copy.
func TestRegistryHasPlanAttributes(t *testing.T) {
	for name, signature := range map[string]string{
		"plan":    `[plan(file="path", summary="path")]`,
		"applies": `[applies("plan-task")]`,
	} {
		b, ok := Lookup(BuiltinAttribute, name)
		if !ok || b.Signature != signature || b.Documentation == "" {
			t.Errorf("Lookup(%q) = %+v, %v", name, b, ok)
		}
		if got := MatchKind(BuiltinAttribute, name[:2]); len(got) == 0 {
			t.Errorf("MatchKind(%q) finds nothing", name[:2])
		}
	}
}

// Spec 057: completion and hover read the registry, so both attributes have
// to be in it with the signature and documentation of the contract.
func TestRegistryHasAgentOutputAttributes(t *testing.T) {
	for name, signature := range map[string]string{
		"agent-output":  `[agent-output(head="10", tail="50")]`,
		"agent-summary": `[agent-summary]`,
	} {
		b, ok := Lookup(BuiltinAttribute, name)
		if !ok || b.Signature != signature || b.Documentation == "" {
			t.Errorf("Lookup(%q) = %+v, %v", name, b, ok)
		}
	}
	got := MatchKind(BuiltinAttribute, "agent-")
	names := make([]string, 0, len(got))
	for _, b := range got {
		names = append(names, b.Name)
	}
	for _, want := range []string{"agent-allow", "agent-output", "agent-summary"} {
		if !slices.Contains(names, want) {
			t.Errorf("MatchKind(\"agent-\") = %v, want %s among them", names, want)
		}
	}
}

// Spec 060: completion, hover and the setting check read the registry.
func TestRegistryHasReceiptsSetting(t *testing.T) {
	b, ok := Lookup(BuiltinSetting, "receipts")
	if !ok || b.Signature != "set receipts" || b.Documentation == "" {
		t.Fatalf("Lookup(receipts) = %+v, %v", b, ok)
	}
}
