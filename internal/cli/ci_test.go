package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rune-task-runner/rune/internal/ci"
)

// ciDiamond has sentinel bodies: a file a body writes shows that it ran.
const ciDiamond = `generate:
    @echo g > ran-generate

[cache(inputs=["src/**"], outputs=["app.txt"])]
build: generate
    @echo app > app.txt

lint: build
    @echo l > ran-lint

test: build
    @echo t > ran-test
`

// ciProject writes files into a new directory and returns it.
func ciProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func ciOpts(dir string) (Options, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	return Options{Cwd: dir, Stdout: &out, Stderr: &errb, Version: "dev"}, &out, &errb
}

func runCIGithub(t *testing.T, dir string, flags CIGithubFlags, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	opts, out, errb := ciOpts(dir)
	err = CIGithub(opts, flags, args)
	return out.String(), errb.String(), err
}

func runCIRun(t *testing.T, dir string, flags CIRunFlags, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	opts, out, errb := ciOpts(dir)
	err = CIRun(opts, flags, args)
	return out.String(), errb.String(), err
}

var v011 = CIGithubFlags{RuneVersion: "0.11.0"}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func noSentinels(t *testing.T, dir string) {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(dir, "ran-*"))
	if len(matches) > 0 || fileExists(filepath.Join(dir, "app.txt")) {
		t.Errorf("a body ran: %v", matches)
	}
}

func TestCIGithubPrints(t *testing.T) {
	dir := ciProject(t, map[string]string{"Runefile": ciDiamond})
	stdout, stderr, err := runCIGithub(t, dir, v011, "lint", "test")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if !strings.HasPrefix(stdout, ci.Marker+"\n#   rune ci github --rune-version 0.11.0 --write lint test\n") {
		t.Errorf("header:\n%s", stdout[:min(200, len(stdout))])
	}
	if !strings.Contains(stderr, "4 jobs for 2 root tasks on linux") {
		t.Errorf("stderr = %q", stderr)
	}
	if fileExists(filepath.Join(dir, ".github")) {
		t.Error("printing wrote a file")
	}
	noSentinels(t, dir)
	again, _, _ := runCIGithub(t, dir, v011, "lint", "test")
	if again != stdout {
		t.Error("two runs printed different workflows")
	}
}

func TestCIGithubUsageErrors(t *testing.T) {
	dir := ciProject(t, map[string]string{"Runefile": ciDiamond + "\nt x:\n    @echo {{x}}\n"})
	for _, c := range []struct {
		flags CIGithubFlags
		args  []string
		want  string
	}{
		{v011, nil, "needs the tasks CI runs"},
		{v011, []string{"X=1", "lint"}, "does not take variable overrides: a job could not repeat X=1"},
		{v011, []string{"nope"}, "unknown task: nope"},
		{v011, []string{"t"}, `missing required argument "x"`},
		{CIGithubFlags{}, []string{"lint"}, "development build"},
		{CIGithubFlags{RuneVersion: "latest"}, []string{"lint"}, `"latest" is not a release version`},
		{CIGithubFlags{RuneVersion: "0.11.0+dirty"}, []string{"lint"}, "is not a release version"},
		{CIGithubFlags{RuneVersion: "0.11.0", OS: []string{"windows"}}, []string{"lint"}, "windows runners are not supported"},
		{CIGithubFlags{RuneVersion: "0.11.0", OS: []string{"plan9"}}, []string{"lint"}, `unknown OS "plan9"`},
		{CIGithubFlags{RuneVersion: "0.11.0", Branch: "a${{ x }}"}, []string{"lint"}, "--branch:"},
		{CIGithubFlags{RuneVersion: "0.11.0", Branch: "a\nb"}, []string{"lint"}, "--branch:"},
		{CIGithubFlags{RuneVersion: "0.11.0", Check: true, Write: true}, []string{"lint"}, "--check cannot be combined with --write"},
	} {
		stdout, stderr, err := runCIGithub(t, dir, c.flags, c.args...)
		if CodeFor(err) != ExitUsage || !strings.Contains(err.Error()+stderr, c.want) {
			t.Errorf("%v %v: exit %d, err %v, stderr %q; want exit 2 with %q", c.flags, c.args, CodeFor(err), err, stderr, c.want)
		}
		if stdout != "" {
			t.Errorf("%v: stdout = %q", c.args, stdout)
		}
	}
}

// A job runs rune ci run without -f, so a Runefile that discovery would not
// find in its directory is refused rather than written into a workflow.
func TestCIGithubRefusesARunefileJobsWouldNotFind(t *testing.T) {
	for _, files := range []map[string]string{
		{"tasks.rune": ciDiamond},
		{"Runefile": ciDiamond, ".runefile": ciDiamond},
	} {
		dir := ciProject(t, files)
		name := "tasks.rune"
		if _, ok := files["Runefile"]; ok {
			name = "Runefile"
		}
		opts, out, errb := ciOpts(dir)
		opts.File = filepath.Join(dir, name)
		err := CIGithub(opts, v011, []string{"lint"})
		if CodeFor(err) != ExitUsage || !strings.Contains(err.Error(), "the jobs find the Runefile in its directory by name") {
			t.Errorf("%v: exit %d, err %v, stderr %q", files, CodeFor(err), err, errb.String())
		}
		if out.Len() != 0 {
			t.Errorf("%v: stdout = %q", files, out.String())
		}
	}
}

func TestFoundInItsDir(t *testing.T) {
	dir := ciProject(t, map[string]string{"Runefile": ciDiamond, "other.rune": ciDiamond})
	if !foundInItsDir(filepath.Join(dir, "Runefile")) {
		t.Error("the Runefile discovery finds was refused")
	}
	if foundInItsDir(filepath.Join(dir, "other.rune")) {
		t.Error("a file discovery does not find was accepted")
	}
}

func TestCIGithubNoRunefile(t *testing.T) {
	_, _, err := runCIGithub(t, t.TempDir(), v011, "lint")
	if CodeFor(err) != ExitUsage {
		t.Errorf("exit %d, err %v", CodeFor(err), err)
	}
}

func TestCIGithubInvalidRunefile(t *testing.T) {
	dir := ciProject(t, map[string]string{"Runefile": "a: missing\n    @echo\n"})
	if _, _, err := runCIGithub(t, dir, v011, "a"); CodeFor(err) != ExitValidation {
		t.Errorf("exit %d, err %v", CodeFor(err), err)
	}
}

func TestCIGithubVersions(t *testing.T) {
	dir := ciProject(t, map[string]string{"Runefile": "set minimum_version := \"0.10.0\"\n\na:\n    @echo\n"})
	plain, _, err := runCIGithub(t, dir, CIGithubFlags{RuneVersion: "0.11.0"}, "a")
	if err != nil {
		t.Fatal(err)
	}
	withV, _, err := runCIGithub(t, dir, CIGithubFlags{RuneVersion: "v0.11.0"}, "a")
	if err != nil || withV != plain {
		t.Errorf("v0.11.0 printed something else: %v", err)
	}
	_, stderr, err := runCIGithub(t, dir, CIGithubFlags{RuneVersion: "0.9.0"}, "a")
	if CodeFor(err) != ExitUsage || !strings.Contains(err.Error(), "does not satisfy minimum_version 0.10.0") {
		t.Errorf("exit %d, err %v, %s", CodeFor(err), err, stderr)
	}

	// A release build pins itself; a pseudo-version does not.
	opts, out, _ := ciOpts(dir)
	opts.Version = "v0.12.0"
	if err := CIGithub(opts, CIGithubFlags{}, []string{"a"}); err != nil || !strings.Contains(out.String(), "version=0.12.0") {
		t.Errorf("a release build: %v", err)
	}
	opts.Version = "v0.12.1-0.20261125123456-abcdefabcdef"
	if err := CIGithub(opts, CIGithubFlags{}, []string{"a"}); CodeFor(err) != ExitUsage {
		t.Errorf("a pseudo-version: %v", err)
	}
}

func TestCIGithubOSes(t *testing.T) {
	dir := ciProject(t, map[string]string{"Runefile": "[windows]\nwin:\n    @echo\n\n[linux]\nnix:\n    @echo\n\na: win nix\n    @echo\n"})
	stdout, stderr, err := runCIGithub(t, dir, CIGithubFlags{RuneVersion: "0.11.0", OS: []string{"linux", "macos", "linux"}}, "a")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "3 jobs for 1 root task on linux, macos") {
		t.Errorf("stderr = %q", stderr)
	}
	for _, want := range []string{"--os linux --os macos", "nix-linux:", "a-linux:", "a-macos:", "runs-on: macos-latest"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("no %q in the workflow", want)
		}
	}
	if strings.Contains(stdout, "win:") || strings.Contains(stdout, "-- win") {
		t.Error("a task for another OS got a job")
	}

	_, stderr, err = runCIGithub(t, dir, v011, "win")
	if CodeFor(err) != ExitValidation || !strings.Contains(stderr, `task "win" is not available on linux`) {
		t.Errorf("an unavailable root: exit %d, %s", CodeFor(err), stderr)
	}
}

func TestCIGithubModulesAndParameters(t *testing.T) {
	dir := ciProject(t, map[string]string{
		"Runefile": "mod api \"api.rune\"\n\nbuild mode:\n    @echo {{mode}}\n\nlint: (build \"release\")\n    @echo\n\ntest: (build \"debug\")\n    @echo\n",
		"api.rune": "test:\n    @echo api\n",
	})
	stdout, _, err := runCIGithub(t, dir, v011, "api::test", "lint", "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"  api-test:\n    name: 'api::test'", "-- api::test\n", "build-release:", "-- build release\n", "build-debug:", "-- build debug\n"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("no %q in the workflow", want)
		}
	}
}

func TestCIGithubReadsNothingOfTheMachine(t *testing.T) {
	t.Setenv("RUNE_CI_SECRET", "s3cr3t-value-123")
	src := "tag := env(\"RUNE_CI_SECRET\")\n\nt x=tag:\n    @echo {{x}} > ran-t\n\ntop: t\n    @echo > ran-top\n"
	dir := ciProject(t, map[string]string{"Runefile": src})
	for _, root := range []string{"top", "t"} {
		stdout, stderr, err := runCIGithub(t, dir, v011, root)
		if CodeFor(err) != ExitValidation {
			t.Errorf("%s: exit %d, err %v", root, CodeFor(err), err)
		}
		if stdout != "" || !strings.Contains(stderr, "env() reads this machine") {
			t.Errorf("%s: stdout %q stderr %q", root, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "s3cr3t-value-123") {
			t.Errorf("%s: the value of the variable appeared", root)
		}
	}
	noSentinels(t, dir)
}

// Spec 059 SC-007: every refusal exits 3, names the task, and prints nothing
// on standard output.
func TestCIGithubRefusals(t *testing.T) {
	for name, c := range map[string]struct{ src, root, want string }{
		"confirm":    {"[confirm]\ndeploy:\n    @echo > ran-d\n", "deploy", `task "deploy" has [confirm]`},
		"applies":    {"[plan(file=\"p\", summary=\"s\")]\nplan:\n    @echo\n\n[applies(\"plan\")]\napply:\n    @echo $RUNE_PLAN_FILE > ran-a\n", "apply", `task "apply" has [applies]`},
		"agent":      {"review (agent):\n    look\n", "review", `task "review" runs with the agent executor`},
		"hook":       {"s:\n    @echo\n\nd: s\n    @echo\n\nt: || d\n    @echo > ran-t\n", "t", `failure hook "d" of task "t"`},
		"background": {"srv:\n    ./serve &\n\nt: srv\n    @echo > ran-t\n", "t", `task "srv" starts a background process`},
		"output":     {"[cache(inputs=[\"a\"], outputs=[\"/tmp/x\"])]\nb:\n    @echo > ran-b\n", "b", `"/tmp/x" is outside the project`},
		"machine":    {"t x:\n    @echo\n\ntop: (t datetime())\n    @echo\n", "top", "datetime() reads this machine"},
		"expression": {"t x:\n    @echo\n\ntop: (t \"${{ github.token }}\")\n    @echo\n", "top", `an argument of task "t"`},
		"forwarding": {"plus *pkgs: (need pkgs)\n    @echo > ran-p\n\nneed +pkgs:\n    @echo > ran-n\n", "plus", `task "need" gets arguments from this call that its job could not pass`},
	} {
		t.Run(name, func(t *testing.T) {
			dir := ciProject(t, map[string]string{"Runefile": c.src})
			stdout, stderr, err := runCIGithub(t, dir, v011, c.root)
			if CodeFor(err) != ExitValidation {
				t.Errorf("exit %d, err %v", CodeFor(err), err)
			}
			if stdout != "" {
				t.Errorf("stdout = %q", stdout)
			}
			if !strings.Contains(stderr, c.want) || !strings.Contains(stderr, "Runefile:") {
				t.Errorf("stderr = %q, want a diagnostic with %q", stderr, c.want)
			}
			noSentinels(t, dir)
		})
	}
}

func TestCIGithubCycle(t *testing.T) {
	dir := ciProject(t, map[string]string{"Runefile": "a: && b\n    @echo\n\nb: a\n    @echo\n"})
	_, _, err := runCIGithub(t, dir, v011, "a")
	if err == nil {
		t.Fatal("a cycle was accepted")
	}
}

func TestCIGithubWriteAndCheck(t *testing.T) {
	dir := ciProject(t, map[string]string{"Runefile": ciDiamond})
	target := filepath.Join(dir, ".github", "workflows", "rune.yml")
	write := v011
	write.Write = true
	check := v011
	check.Check = true

	_, stderr, err := runCIGithub(t, dir, check, "lint")
	if CodeFor(err) != ExitTaskFail || !strings.Contains(stderr, ".github/workflows/rune.yml: missing") {
		t.Errorf("check of a missing file: exit %d, %s", CodeFor(err), stderr)
	}

	_, stderr, err = runCIGithub(t, dir, write, "lint")
	if err != nil || !strings.Contains(stderr, ".github/workflows/rune.yml: created: 3 jobs for 1 root task on linux") {
		t.Fatalf("write: %v, %s", err, stderr)
	}
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(target, old, old); err != nil {
		t.Fatal(err)
	}
	_, stderr, err = runCIGithub(t, dir, write, "lint")
	if err != nil || !strings.Contains(stderr, "rune.yml: unchanged") {
		t.Errorf("second write: %v, %s", err, stderr)
	}
	if info, _ := os.Stat(target); !info.ModTime().Equal(old) {
		t.Error("an unchanged write touched the file")
	}
	if _, stderr, err = runCIGithub(t, dir, check, "lint"); err != nil || !strings.Contains(stderr, "rune.yml: current") {
		t.Errorf("check of a current file: %v, %s", err, stderr)
	}

	if err := os.WriteFile(filepath.Join(dir, "Runefile"), []byte(ciDiamond+"\nextra: lint\n    @echo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err = runCIGithub(t, dir, check, "lint"); err != nil {
		t.Errorf("a change to another task made the file stale: %s", stderr)
	}
	_, stderr, err = runCIGithub(t, dir, check, "extra")
	if CodeFor(err) != ExitTaskFail || !strings.Contains(stderr, "rune.yml: stale") ||
		!strings.Contains(stderr, "regenerate with: rune ci github --rune-version 0.11.0 --write extra") {
		t.Errorf("check of a stale file: exit %d, %s", CodeFor(err), stderr)
	}
	if _, stderr, err = runCIGithub(t, dir, write, "extra"); err != nil || !strings.Contains(stderr, "rune.yml: updated") {
		t.Errorf("update: %v, %s", err, stderr)
	}
}

func TestCIGithubNeverReplacesAHandWrittenFile(t *testing.T) {
	hand := "name: ci\non: push\n"
	dir := ciProject(t, map[string]string{"Runefile": ciDiamond, ".github/workflows/rune.yml": hand})
	for _, flags := range []CIGithubFlags{{RuneVersion: "0.11.0", Write: true}, {RuneVersion: "0.11.0", Check: true}} {
		_, _, err := runCIGithub(t, dir, flags, "lint")
		if CodeFor(err) != ExitUsage || !strings.Contains(err.Error(), "was not generated by rune ci github") {
			t.Errorf("%+v: exit %d, err %v", flags, CodeFor(err), err)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(dir, ".github", "workflows", "rune.yml")); string(data) != hand {
		t.Error("the hand-written file changed")
	}
}

func TestCIGithubOutputAndWorkingDirectory(t *testing.T) {
	repo := ciProject(t, map[string]string{"services/api/Runefile": ciDiamond})
	project := filepath.Join(repo, "services", "api")
	flags := v011
	flags.Output = "../../.github/workflows/api.yml"
	_, stderr, err := runCIGithub(t, project, flags, "lint")
	if err != nil {
		t.Fatalf("%v %s", err, stderr)
	}
	data, err := os.ReadFile(filepath.Join(repo, ".github", "workflows", "api.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--output ../../.github/workflows/api.yml lint", "working-directory: 'services/api'"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("no %q in the file", want)
		}
	}

	// The same command from another directory, with -f, writes the same bytes.
	opts, _, errb := ciOpts(repo)
	opts.File = filepath.Join(repo, "services", "api", "Runefile")
	flags.Output = ".github/workflows/api.yml"
	flags.Check = true
	if err := CIGithub(opts, flags, []string{"lint"}); err != nil {
		t.Errorf("the file depends on the working directory: %v\n%s", err, errb.String())
	}

	// A target in another repository cannot find the Runefile.
	other := t.TempDir()
	flags = v011
	flags.Output = filepath.Join(other, ".github", "workflows", "x.yml")
	if _, _, err := runCIGithub(t, project, flags, "lint"); CodeFor(err) != ExitUsage {
		t.Errorf("a target outside the repository: %v", err)
	}

	// A target elsewhere is taken to be in the project's own repository.
	flags.Output = "ci.yml"
	if _, _, err := runCIGithub(t, project, flags, "lint"); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(project, "ci.yml")); strings.Contains(string(data), "working-directory") {
		t.Error("a target outside .github/workflows got a working directory")
	}
}

func TestCIGithubWriteFails(t *testing.T) {
	dir := ciProject(t, map[string]string{"Runefile": ciDiamond})
	flags := v011
	flags.Write = true
	flags.write = func(string, []byte) error { return errors.New("disk full") }
	if _, _, err := runCIGithub(t, dir, flags, "lint"); CodeFor(err) != ExitTaskFail || !strings.Contains(err.Error(), "disk full") {
		t.Errorf("exit %d, err %v", CodeFor(err), err)
	}
}

func TestCIGithubTaskNote(t *testing.T) {
	dir := ciProject(t, map[string]string{"Runefile": "ci:\n    @echo\n"})
	_, stderr, _ := runCIGithub(t, dir, v011, "ci")
	if !strings.Contains(stderr, ciTaskNote) {
		t.Errorf("stderr = %q", stderr)
	}
	opts, _, errb := ciOpts(dir)
	CITaskNote(opts)
	if !strings.Contains(errb.String(), ciTaskNote) {
		t.Error("CITaskNote printed nothing")
	}
	opts, _, errb = ciOpts(t.TempDir())
	CITaskNote(opts)
	if errb.Len() != 0 {
		t.Error("a note without a task named ci")
	}
}

const ciRunfile = `setup:
    @echo s > ran-setup

notify:
    @echo n > ran-notify

top: setup && notify
    @echo t > ran-top

args a b="dflt" *rest:
    @echo "{{a}}|{{b}}|{{rest}}" > ran-args

count n:number:
    @echo {{n}}

fail: || diagnose
    @exit 3

diagnose:
    @echo "$RUNE_FAILED_TASK $RUNE_FAILED_EXIT_CODE" > ran-diagnose

[cache(inputs=["src/*"], outputs=["out.txt"])]
build:
    @echo built > out.txt
    @echo b >> ran-build
`

func TestCIRunRunsOnlyTheBody(t *testing.T) {
	dir := ciProject(t, map[string]string{"Runefile": ciRunfile})
	if _, stderr, err := runCIRun(t, dir, CIRunFlags{}, "top"); err != nil {
		t.Fatalf("%v %s", err, stderr)
	}
	if !fileExists(filepath.Join(dir, "ran-top")) {
		t.Error("the body did not run")
	}
	for _, other := range []string{"ran-setup", "ran-notify"} {
		if fileExists(filepath.Join(dir, other)) {
			t.Errorf("%s ran", other)
		}
	}
}

func TestCIRunBindsArguments(t *testing.T) {
	dir := ciProject(t, map[string]string{"Runefile": ciRunfile})
	if _, stderr, err := runCIRun(t, dir, CIRunFlags{}, "args", "-v", "b=1", "x", "y"); err != nil {
		t.Fatalf("%v %s", err, stderr)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "ran-args")); strings.TrimSpace(string(got)) != "-v|b=1|x y" {
		t.Errorf("body got %q", got)
	}
	if _, _, err := runCIRun(t, dir, CIRunFlags{}, "count", "x"); CodeFor(err) != ExitUsage {
		t.Errorf("a value the constraint refuses: exit %d", CodeFor(err))
	}
}

func TestCIRunUsage(t *testing.T) {
	dir := ciProject(t, map[string]string{"Runefile": ciRunfile})
	for _, args := range [][]string{nil, {"nope"}, {"top", "setup"}, {"X=1", "top"}} {
		if _, _, err := runCIRun(t, dir, CIRunFlags{}, args...); CodeFor(err) != ExitUsage {
			t.Errorf("%q: exit %d, err %v", args, CodeFor(err), err)
		}
	}
	if _, _, err := runCIRun(t, t.TempDir(), CIRunFlags{}, "top"); CodeFor(err) != ExitUsage {
		t.Errorf("no Runefile: %v", err)
	}
	noSentinels(t, dir)
}

func TestCIRunFailureHooks(t *testing.T) {
	dir := ciProject(t, map[string]string{"Runefile": ciRunfile})
	_, _, err := runCIRun(t, dir, CIRunFlags{}, "fail")
	if CodeFor(err) != 3 {
		t.Errorf("exit %d, want the body's 3", CodeFor(err))
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "ran-diagnose")); strings.TrimSpace(string(got)) != "fail 3" {
		t.Errorf("hook saw %q", got)
	}
}

func TestCIRunCacheHit(t *testing.T) {
	dir := ciProject(t, map[string]string{"Runefile": ciRunfile, "src/a": "1"})
	for i, want := range []string{"running: build", "cached: build"} {
		_, stderr, err := runCIRun(t, dir, CIRunFlags{}, "build")
		if err != nil || !strings.Contains(stderr, want) {
			t.Errorf("run %d: %v, %q, want %q", i+1, err, stderr, want)
		}
	}
}

var keyLine = regexp.MustCompile(`^key=rune-build-[0-9a-f]{64}\n$`)

func ciKeyOf(t *testing.T, dir, version string) string {
	t.Helper()
	opts, out, errb := ciOpts(dir)
	opts.Version = version
	if err := CIRun(opts, CIRunFlags{Key: true}, []string{"build"}); err != nil {
		t.Fatalf("%v %s", err, errb.String())
	}
	if !keyLine.MatchString(out.String()) {
		t.Fatalf("stdout = %q", out.String())
	}
	return out.String()
}

// Spec 059 SC-004: the key follows the task, its inputs and its context.
func TestCIRunKey(t *testing.T) {
	base := "import \"vars.rune\"\n\n# A comment.\n[cache(inputs=[\"src/*\"], outputs=[\"out.txt\"])]\nbuild:\n    @echo {{ver}} > out.txt\n\nother:\n    @echo o\n"
	files := map[string]string{"Runefile": base, "vars.rune": "ver := \"1\"\nunused := \"x\"\n", "src/a": "1"}
	dir := ciProject(t, files)
	key := ciKeyOf(t, dir, "0.11.0")
	if fileExists(filepath.Join(dir, "out.txt")) {
		t.Error("--key ran the body")
	}

	change := func(name, file, content string, same bool) {
		t.Helper()
		d := ciProject(t, files)
		if err := os.WriteFile(filepath.Join(d, file), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := ciKeyOf(t, d, "0.11.0"); (got == key) != same {
			t.Errorf("%s: key changed = %v, want %v", name, got != key, !same)
		}
	}
	change("body", "Runefile", strings.Replace(base, "> out.txt", ">  out.txt", 1), false)
	change("imported variable", "vars.rune", "ver := \"2\"\nunused := \"x\"\n", false)
	change("input file", "src/a", "2", false)
	change("executor", "Runefile", strings.Replace(base, "build:", "build (python):", 1), false)
	change("comment", "Runefile", strings.Replace(base, "# A comment.", "# Another.", 1), true)
	change("other task", "Runefile", strings.Replace(base, "@echo o", "@echo p", 1), true)
	change("unused variable", "vars.rune", "ver := \"1\"\nunused := \"y\"\n", true)
	if ciKeyOf(t, dir, "0.12.0") == key {
		t.Error("the Rune version did not change the key")
	}
}

func TestCIRunKeyErrors(t *testing.T) {
	dir := ciProject(t, map[string]string{"Runefile": ciRunfile})
	if _, _, err := runCIRun(t, dir, CIRunFlags{Key: true}, "top"); CodeFor(err) != ExitUsage || !strings.Contains(err.Error(), "--key needs a task with [cache]") {
		t.Errorf("a task without [cache]: exit %d, %v", CodeFor(err), err)
	}
	noSentinels(t, dir)
	if runtime.GOOS == "windows" {
		return
	}
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", filepath.Join(dir, "src", "broken")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runCIRun(t, dir, CIRunFlags{Key: true}, "build"); CodeFor(err) != ExitTaskFail || !strings.Contains(err.Error(), "cache fingerprint") {
		t.Errorf("an input that cannot be read: exit %d, %v", CodeFor(err), err)
	}
}

func TestCIRunBundles(t *testing.T) {
	dir := ciProject(t, map[string]string{"Runefile": ciRunfile, "src/a": "1"})
	state := t.TempDir()
	if _, stderr, err := runCIRun(t, dir, CIRunFlags{Dir: state}, "build"); err != nil {
		t.Fatalf("%v %s", err, stderr)
	}
	bundle := filepath.Join(state, ci.BundleFile)
	entries := bundleEntries(t, bundle)
	if strings.Join(entries, ",") != "out.txt,.rune/cache/build.json" {
		t.Errorf("bundle holds %v", entries)
	}

	// A later job gets the outputs before its body; its own restored
	// bundle makes the [cache] task a hit.
	next := ciProject(t, map[string]string{"Runefile": ciRunfile, "src/a": "1"})
	nextState := t.TempDir()
	in := filepath.Join(nextState, "in", "0001-build")
	if err := os.MkdirAll(in, 0o755); err != nil {
		t.Fatal(err)
	}
	copyFile(t, bundle, filepath.Join(in, ci.BundleFile))
	if _, _, err := runCIRun(t, next, CIRunFlags{Dir: nextState}, "top"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(next, "out.txt")); strings.TrimSpace(string(got)) != "built" {
		t.Errorf("out.txt = %q", got)
	}
	if fileExists(filepath.Join(nextState, ci.BundleFile)) {
		t.Error("a task without [cache] wrote a bundle")
	}

	again := ciProject(t, map[string]string{"Runefile": ciRunfile, "src/a": "1"})
	againState := t.TempDir()
	copyFile(t, bundle, filepath.Join(againState, ci.BundleFile))
	_, stderr, err := runCIRun(t, again, CIRunFlags{Dir: againState}, "build")
	if err != nil || !strings.Contains(stderr, "cached: build") || fileExists(filepath.Join(again, "ran-build")) {
		t.Errorf("a restored bundle: %v, %q", err, stderr)
	}
}

func TestCIRunNoBundleAfterAFailure(t *testing.T) {
	src := "[cache(inputs=[\"src/*\"], outputs=[\"out.txt\"])]\nbuild:\n    @echo x > out.txt\n    @exit 1\n"
	dir := ciProject(t, map[string]string{"Runefile": src, "src/a": "1"})
	state := t.TempDir()
	if _, _, err := runCIRun(t, dir, CIRunFlags{Dir: state}, "build"); err == nil {
		t.Fatal("the body did not fail")
	}
	if fileExists(filepath.Join(state, ci.BundleFile)) {
		t.Error("a failed body wrote a bundle")
	}
}

func TestCIRunRefusesUnsafeBundles(t *testing.T) {
	for name, data := range map[string][]byte{
		"dotdot":  rawBundle(t, "../escape"),
		"corrupt": []byte("not a bundle"),
	} {
		dir := ciProject(t, map[string]string{"Runefile": ciRunfile})
		state := t.TempDir()
		in := filepath.Join(state, "in", "0001-x")
		if err := os.MkdirAll(in, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(in, ci.BundleFile), data, 0o644); err != nil {
			t.Fatal(err)
		}
		_, _, err := runCIRun(t, dir, CIRunFlags{Dir: state}, "top")
		if CodeFor(err) != ExitTaskFail {
			t.Errorf("%s: exit %d, err %v", name, CodeFor(err), err)
		}
		if fileExists(filepath.Join(dir, "ran-top")) {
			t.Errorf("%s: the body ran", name)
		}
	}
}

func TestCIRunRefusesUnsafeOutputs(t *testing.T) {
	src := "[cache(inputs=[\"a\"], outputs=[out])]\nbuild out=\"../x\":\n    @echo\n"
	dir := ciProject(t, map[string]string{"Runefile": src, "a": "1"})
	_, _, err := runCIRun(t, dir, CIRunFlags{Dir: t.TempDir()}, "build")
	if CodeFor(err) != ExitTaskFail || !strings.Contains(err.Error(), "outside the project") {
		t.Errorf("exit %d, %v", CodeFor(err), err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	src = "[cache(inputs=[\"a\"], outputs=[\"dist\"])]\nbuild:\n    @echo\n"
	dir = ciProject(t, map[string]string{"Runefile": src, "a": "1", "dist/real": "r"})
	if err := os.Symlink("real", filepath.Join(dir, "dist", "link")); err != nil {
		t.Fatal(err)
	}
	_, _, err = runCIRun(t, dir, CIRunFlags{Dir: t.TempDir()}, "build")
	if CodeFor(err) != ExitTaskFail || !strings.Contains(err.Error(), "dist/link is a link") {
		t.Errorf("an output holding a link: exit %d, %v", CodeFor(err), err)
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func bundleEntries(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	var names []string
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		names = append(names, h.Name)
	}
	return names
}

func rawBundle(t *testing.T, name string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestCIGithubRegenerateCommand(t *testing.T) {
	dir := ciProject(t, map[string]string{"Runefile": "t x:\n    @echo {{x}}\n"})
	opts, _, errb := ciOpts(t.TempDir())
	opts.File = filepath.Join(dir, "Runefile")
	flags := CIGithubFlags{RuneVersion: "0.11.0", Branch: "trunk", OS: []string{"macos"}, Output: filepath.Join(dir, "wf.yml"), Check: true}
	if err := CIGithub(opts, flags, []string{"t", "a b"}); CodeFor(err) != ExitTaskFail {
		t.Fatalf("check of a missing file: %v", err)
	}
	want := "regenerate with: rune ci github -f " + filepath.Join(dir, "Runefile") + " --os macos --branch trunk --rune-version 0.11.0 --output " + filepath.Join(dir, "wf.yml") + ` t "a b"`
	if !strings.Contains(errb.String(), want) {
		t.Errorf("stderr = %q, want %q", errb.String(), want)
	}
}

func TestCIGithubRegenerateCommandEndsFlagsBeforeADashArgument(t *testing.T) {
	dir := ciProject(t, map[string]string{"Runefile": "t x:\n    @echo {{x}}\n"})
	stdout, _, err := runCIGithub(t, dir, v011, "t", "-race")
	if err != nil {
		t.Fatal(err)
	}
	if want := "#   rune ci github --rune-version 0.11.0 --write -- t -race\n"; !strings.Contains(stdout, want) {
		t.Errorf("header lacks %q:\n%s", want, stdout)
	}
	stdout, _, err = runCIGithub(t, dir, v011, "t", "race")
	if err != nil {
		t.Fatal(err)
	}
	if want := "#   rune ci github --rune-version 0.11.0 --write t race\n"; !strings.Contains(stdout, want) {
		t.Errorf("header lacks %q:\n%s", want, stdout)
	}
}

func TestCIGithubRefusesAnOutputPathWithALineBreak(t *testing.T) {
	dir := ciProject(t, map[string]string{"Runefile": "t:\n    @echo\n"})
	flags := v011
	flags.Output = "wf\nname: x.yml"
	_, _, err := runCIGithub(t, dir, flags, "t")
	if CodeFor(err) != ExitUsage {
		t.Fatalf("exit %d, err %v", CodeFor(err), err)
	}
	if fileExists(filepath.Join(dir, "wf\nname: x.yml")) {
		t.Error("the file was written")
	}
}
