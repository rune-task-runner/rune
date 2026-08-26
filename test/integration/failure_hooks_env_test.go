package integration

import (
	"strings"
	"testing"
)

// Spec 022 US3 (quickstart Scenario B): one shared diagnostic hook observes
// the correct per-failure task name and exit code in its environment.

const failHookEnvRunefile = `test: || diagnose
    @exit 2

lint: || diagnose
    @exit 5

diagnose:
    @echo "failed=$RUNE_FAILED_TASK code=$RUNE_FAILED_EXIT_CODE"
`

func TestFailureHooks_EnvContextPerFailure(t *testing.T) {
	dir := writeRunefile(t, failHookEnvRunefile)

	r := run(t, dir, nil, "test")
	if !strings.Contains(r.stdout, "failed=test code=2") {
		t.Errorf("test run: hook env wrong:\n%s", r.stdout)
	}

	r = run(t, dir, nil, "lint")
	if !strings.Contains(r.stdout, "failed=lint code=5") {
		t.Errorf("lint run: hook env wrong:\n%s", r.stdout)
	}
}
