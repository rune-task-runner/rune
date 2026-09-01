package cli

import (
	"strings"
	"testing"

	"github.com/rune-task-runner/rune/internal/ast"
	"github.com/rune-task-runner/rune/internal/eval"
	"github.com/rune-task-runner/rune/internal/parser"
)

// typedTask parses a one-task Runefile and returns the task plus a fresh scope.
func typedTask(t *testing.T, src string) (*ast.Task, *eval.Scope) {
	t.Helper()
	f, diags := parser.Parse("Runefile", src)
	if diags.HasErrors() {
		t.Fatalf("parse: %v", diags)
	}
	assigns := map[string]*ast.Assignment{}
	for _, a := range f.Assignments {
		assigns[a.Name] = a
	}
	return f.Tasks[0], eval.NewScope(assigns, nil)
}

// Spec 023 US2 / FR-005, FR-006: every binding path validates constraints
// before anything executes, with errors naming the parameter, the rejected
// value, and the accepted set.
func TestBindParamsEnforcesConstraints(t *testing.T) {
	deploy, scope := typedTask(t, "deploy env:enum(\"staging\",\"prod\") replicas:number=\"2\":\n    @echo hi\n")

	if _, err := bindParams(deploy, []string{"production"}, scope); err == nil {
		t.Fatal("out-of-enum positional accepted")
	} else {
		for _, want := range []string{"env", `"production"`, `"staging", "prod"`} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q missing %q", err, want)
			}
		}
	}

	if _, err := bindParams(deploy, []string{"staging", "abc"}, scope); err == nil {
		t.Fatal("non-numeric value accepted for number param")
	} else if !strings.Contains(err.Error(), "replicas") || !strings.Contains(err.Error(), "number") {
		t.Errorf("error %q should name replicas and the number kind", err)
	}

	// Decimals are numbers (clarified 2026-08-28); the evaluated default "2" passes.
	params, err := bindParams(deploy, []string{"staging", "2.5"}, scope)
	if err != nil {
		t.Fatalf("valid decimal rejected: %v", err)
	}
	if params["replicas"] != "2.5" {
		t.Errorf("replicas = %q", params["replicas"])
	}
	if params, err = bindParams(deploy, []string{"prod"}, scope); err != nil || params["replicas"] != "2" {
		t.Errorf("default binding: params=%v err=%v", params, err)
	}
}

// FR-008: variadic values are validated per element on the pos[i:] slice
// BEFORE the space-join collapses them into one string.
func TestBindParamsValidatesVariadicPerElement(t *testing.T) {
	sum, scope := typedTask(t, "sum +nums:number:\n    @echo {{nums}}\n")

	if _, err := bindParams(sum, []string{"1.5", "x", "3"}, scope); err == nil {
		t.Fatal("bad variadic element accepted")
	} else if !strings.Contains(err.Error(), `"x"`) || !strings.Contains(err.Error(), "nums") {
		t.Errorf("error %q should identify the offending element and parameter", err)
	}

	params, err := bindParams(sum, []string{"1", "2.5"}, scope)
	if err != nil || params["nums"] != "1 2.5" {
		t.Errorf("valid variadic: params=%v err=%v", params, err)
	}
}

func TestBindNamedParamsEnforcesConstraints(t *testing.T) {
	deploy, scope := typedTask(t, "deploy env:enum(\"staging\",\"prod\") replicas:number=\"2\":\n    @echo hi\n")

	if _, err := bindNamedParams(deploy, map[string]string{"env": "production"}, scope); err == nil {
		t.Fatal("out-of-enum named value accepted")
	} else {
		for _, want := range []string{"env", `"production"`, `"staging", "prod"`} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q missing %q", err, want)
			}
		}
	}

	params, err := bindNamedParams(deploy, map[string]string{"env": "prod"}, scope)
	if err != nil || params["replicas"] != "2" {
		t.Errorf("valid named binding: params=%v err=%v", params, err)
	}
}

// A non-literal default is beyond static analysis (RUNE2014 covers literals);
// the identical runtime check catches it at bind time.
func TestBindParamsValidatesEvaluatedDefault(t *testing.T) {
	task, scope := typedTask(t, "v := \"nope\"\nrun n:number=v:\n    @echo {{n}}\n")
	if _, err := bindParams(task, nil, scope); err == nil {
		t.Fatal("evaluated default violating the constraint accepted")
	} else if !strings.Contains(err.Error(), "n") || !strings.Contains(err.Error(), "number") {
		t.Errorf("error %q should name the parameter and kind", err)
	}
}
