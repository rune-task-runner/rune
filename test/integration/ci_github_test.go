//go:build !windows

package integration

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

// Spec 059: a generated workflow, run job by job as GitHub runs it, does what
// a local run of the same roots does. The simulation runs each job's real
// run: scripts with bash in its own copy of the project, keeps artifacts and
// caches by exact name and key, and skips the jobs that need a failed one.
// Only the install step is not run: the binary under test is on PATH.

// ciDiamondRunefile logs every body to $RUNE_CI_LOG, outside the project.
const ciDiamondRunefile = `generate:
    @echo generate >> "$RUNE_CI_LOG"

[cache(inputs=["src/**"], outputs=["dist/app"])]
build: generate
    @echo build >> "$RUNE_CI_LOG"
    @test -z "$FAIL_BUILD"
    @mkdir -p dist
    @cat src/main.txt > dist/app

lint: build
    @echo lint >> "$RUNE_CI_LOG"
    @test -f dist/app

test: build && report
    @echo test >> "$RUNE_CI_LOG"
    @cat dist/app > test-saw.txt

report:
    @echo report >> "$RUNE_CI_LOG"

unrelated:
    @echo unrelated >> "$RUNE_CI_LOG"
`

type simWorkflow struct {
	Defaults struct {
		Run struct {
			WorkingDirectory string `yaml:"working-directory"`
		} `yaml:"run"`
	} `yaml:"defaults"`
	Jobs yaml.Node `yaml:"jobs"`
}

type simJob struct {
	id    string
	Needs []string  `yaml:"needs"`
	Steps []simStep `yaml:"steps"`
}

type simStep struct {
	Name string            `yaml:"name"`
	ID   string            `yaml:"id"`
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	If   string            `yaml:"if"`
	With map[string]string `yaml:"with"`
}

// gitHub keeps what outlives a job: caches across runs, artifacts in one.
type gitHub struct {
	t       *testing.T
	project string            // the repository as checked out
	cache   map[string][]byte // by exact key, kept across runs
	env     []string
}

// simRun is one run of the workflow.
type simRun struct {
	result    map[string]string // job id -> success, failure, skipped
	artifacts map[string][]byte
	workdirs  map[string]string // job id -> its copy of the project
	output    map[string]string // job id -> what its steps printed
}

func newGitHub(t *testing.T, files map[string]string) *gitHub {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &gitHub{t: t, project: dir, cache: map[string][]byte{}}
}

// generate runs rune ci github in the repository and parses the workflow.
func (g *gitHub) generate(args ...string) (simWorkflow, []simJob) {
	g.t.Helper()
	r := run(g.t, g.project, nil, append([]string{"ci", "github", "--rune-version", "0.11.0"}, args...)...)
	if r.code != 0 {
		g.t.Fatalf("rune ci github: exit %d\n%s", r.code, r.stderr)
	}
	var w simWorkflow
	if err := yaml.Unmarshal([]byte(r.stdout), &w); err != nil {
		g.t.Fatalf("the workflow is not YAML: %v", err)
	}
	var jobs []simJob
	for i := 0; i+1 < len(w.Jobs.Content); i += 2 {
		j := simJob{id: w.Jobs.Content[i].Value}
		if err := w.Jobs.Content[i+1].Decode(&j); err != nil {
			g.t.Fatal(err)
		}
		jobs = append(jobs, j)
	}
	return w, jobs
}

// runWorkflow runs every job in file order, which is a topological order.
func (g *gitHub) runWorkflow(w simWorkflow, jobs []simJob, log string) simRun {
	g.t.Helper()
	r := simRun{result: map[string]string{}, artifacts: map[string][]byte{}, workdirs: map[string]string{}, output: map[string]string{}}
	for _, j := range jobs {
		if slices.ContainsFunc(j.Needs, func(n string) bool { return r.result[n] != "success" }) {
			r.result[j.id] = "skipped"
			continue
		}
		r.result[j.id] = g.runJob(w, j, log, &r)
	}
	return r
}

func (g *gitHub) runJob(w simWorkflow, j simJob, log string, r *simRun) string {
	t := g.t
	work := filepath.Join(t.TempDir(), "repo")
	if err := os.CopyFS(work, os.DirFS(g.project)); err != nil {
		t.Fatal(err)
	}
	r.workdirs[j.id] = work
	temp := t.TempDir()
	outputs := map[string]map[string]string{}
	expand := func(s string) string {
		s = strings.ReplaceAll(s, "${{ runner.temp }}", temp)
		return strings.ReplaceAll(s, "${{ steps.key.outputs.key }}", outputs["key"]["key"])
	}
	var printed strings.Builder
	defer func() { r.output[j.id] = printed.String() }()
	for _, s := range j.Steps {
		switch {
		case s.Uses == "actions/checkout@v4", strings.HasPrefix(s.Name, "Install rune "):
		case s.Uses == "actions/download-artifact@v4":
			data, ok := r.artifacts[s.With["name"]]
			if !ok {
				printed.WriteString("no artifact " + s.With["name"])
				return "failure"
			}
			writeSim(t, filepath.Join(expand(s.With["path"]), "bundle.tar"), data)
		case s.Uses == "actions/cache/restore@v4":
			data, hit := g.cache[expand(s.With["key"])]
			if hit {
				writeSim(t, expand(s.With["path"]), data)
			}
			outputs[s.ID] = map[string]string{"cache-hit": map[bool]string{true: "true", false: "false"}[hit]}
		case s.Uses == "actions/cache/save@v4":
			if s.If != "steps.restore.outputs.cache-hit != 'true'" {
				t.Fatalf("the save step's if: is %q", s.If)
			}
			if outputs["restore"]["cache-hit"] == "true" {
				continue
			}
			key := expand(s.With["key"])
			if _, taken := g.cache[key]; !taken {
				g.cache[key] = readSim(t, expand(s.With["path"]))
			}
		case s.Uses == "actions/upload-artifact@v4":
			r.artifacts[s.With["name"]] = readSim(t, expand(s.With["path"]))
		case s.Run != "":
			out := filepath.Join(temp, "github-output")
			writeSim(t, out, nil)
			cmd := exec.Command("bash", "--noprofile", "--norc", "-eo", "pipefail", "-c", s.Run)
			cmd.Dir = filepath.Join(work, w.Defaults.Run.WorkingDirectory)
			cmd.Env = append(os.Environ(),
				"PATH="+filepath.Dir(runeBin)+string(os.PathListSeparator)+os.Getenv("PATH"),
				"RUNNER_TEMP="+temp, "GITHUB_OUTPUT="+out, "RUNE_CI_LOG="+log)
			cmd.Env = append(cmd.Env, g.env...)
			var buf bytes.Buffer
			cmd.Stdout, cmd.Stderr = &buf, &buf
			err := cmd.Run()
			printed.Write(buf.Bytes())
			if err != nil {
				return "failure"
			}
			if s.ID != "" {
				outputs[s.ID] = map[string]string{}
				for _, line := range strings.Split(string(readSim(t, out)), "\n") {
					if k, v, ok := strings.Cut(line, "="); ok {
						outputs[s.ID][k] = v
					}
				}
			}
		default:
			t.Fatalf("job %s: a step the simulation does not know: %+v", j.id, s)
		}
	}
	return "success"
}

func writeSim(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readSim(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// logLines are the bodies a log recorded, sorted.
func logLines(t *testing.T, log string) []string {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil {
		return nil
	}
	lines := strings.Fields(string(data))
	slices.Sort(lines)
	return lines
}

func newLog(t *testing.T) string { return filepath.Join(t.TempDir(), "bodies.log") }

func TestCIGithubSimulatedRun(t *testing.T) {
	g := newGitHub(t, map[string]string{"Runefile": ciDiamondRunefile, "src/main.txt": "app v1"})
	w, jobs := g.generate("lint", "test", "unrelated")

	// Local: one run of the same roots in one copy.
	local := filepath.Join(t.TempDir(), "repo")
	if err := os.CopyFS(local, os.DirFS(g.project)); err != nil {
		t.Fatal(err)
	}
	localLog := newLog(t)
	if r := run(t, local, []string{"RUNE_CI_LOG=" + localLog}, "lint", "test", "unrelated"); r.code != 0 {
		t.Fatalf("local run: %d %s", r.code, r.stderr)
	}

	log := newLog(t)
	first := g.runWorkflow(w, jobs, log)
	for id, res := range first.result {
		if res != "success" {
			t.Errorf("job %s: %s\n%s", id, res, first.output[id])
		}
	}
	want := []string{"build", "generate", "lint", "report", "test", "unrelated"}
	if got := logLines(t, log); !slices.Equal(got, want) {
		t.Errorf("CI ran %v, want each body once: %v", got, want)
	}
	if got := logLines(t, localLog); !slices.Equal(got, want) {
		t.Errorf("the local run ran %v", got)
	}
	if got, want := string(readSim(t, filepath.Join(first.workdirs["test"], "test-saw.txt"))), string(readSim(t, filepath.Join(local, "test-saw.txt"))); got != want {
		t.Errorf("test saw %q in CI and %q locally", got, want)
	}
	if _, err := os.Stat(filepath.Join(first.workdirs["unrelated"], "dist", "app")); err == nil {
		t.Error("a job that does not depend on build received its outputs")
	}

	// A second run of the same commit: build is a cache hit, and the jobs
	// after it still receive its outputs.
	log2 := newLog(t)
	second := g.runWorkflow(w, jobs, log2)
	if got := logLines(t, log2); slices.Contains(got, "build") || !slices.Contains(got, "lint") {
		t.Errorf("second run ran %v, want no build body", got)
	}
	if !strings.Contains(second.output["build"], "cached: build") {
		t.Errorf("the build job did not log its cache hit:\n%s", second.output["build"])
	}
	if second.result["lint"] != "success" || second.result["test"] != "success" {
		t.Errorf("second run results: %v", second.result)
	}

	// A stored bundle without its declared output does not make build pass
	// unseen: the body runs again.
	for key, data := range g.cache {
		g.cache[key] = dropEntry(t, data, "dist/app")
	}
	log3 := newLog(t)
	third := g.runWorkflow(w, jobs, log3)
	if got := logLines(t, log3); !slices.Contains(got, "build") {
		t.Errorf("third run ran %v, want build again", got)
	}
	if third.result["lint"] != "success" {
		t.Errorf("third run results: %v", third.result)
	}

	// A changed input is a new key: build runs.
	writeSim(t, filepath.Join(g.project, "src", "main.txt"), []byte("app v2"))
	keys := len(g.cache)
	log4 := newLog(t)
	fourth := g.runWorkflow(w, jobs, log4)
	if got := logLines(t, log4); !slices.Contains(got, "build") || len(g.cache) != keys+1 {
		t.Errorf("after an input changed: ran %v, %d keys (was %d)", got, len(g.cache), keys)
	}
	if got := string(readSim(t, filepath.Join(fourth.workdirs["test"], "test-saw.txt"))); got != "app v2" {
		t.Errorf("test saw %q after the input changed", got)
	}
}

func TestCIGithubSimulatedFailure(t *testing.T) {
	g := newGitHub(t, map[string]string{"Runefile": ciDiamondRunefile, "src/main.txt": "app"})
	g.env = []string{"FAIL_BUILD=1"}
	w, jobs := g.generate("lint", "test", "unrelated")
	log := newLog(t)
	r := g.runWorkflow(w, jobs, log)
	want := map[string]string{"generate": "success", "build": "failure", "lint": "skipped", "test": "skipped", "report": "skipped", "unrelated": "success"}
	if !maps.Equal(r.result, want) {
		t.Errorf("results = %v, want %v", r.result, want)
	}
	if len(g.cache) != 0 {
		t.Error("a failed job saved a cache entry")
	}
}

func TestCIGithubSimulatedQuoting(t *testing.T) {
	src := `greet who:
    @printf '%s\n' {{quote(who)}} >> "$RUNE_CI_LOG"

all: (greet "it's") (greet "a b") (greet "$HOME") (greet "--flag")
`
	g := newGitHub(t, map[string]string{"Runefile": src})
	w, jobs := g.generate("all")
	log := newLog(t)
	r := g.runWorkflow(w, jobs, log)
	for id, res := range r.result {
		if res != "success" {
			t.Fatalf("job %s: %s\n%s", id, res, r.output[id])
		}
	}
	data, _ := os.ReadFile(log)
	if got, want := string(data), "it's\na b\n$HOME\n--flag\n"; got != want {
		t.Errorf("the bodies got %q, want %q", got, want)
	}
}

func TestCIGithubSimulatedSubdirectory(t *testing.T) {
	g := newGitHub(t, map[string]string{"services/api/Runefile": "hello:\n    @pwd >> \"$RUNE_CI_LOG\"\n"})
	r := run(t, filepath.Join(g.project, "services", "api"), nil, "ci", "github", "--rune-version", "0.11.0", "--output", "../../.github/workflows/api.yml", "hello")
	if r.code != 0 {
		t.Fatalf("%d %s", r.code, r.stderr)
	}
	var w simWorkflow
	data := readSim(t, filepath.Join(g.project, ".github", "workflows", "api.yml"))
	if err := yaml.Unmarshal(data, &w); err != nil {
		t.Fatal(err)
	}
	var jobs []simJob
	for i := 0; i+1 < len(w.Jobs.Content); i += 2 {
		j := simJob{id: w.Jobs.Content[i].Value}
		if err := w.Jobs.Content[i+1].Decode(&j); err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, j)
	}
	log := newLog(t)
	res := g.runWorkflow(w, jobs, log)
	if res.result["hello"] != "success" {
		t.Fatalf("hello: %s\n%s", res.result["hello"], res.output["hello"])
	}
	if got := strings.TrimSpace(string(readSim(t, log))); !strings.HasSuffix(got, filepath.Join("repo", "services", "api")) {
		t.Errorf("the body ran in %q", got)
	}
}

// Spec 059 US2 scenario 6: an interrupted node runs no failure hook and exits
// 130, as an interrupted local run does.
func TestCIRunInterrupted(t *testing.T) {
	dir := writeRunefile(t, "slow: || hook\n    @echo ready\n    @sleep 30\n\nhook:\n    @echo hook > hook-ran\n")
	cmd := exec.Command(runeBin, "ci", "run", "slow")
	cmd.Dir = dir
	out := &readyWriter{ready: make(chan struct{})}
	var errb bytes.Buffer
	cmd.Stdout = out
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-out.ready:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("the body never started; stderr=%s", errb.String())
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	code := 0
	if err := cmd.Wait(); err != nil {
		var ee *exec.ExitError
		if !asExit(err, &ee) {
			t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	if code != 130 {
		t.Errorf("exit = %d, want 130; stderr=%s", code, errb.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "hook-ran")); err == nil {
		t.Error("a failure hook ran after an interrupt")
	}
}

// dropEntry rewrites a bundle without one entry.
func dropEntry(t *testing.T, data []byte, name string) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	tw := tar.NewWriter(zw)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSuffix(h.Name, "/") == name {
			continue
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(tw, tr); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
