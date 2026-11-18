package integration

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// Spec 054: `rune import` writes a Runefile from the task file of another
// runner. These tests import one small project per format with the built
// binary and then run the imported tasks.

// importFixture copies testdata/import/<name> into a new directory.
func importFixture(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("testdata", "import", name))); err != nil {
		t.Fatal(err)
	}
	return dir
}

// dirSnapshot maps every file under dir to its bytes.
func dirSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		files[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// parityCase is one run of an imported task, with what the source runner
// does for the same request.
type parityCase struct {
	name string
	args []string // what follows `rune --quiet`
	cwd  string   // the subdirectory the task is run from
	// stdout is the standard output of the task. "{dir}" stands for the base
	// name of the project directory.
	stdout    string
	unordered bool     // the lines can come in any order
	code      int      // rune's exit status
	runner    []string // the same request to the source runner, for the opt-in check
	unix      bool     // the case starts a shell script or reads a Unix path
}

// The records below state what just, make, Task and npm do. Set
// RUNE_TEST_SOURCE_RUNNERS=1 to compare them with a runner that is on the
// path: the test then also runs each case's runner command.
var parityProjects = []struct {
	fixture string
	setup   func(t *testing.T, dir string)
	cases   []parityCase
}{
	{
		fixture: "justfile",
		cases: []parityCase{
			{name: "variables", args: []string{"hello"}, stdout: "hello, world\n", runner: []string{"just", "hello"}},
			{name: "cd lasts one line", args: []string{"where"}, stdout: "{dir}\n", unix: true, runner: []string{"just", "where"}},
			{name: "export lasts one line", args: []string{"state"}, stdout: "name=unset\n", runner: []string{"just", "state"}},
			{name: "stops at a failing line", args: []string{"fail"}, stdout: "before\n", code: 3, runner: []string{"just", "fail"}},
			{name: "goes on after an ignored failure", args: []string{"ignore"}, stdout: "after\n", runner: []string{"just", "ignore"}},
			{name: "a default", args: []string{"arg"}, stdout: "[none]\n", runner: []string{"just", "arg"}},
			{name: "an argument with a space", args: []string{"arg", "a b"}, stdout: "[a b]\n", runner: []string{"just", "arg", "a b"}},
			{name: "variadic arguments", args: []string{"count", "a", "b", "c"}, stdout: "[a][b][c]\n", runner: []string{"just", "count", "a", "b", "c"}},
			{name: "dependencies and hook in order", args: []string{"all"}, stdout: "first\nsecond\nall\nlast\n", runner: []string{"just", "all"}},
		},
	},
	{
		fixture: "make",
		cases: []parityCase{
			{name: "variables", args: []string{"hello"}, stdout: "hello, world\n", runner: []string{"make", "-s", "hello"}},
			{name: "a variable override", args: []string{"NAME=cli", "hello"}, stdout: "hello, cli\n", runner: []string{"make", "-s", "hello", "NAME=cli"}},
			{name: "cd lasts one line", args: []string{"where"}, stdout: "{dir}\n", unix: true, runner: []string{"make", "-s", "where"}},
			{name: "export lasts one line", args: []string{"state"}, stdout: "other=unset\n", runner: []string{"make", "-s", "state"}},
			{name: "stops at a failing line", args: []string{"fail"}, stdout: "before\n", code: 3, runner: []string{"make", "-s", "fail"}},
			{name: "goes on after an ignored failure", args: []string{"ignore"}, stdout: "after\n", runner: []string{"make", "-s", "ignore"}},
			{name: "prerequisites in order, each once", args: []string{"all"}, stdout: "first\nsecond\nall\n", runner: []string{"make", "-s", "all"}},
		},
	},
	{
		fixture: "task",
		cases: []parityCase{
			{name: "variables", args: []string{"hello"}, stdout: "hello, world\n", runner: []string{"task", "hello"}},
			{name: "cd lasts one command", args: []string{"where"}, stdout: "{dir}\n", unix: true, runner: []string{"task", "where"}},
			{name: "stops at a failing command", args: []string{"fail"}, stdout: "before\n", code: 3, runner: []string{"task", "fail"}},
			{name: "goes on after an ignored failure", args: []string{"ignore"}, stdout: "after\n", runner: []string{"task", "ignore"}},
			{name: "arguments keep their boundaries", args: []string{"args", "a", "b c"}, stdout: "[a][b c]\n", runner: []string{"task", "args", "--", "a", "b c"}},
			{name: "task calls in order", args: []string{"all"}, stdout: "first\nall\nlast\n", runner: []string{"task", "all"}},
			{name: "deps before the commands", args: []string{"both"}, stdout: "first\nlast\nboth\n", unordered: true, runner: []string{"task", "both"}},
			{name: "a prompt approved with --yes", args: []string{"--yes", "ask"}, stdout: "asked\n", runner: []string{"task", "--yes", "ask"}},
		},
	},
	{
		fixture: "npm",
		setup: func(t *testing.T, dir string) {
			// The repository ignores node_modules, so the program npm would
			// have installed is written here.
			bin := filepath.Join(dir, "node_modules", ".bin")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			script := "#!/bin/sh\necho \"mytool $# [$1] [$2]\"\n"
			if err := os.WriteFile(filepath.Join(bin, "mytool"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
		},
		cases: []parityCase{
			{
				name: "hooks, the local program and arguments", args: []string{"build", "a b", "c"}, unix: true,
				stdout: "pre prebuild\nmytool 3 [one] [a b]\npost demo 1.2.3\n",
				runner: []string{"npm", "run", "--silent", "build", "--", "a b", "c"},
			},
			{name: "no post script after a failure", args: []string{"fail"}, stdout: "before\n", code: 3, runner: []string{"npm", "run", "--silent", "fail"}},
			{name: "runs from the package directory", args: []string{"where"}, cwd: "sub", stdout: "{dir}\n", unix: true, runner: []string{"npm", "run", "--silent", "where"}},
			{name: "braces stay as they are", args: []string{"braces"}, stdout: "{{x}}\n", runner: []string{"npm", "run", "--silent", "braces"}},
			{name: "a mapped name", args: []string{"test-unit"}, stdout: "unit\n", runner: []string{"npm", "run", "--silent", "test:unit"}},
		},
	},
}

func TestImportParity(t *testing.T) {
	live := os.Getenv("RUNE_TEST_SOURCE_RUNNERS") == "1"
	for _, p := range parityProjects {
		t.Run(p.fixture, func(t *testing.T) {
			dir := importFixture(t, p.fixture)
			if p.setup != nil {
				p.setup(t, dir)
			}
			before := dirSnapshot(t, dir)

			// SC-001: a simple project imports completely.
			r := run(t, dir, nil, "import")
			if r.code != 0 || r.stdout != "" {
				t.Fatalf("rune import: exit %d, stdout %q\n%s", r.code, r.stdout, r.stderr)
			}
			if strings.Contains(r.stderr, "unsupported: ") || !strings.HasSuffix(r.stderr, "wrote Runefile\n") {
				t.Errorf("report:\n%s\nwant every line converted or review", r.stderr)
			}
			after := dirSnapshot(t, dir)
			runefile := after["Runefile"]
			delete(after, "Runefile")
			if len(after) != len(before) {
				t.Errorf("import changed the files: %d before, %d after besides the Runefile", len(before), len(after))
			}
			for name, text := range before {
				if after[name] != text {
					t.Errorf("import changed %s", name)
				}
			}
			if a := run(t, dir, nil, "analyze"); a.code != 0 {
				t.Fatalf("rune analyze: exit %d\n%s%s\n%s", a.code, a.stdout, a.stderr, runefile)
			}

			for _, c := range p.cases {
				t.Run(c.name, func(t *testing.T) {
					if c.unix && runtime.GOOS == "windows" {
						t.Skip("the case starts a shell script or reads a Unix path")
					}
					want := strings.ReplaceAll(c.stdout, "{dir}", filepath.Base(dir))
					got := run(t, filepath.Join(dir, c.cwd), nil, append([]string{"--quiet"}, c.args...)...)
					if got.code != c.code || !sameOutput(got.stdout, want, c.unordered) {
						t.Errorf("rune %v: exit %d, stdout %q\nwant exit %d, stdout %q\nstderr: %s", c.args, got.code, got.stdout, c.code, want, got.stderr)
					}
					if !live || len(c.runner) == 0 {
						return
					}
					path, err := exec.LookPath(c.runner[0])
					if err != nil {
						t.Logf("%s is not installed: its record is not checked", c.runner[0])
						return
					}
					cmd := exec.Command(path, c.runner[1:]...)
					cmd.Dir = filepath.Join(dir, c.cwd)
					var out bytes.Buffer
					cmd.Stdout = &out
					err = cmd.Run()
					if (err != nil) != (c.code != 0) || !sameOutput(out.String(), want, c.unordered) {
						t.Errorf("%v: err %v, stdout %q\nthe record says: failure %v, stdout %q", c.runner, err, out.String(), c.code != 0, want)
					}
				})
			}
		})
	}
}

// sameOutput compares two outputs, as sets of lines when the order is free.
func sameOutput(got, want string, unordered bool) bool {
	if !unordered {
		return got == want
	}
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	slices.Sort(g)
	slices.Sort(w)
	return slices.Equal(g, w)
}

func TestImportTaskfileMarkers(t *testing.T) {
	dir := importFixture(t, "task")
	if r := run(t, dir, nil, "import"); r.code != 0 {
		t.Fatalf("rune import: exit %d\n%s", r.code, r.stderr)
	}
	list := run(t, dir, nil, "--list")
	if list.code != 0 || strings.Contains(list.stdout, "helper") || !strings.Contains(list.stdout, "hello") {
		t.Errorf("--list: exit %d\n%s\nwant hello listed and the internal task hidden", list.code, list.stdout)
	}
	// Without a terminal and without --yes, a prompt declines.
	ask := run(t, dir, nil, "--quiet", "ask")
	if ask.code == 0 || ask.stdout != "" || !strings.Contains(ask.stderr, "Continue?") {
		t.Errorf("ask: exit %d, stdout %q, stderr %q; want it declined with the prompt text", ask.code, ask.stdout, ask.stderr)
	}
}

// US2: every recipe has one report line, the result is the same on every
// run, and a task that was not converted fails with the reason.
func TestImportReport(t *testing.T) {
	const want = "justfile:1: unsupported: variable version: its value runs a command (backtick), which import never runs\n" +
		"justfile:4: converted: build\n" +
		"justfile:8: unsupported: tag: it uses the variable \"version\" of line 1, which was not converted\n" +
		"justfile:12: converted: release\n" +
		"summary: 2 converted, 0 review, 2 unsupported, 0 wrappers\n" +
		"wrote Runefile\n" +
		"rune: import: 2 of 4 items were not converted\n"
	var runefiles []string
	for range 2 {
		dir := importFixture(t, "mixed")
		r := run(t, dir, nil, "import")
		if r.code != 1 || r.stderr != want || r.stdout != "" {
			t.Fatalf("rune import: exit %d, stdout %q, stderr:\n%s\nwant exit 1 and:\n%s", r.code, r.stdout, r.stderr, want)
		}
		runefiles = append(runefiles, dirSnapshot(t, dir)["Runefile"])

		if ok := run(t, dir, nil, "--quiet", "build"); ok.code != 0 || ok.stdout != "build\n" {
			t.Errorf("build: exit %d, stdout %q", ok.code, ok.stdout)
		}
		const reason = `rune import: "tag" (justfile:8) was not imported: it uses the variable "version" of line 1, which was not converted`
		for _, task := range []string{"tag", "release"} {
			r := run(t, dir, nil, "--quiet", task)
			// release runs build, then stops at tag: its own body never runs.
			if r.code != 1 || !strings.Contains(r.stderr, reason) || strings.Contains(r.stdout, "release") {
				t.Errorf("%s: exit %d, stdout %q, stderr %q; want it to fail with the reason", task, r.code, r.stdout, r.stderr)
			}
		}
	}
	if runefiles[0] != runefiles[1] || runefiles[0] == "" {
		t.Errorf("two imports of one source differ:\n%s\n---\n%s", runefiles[0], runefiles[1])
	}
}

// US3: importing runs nothing and fetches nothing. Every source below would
// create MARKER if a variable command, an include, a script or a body ran.
func TestImportIsStatic(t *testing.T) {
	dir := importFixture(t, "unsafe")
	before := dirSnapshot(t, dir)
	sources := []struct{ format, file string }{
		{"justfile", "justfile"}, {"make", "Makefile"}, {"task", "Taskfile.yml"}, {"npm", "package.json"},
	}
	for _, s := range sources {
		r := run(t, dir, nil, "import", "--from", s.format, "-o", s.format+".rune", s.file)
		if r.code != 0 && r.code != 1 {
			t.Errorf("import %s: exit %d\n%s", s.file, r.code, r.stderr)
		}
	}
	after := dirSnapshot(t, dir)
	for name, text := range before {
		if after[name] != text {
			t.Errorf("import changed %s", name)
		}
		delete(after, name)
	}
	var created []string
	for name := range after {
		created = append(created, name)
	}
	slices.Sort(created)
	if want := []string{"justfile.rune", "make.rune", "npm.rune", "task.rune"}; !slices.Equal(created, want) {
		t.Errorf("created files = %q, want only the outputs %q", created, want)
	}
}

func TestImportDryRunAndExisting(t *testing.T) {
	dir := importFixture(t, "justfile")
	before := dirSnapshot(t, dir)
	dry := run(t, dir, nil, "import", "--dry-run")
	if dry.code != 0 || !strings.HasPrefix(dry.stdout, "# Imported from justfile by rune import.\n") || !strings.HasSuffix(dry.stderr, "dry run: nothing written\n") {
		t.Errorf("--dry-run: exit %d\nstdout: %s\nstderr: %s", dry.code, dry.stdout, dry.stderr)
	}
	if after := dirSnapshot(t, dir); len(after) != len(before) {
		t.Errorf("--dry-run created a file: %d files, then %d", len(before), len(after))
	}

	if r := run(t, dir, nil, "import"); r.code != 0 {
		t.Fatalf("rune import: exit %d\n%s", r.code, r.stderr)
	}
	written := dirSnapshot(t, dir)["Runefile"]
	again := run(t, dir, nil, "import")
	const refusal = "rune: Runefile already exists; write to another file with --output <path>, or replace it with --force\n"
	if again.code != 2 || again.stderr != refusal {
		t.Errorf("second import: exit %d, stderr %q\nwant exit 2 and %q", again.code, again.stderr, refusal)
	}
	if got := dirSnapshot(t, dir)["Runefile"]; got != written {
		t.Error("the refused import changed the Runefile")
	}

	// The documented way to merge: import to another file and name it.
	if r := run(t, dir, nil, "import", "-o", "imported.rune"); r.code != 0 {
		t.Fatalf("import -o: exit %d\n%s", r.code, r.stderr)
	}
	merged := "import \"imported.rune\"\n\n# Mine.\nmine:\n    @echo mine\n"
	if err := os.WriteFile(filepath.Join(dir, "Runefile"), []byte(merged), 0o644); err != nil {
		t.Fatal(err)
	}
	list := run(t, dir, nil, "--list")
	if list.code != 0 || !strings.Contains(list.stdout, "mine") || !strings.Contains(list.stdout, "hello") {
		t.Errorf("--list of the merged Runefile: exit %d\n%s%s", list.code, list.stdout, list.stderr)
	}
}

func TestImportErrors(t *testing.T) {
	t.Run("a source that does not parse", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "justfile"), "build:\n    echo build\n\n??? what\n")
		r := run(t, dir, nil, "import")
		want := "justfile:4:1: error: this line is not a task, a variable or a setting: ??? what\n4 | ??? what\n  | ^^^^^^^^\n"
		if r.code != 3 || r.stderr != want || r.stdout != "" {
			t.Errorf("exit %d, stderr:\n%s\nwant exit 3 and:\n%s", r.code, r.stderr, want)
		}
		if _, err := os.Stat(filepath.Join(dir, "Runefile")); err == nil {
			t.Error("a Runefile was written")
		}
	})
	t.Run("several sources", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "justfile"), "a:\n    echo a\n")
		writeFile(t, filepath.Join(dir, "package.json"), `{"scripts": {"a": "echo a"}}`)
		r := run(t, dir, nil, "import")
		if want := "rune: more than one task file in .: justfile, package.json; name one: rune import <path>\n"; r.code != 2 || r.stderr != want {
			t.Errorf("exit %d, stderr %q\nwant exit 2 and %q", r.code, r.stderr, want)
		}
	})
	t.Run("an unknown format", func(t *testing.T) {
		r := run(t, t.TempDir(), nil, "import", "--from", "nope")
		if want := "rune: unknown format \"nope\" (want justfile, make, task or npm)\n"; r.code != 2 || r.stderr != want {
			t.Errorf("exit %d, stderr %q\nwant exit 2 and %q", r.code, r.stderr, want)
		}
	})
}

// US7: with --wrap an unsupported target runs through make.
func TestImportWrap(t *testing.T) {
	plain := importFixture(t, "wrap")
	if r := run(t, plain, nil, "import"); r.code != 1 || !strings.Contains(r.stderr, "Makefile:7: unsupported: gen: ") {
		t.Fatalf("import without --wrap: exit %d\n%s", r.code, r.stderr)
	}
	for line := range strings.SplitSeq(dirSnapshot(t, plain)["Runefile"], "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " @-"), "make ") {
			t.Errorf("without --wrap a task runs make: %q", line)
		}
	}

	dir := importFixture(t, "wrap")
	r := run(t, dir, nil, "import", "--wrap")
	if r.code != 0 || !strings.Contains(r.stderr, "Makefile:7: wrapper: gen: ") || !strings.Contains(r.stderr, "0 unsupported, 1 wrappers") {
		t.Fatalf("import --wrap: exit %d\n%s", r.code, r.stderr)
	}
	list := run(t, dir, nil, "--list")
	if !strings.Contains(list.stdout, `Runs "make gen" (wrapper, needs make). Count the words.`) {
		t.Errorf("--list does not say that gen is a wrapper:\n%s", list.stdout)
	}
	if _, err := exec.LookPath("make"); err != nil || runtime.GOOS == "windows" {
		t.Skip("no make to run the wrapper with")
	}
	if got := run(t, dir, nil, "--quiet", "all"); got.code != 0 || got.stdout != "gen 3\nall\n" {
		t.Errorf("all: exit %d, stdout %q, stderr %q", got.code, got.stdout, got.stderr)
	}
}

func TestImportHelpAndCompletion(t *testing.T) {
	dir := t.TempDir()
	help := run(t, dir, nil, "import", "--help")
	for _, want := range []string{"--from", "--output", "--dry-run", "--wrap", "--force", "rune import --dry-run", "not the 'import' statement"} {
		if help.code != 0 || !strings.Contains(help.stdout, want) {
			t.Errorf("rune import --help (exit %d) lacks %q", help.code, want)
		}
	}
	if root := run(t, dir, nil, "--help"); !strings.Contains(root.stdout, "import") {
		t.Errorf("rune --help does not list import:\n%s", root.stdout)
	}
	if c := run(t, dir, nil, "__complete", "imp"); c.code != 0 || !strings.Contains(c.stdout, "import") {
		t.Errorf("completion of imp: exit %d\n%s", c.code, c.stdout)
	}
	c := run(t, dir, nil, "__complete", "import", "--from", "")
	for _, format := range []string{"justfile", "make", "task", "npm"} {
		if c.code != 0 || !strings.Contains(c.stdout, format+"\n") {
			t.Errorf("completion of --from lacks %q:\n%s", format, c.stdout)
		}
	}
}
