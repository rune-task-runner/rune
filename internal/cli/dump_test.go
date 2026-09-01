package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/parser"
)

// Spec 020 US4: the JSON dump carries a computed `available` verdict per
// task, evaluated against the target OS, while ALL tasks stay listed
// (available:false is data, not a filter — mirrors the private field) and
// the raw OS attribute names remain in attributes.
func TestDumpAvailabilityVerdict(t *testing.T) {
	src := "" +
		"everywhere:\n    @echo e\n" +
		"[windows]\nwin-only:\n    @echo w\n" +
		"[private]\n[linux]\nsecret:\n    @echo s\n"
	f, diags := parser.Parse("Runefile", src)
	if diags.HasErrors() {
		t.Fatalf("parse: %v", diags)
	}

	byName := func(goos string) map[string]taskDTO {
		out := map[string]taskDTO{}
		for _, td := range toDTO(f, goos).Tasks {
			out[td.Name] = td
		}
		return out
	}

	linux := byName("linux")
	if len(linux) != 3 {
		t.Fatalf("dump dropped tasks: %v", linux)
	}
	if !linux["everywhere"].Available {
		t.Error("unrestricted task must dump available:true")
	}
	if linux["win-only"].Available {
		t.Error("[windows] task must dump available:false on linux")
	}
	if !linux["secret"].Available || !linux["secret"].Private {
		t.Errorf("private [linux] task on linux: want available:true private:true, got %+v", linux["secret"])
	}
	if got := linux["win-only"].Attributes; len(got) != 1 || got[0] != "windows" {
		t.Errorf("raw OS attributes must stay in attributes: %v", got)
	}

	windows := byName("windows")
	if !windows["win-only"].Available {
		t.Error("[windows] task must dump available:true on windows")
	}
	if windows["secret"].Available {
		t.Error("[linux] task must dump available:false on windows")
	}
}

// Spec 022: --dump carries failure hooks in a dedicated failHooks field.
func TestDumpFailHooks(t *testing.T) {
	src := "test: build && notify || diagnose\n    @echo t\n" +
		"build:\n    @echo b\nnotify:\n    @echo n\ndiagnose:\n    @echo d\n"
	f, diags := parser.Parse("Runefile", src)
	if diags.HasErrors() {
		t.Fatalf("parse: %v", diags)
	}
	for _, td := range toDTO(f, "linux").Tasks {
		if td.Name != "test" {
			continue
		}
		if len(td.PostHooks) != 1 || td.PostHooks[0] != "notify" {
			t.Errorf("postHooks = %v", td.PostHooks)
		}
		if len(td.FailHooks) != 1 || td.FailHooks[0] != "diagnose" {
			t.Errorf("failHooks = %v", td.FailHooks)
		}
		return
	}
	t.Fatal("task 'test' not found in dump")
}

// Spec 023: the JSON dump reports each parameter's declared constraint and
// [param-doc] description; an unannotated file's params serialize exactly as
// before (omitempty keeps the pre-023 document byte-identical, SC-003).
func TestDumpTypedParams(t *testing.T) {
	src := "[param-doc(\"env\", \"Target environment\")]\n" +
		"deploy env:enum(\"staging\",\"prod\") replicas:number=\"2\":\n    @echo hi\n" +
		"plain name=\"world\":\n    @echo {{name}}\n"
	f, diags := parser.Parse("Runefile", src)
	if diags.HasErrors() {
		t.Fatalf("parse: %v", diags)
	}
	byName := map[string]taskDTO{}
	for _, td := range toDTO(f, "linux").Tasks {
		byName[td.Name] = td
	}

	env := byName["deploy"].Params[0]
	if env.Type != "enum" || len(env.Values) != 2 || env.Values[0] != "staging" {
		t.Errorf("env param dto = %+v", env)
	}
	if env.Description != "Target environment" {
		t.Errorf("env description = %q", env.Description)
	}
	replicas := byName["deploy"].Params[1]
	if replicas.Type != "number" || replicas.Values != nil || replicas.Description != "" {
		t.Errorf("replicas dto = %+v", replicas)
	}

	name := byName["plain"].Params[0]
	if name.Type != "" || name.Values != nil || name.Description != "" {
		t.Errorf("unannotated param gained fields: %+v", name)
	}
	data, err := json.Marshal(name)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != `{"name":"name","kind":"defaulted"}` {
		t.Errorf("unannotated param JSON drifted: %s", got)
	}
}

// Spec 023 US3: [returns] appears in the machine-readable dump; absence keeps
// the pre-023 document byte-identical.
func TestDumpReturns(t *testing.T) {
	src := "[returns(\"JSON array of artifact IDs\")]\nids:\n    @echo []\nplain:\n    @echo hi\n"
	f, diags := parser.Parse("Runefile", src)
	if diags.HasErrors() {
		t.Fatalf("parse: %v", diags)
	}
	dto := toDTO(f, "linux")
	if got := dto.Tasks[0].Returns; got != "JSON array of artifact IDs" {
		t.Errorf("returns = %q", got)
	}
	data, err := json.Marshal(dto.Tasks[1])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "returns") {
		t.Errorf("unannotated task gained a returns field: %s", data)
	}
}
