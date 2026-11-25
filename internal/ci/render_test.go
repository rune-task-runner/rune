package ci

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/rune-task-runner/rune/internal/eval"
)

// goldenCase is one golden workflow: a Runefile, roots per OS, options.
type goldenCase struct {
	src   string
	names []string
	oses  []string
	opts  Options
}

var goldenCases = map[string]goldenCase{
	"diamond": {
		src:   diamond,
		names: []string{"lint", "test"},
		oses:  []string{"linux"},
		opts:  Options{Version: "0.11.0", Branch: "main", WorkDir: ".", Command: "rune ci github --rune-version 0.11.0 --write lint test"},
	},
	"two-os": {
		src: `[linux]
apt:
    @echo apt

[macos]
brew:
    @echo brew

setup: apt brew
    @echo setup

test: setup
    @echo test
`,
		names: []string{"test"},
		oses:  []string{"linux", "darwin"},
		opts:  Options{Version: "0.11.0", Branch: "main", WorkDir: ".", Command: "rune ci github --os linux --os macos --rune-version 0.11.0 --write test"},
	},
	"outputs": {
		src: `generate:
    @echo generate

[cache(inputs=["src/**"], outputs=["dist/app"])]
build: generate
    @mkdir -p dist && echo app > dist/app

[cache(inputs=["src/**"])]
vet:
    @echo vet

test: build && report
    @cat dist/app

report:
    @echo report

lint:
    @echo lint
`,
		names: []string{"test", "vet", "lint"},
		oses:  []string{"linux"},
		opts:  Options{Version: "0.11.0-rc.1", Branch: "release", WorkDir: ".", Command: "rune ci github --branch release --rune-version 0.11.0-rc.1 --write test vet lint"},
	},
	"quoting": {
		src: `greet who mood="it's fine":
    @echo {{quote(who)}} {{quote(mood)}}

all: (greet "a b") (greet "$HOME") (greet "--flag")
    @echo all
`,
		names: []string{"all"},
		oses:  []string{"linux"},
		opts:  Options{Version: "0.11.0", Branch: "it's", WorkDir: "services/api", Command: `rune ci github --branch "it's" --rune-version 0.11.0 --output ../../.github/workflows/api.yml all`},
	},
}

func renderCase(t *testing.T, c goldenCase) []byte {
	t.Helper()
	var graphs []*Graph
	for _, goos := range c.oses {
		tasks, assigns := parse(t, c.src)
		scope := eval.NewScope(assigns, map[string]string{})
		scope.GOOS = goos
		r := &fakeResolver{tasks: tasks, scope: scope, unavailable: map[string]bool{}}
		for name, task := range tasks {
			if !task.AvailableOn(goos) {
				r.unavailable[name] = true
			}
		}
		in := Input{OS: goos, Resolver: r, Tasks: tasks, Assigns: assigns}
		for _, n := range c.names {
			in.Roots = append(in.Roots, Root{Task: tasks[n]})
		}
		g, err := Build(in)
		if err != nil {
			t.Fatal(err)
		}
		graphs = append(graphs, g)
	}
	return Render(c.opts, graphs)
}

func TestRenderGolden(t *testing.T) {
	for name, c := range goldenCases {
		t.Run(name, func(t *testing.T) {
			got := renderCase(t, c)
			if again := renderCase(t, c); !bytes.Equal(got, again) {
				t.Fatal("two renders differ")
			}
			path := filepath.Join("testdata", name+".yml")
			if *update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run with -update)", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s differs from the golden file; run with -update after checking\n%s", path, got)
			}
		})
	}
}

// workflow is the part of a workflow the invariant tests read.
type workflow struct {
	On          map[string]any    `yaml:"on"`
	Permissions map[string]string `yaml:"permissions"`
	Defaults    *struct {
		Run struct {
			WorkingDirectory string `yaml:"working-directory"`
		} `yaml:"run"`
	} `yaml:"defaults"`
	Jobs yaml.Node `yaml:"jobs"`
}

type job struct {
	Name   string   `yaml:"name"`
	RunsOn string   `yaml:"runs-on"`
	Needs  []string `yaml:"needs"`
	Steps  []step   `yaml:"steps"`
}

type step struct {
	Name  string            `yaml:"name"`
	ID    string            `yaml:"id"`
	Uses  string            `yaml:"uses"`
	Shell string            `yaml:"shell"`
	Run   string            `yaml:"run"`
	If    string            `yaml:"if"`
	With  map[string]string `yaml:"with"`
}

// goldenFiles are every golden workflow, the documentation example's
// included.
func goldenFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("testdata", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < len(goldenCases) {
		t.Fatalf("found %d golden files, want %d", len(files), len(goldenCases))
	}
	return files
}

// Spec 059 SC-006 and contracts/workflow.md: the invariants of every
// generated file.
func TestGoldenInvariants(t *testing.T) {
	for _, path := range goldenFiles(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			if !Generated(data) {
				t.Error("no marker line")
			}
			for _, bad := range []string{"secrets.", "restore-keys", "--yes", "environment:", "pull_request_target", "write-all"} {
				if strings.Contains(text, bad) {
					t.Errorf("holds %q", bad)
				}
			}
			var w workflow
			if err := yaml.Unmarshal(data, &w); err != nil {
				t.Fatalf("not YAML: %v", err)
			}
			if len(w.Permissions) != 1 || w.Permissions["contents"] != "read" {
				t.Errorf("permissions = %v", w.Permissions)
			}
			keys := slices.Sorted(func(yield func(string) bool) {
				for k := range w.On {
					if !yield(k) {
						return
					}
				}
			})
			if !slices.Equal(keys, []string{"pull_request", "push"}) {
				t.Errorf("triggers = %v", keys)
			}
			if push, ok := w.On["push"].(map[string]any); !ok || push["branches"] == nil {
				t.Errorf("push trigger = %v, want branches", w.On["push"])
			}
			var defined []string
			for i := 0; i+1 < len(w.Jobs.Content); i += 2 {
				id := w.Jobs.Content[i].Value
				var j job
				if err := w.Jobs.Content[i+1].Decode(&j); err != nil {
					t.Fatal(err)
				}
				for _, need := range j.Needs {
					if !slices.Contains(defined, need) {
						t.Errorf("%s needs %s, which is not defined before it", id, need)
					}
				}
				defined = append(defined, id)
				if j.RunsOn != "ubuntu-latest" && j.RunsOn != "macos-latest" {
					t.Errorf("%s runs on %q", id, j.RunsOn)
				}
				if len(j.Steps) == 0 || j.Steps[0].Uses != "actions/checkout@v4" || j.Steps[0].With["persist-credentials"] != "false" {
					t.Errorf("%s does not start with a checkout that keeps no credentials", id)
				}
				for _, s := range j.Steps {
					if s.Run != "" && s.Shell != "bash" {
						t.Errorf("%s: step %q runs without shell: bash", id, s.Name)
					}
					if strings.Contains(s.Run, "${{") {
						t.Errorf("%s: step %q has an expression in its script", id, s.Name)
					}
				}
			}
		})
	}
}

func TestRenderWorkingDirectory(t *testing.T) {
	c := goldenCases["diamond"]
	if strings.Contains(string(renderCase(t, c)), "defaults:") {
		t.Error("a workflow at the root has defaults")
	}
	c.opts.WorkDir = ""
	if strings.Contains(string(renderCase(t, c)), "defaults:") {
		t.Error("an empty work directory gave defaults")
	}
	c.opts.WorkDir = "sub"
	var w workflow
	if err := yaml.Unmarshal(renderCase(t, c), &w); err != nil {
		t.Fatal(err)
	}
	if w.Defaults == nil || w.Defaults.Run.WorkingDirectory != "sub" {
		t.Errorf("defaults = %+v", w.Defaults)
	}
}

func TestRenderDownloadsOnlyFromAncestors(t *testing.T) {
	var w workflow
	if err := yaml.Unmarshal(renderCase(t, goldenCases["outputs"]), &w); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"test": true, "report": true}
	for i := 0; i+1 < len(w.Jobs.Content); i += 2 {
		id := w.Jobs.Content[i].Value
		var j job
		if err := w.Jobs.Content[i+1].Decode(&j); err != nil {
			t.Fatal(err)
		}
		downloads := slices.ContainsFunc(j.Steps, func(s step) bool { return s.With["name"] == "rune-build" && s.Uses == "actions/download-artifact@v4" })
		if downloads != want[id] {
			t.Errorf("job %s downloads build's outputs: %v, want %v", id, downloads, want[id])
		}
	}
}
