package integration

import (
	"strings"
	"testing"
)

// Spec 023 US2 (quickstart §2): constraint violations stop the invocation
// before any command executes — exit 2, stderr naming the parameter, the
// rejected value, and the accepted set; nothing from the task body appears.
const typedEnforcementRunefile = `deploy env:enum("staging","prod") replicas:number="2":
    @echo "deploying {{env}} x{{replicas}}"

release: (deploy "nope")
    @echo "released"
`

func TestTypedEnforcement_CLIRejectsBeforeExecution(t *testing.T) {
	dir := writeRunefile(t, typedEnforcementRunefile)

	res := run(t, dir, nil, "deploy", "production")
	if res.code != 2 {
		t.Errorf("exit = %d, want 2 (usage)\nstderr: %s", res.code, res.stderr)
	}
	for _, want := range []string{"env", `"production"`, `"staging", "prod"`} {
		if !strings.Contains(res.stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, res.stderr)
		}
	}
	if strings.Contains(res.stdout+res.stderr, "deploying") {
		t.Errorf("task body ran despite the violation:\n%s", res.stdout)
	}

	// Decimals are valid numbers (clarified 2026-08-28).
	res = run(t, dir, nil, "deploy", "staging", "2.5")
	if res.code != 0 || !strings.Contains(res.stdout, "deploying staging x2.5") {
		t.Errorf("valid decimal run failed: code=%d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}

	res = run(t, dir, nil, "deploy", "staging", "abc")
	if res.code != 2 || !strings.Contains(res.stderr, "replicas") {
		t.Errorf("non-numeric replicas: code=%d stderr=%s", res.code, res.stderr)
	}
}

// Dependency parenthesized args flow through the same constraint check: the
// static analyzer cannot see the violation ("nope" is a valid expression), so
// ResolveDep rejects it at bind time, before the dependency or its dependent
// body runs.
func TestTypedEnforcement_DependencyArgsRejected(t *testing.T) {
	dir := writeRunefile(t, typedEnforcementRunefile)

	res := run(t, dir, nil, "release")
	if res.code == 0 {
		t.Fatalf("release succeeded despite invalid dependency arg\nstdout: %s", res.stdout)
	}
	for _, want := range []string{"env", `"nope"`} {
		if !strings.Contains(res.stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, res.stderr)
		}
	}
	if strings.Contains(res.stdout, "deploying") || strings.Contains(res.stdout, "released") {
		t.Errorf("bodies ran despite the violation:\n%s", res.stdout)
	}
}
