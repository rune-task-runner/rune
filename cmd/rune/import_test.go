package main

import (
	"bytes"
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/rune-task-runner/rune/internal/cli"
	"github.com/rune-task-runner/rune/internal/taskimport"
)

// execImport runs `rune import` through the root command, in dir.
func execImport(t *testing.T, dir string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	t.Chdir(dir)
	var opts cli.Options
	root := newRootCmd(&opts, "1.2.3", "abc", nil)
	addCommands(root, &opts)
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(append([]string{"import"}, args...))
	err = root.ExecuteContext(context.Background())
	return out.String(), errb.String(), err
}

func importDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	justfile := "ok:\n    echo ok\n\nbad:\n    echo {{uppercase(\"x\")}}\n"
	if err := os.WriteFile(filepath.Join(dir, "tasks.txt"), []byte(justfile), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Spec 054: `import` is a built-in command.
func TestImportCommandIsRegistered(t *testing.T) {
	var opts cli.Options
	root := newRootCmd(&opts, "1.2.3", "abc", nil)
	addCommands(root, &opts)
	if !slices.Contains(subcommandNames(root), "import") {
		t.Errorf("subcommands: %v", subcommandNames(root))
	}
	cmd := newImportCmd(&opts)
	for _, want := range []string{
		"no task, no variable command and no script is run",
		"never changed",
		"converted", "review", "unsupported", "--wrap", "--force", "--output",
		"0 when nothing is unsupported, 1 when something is", "2 for a usage error, 3 when the source cannot be parsed",
		"This is not the 'import' statement of a Runefile",
	} {
		if !strings.Contains(cmd.Long, want) {
			t.Errorf("Long lacks %q", want)
		}
	}
	for _, want := range []string{"rune import --from make ./Makefile", "rune import --dry-run", "rune import -o imported.rune", "rune import --wrap"} {
		if !strings.Contains(cmd.Example, want) {
			t.Errorf("Example lacks %q", want)
		}
	}
	for _, flag := range []string{"from", "output", "dry-run", "wrap", "force"} {
		if cmd.Flags().Lookup(flag) == nil {
			t.Errorf("no --%s flag", flag)
		}
	}
	if cmd.Flags().ShorthandLookup("o") == nil {
		t.Error("no -o shorthand for --output")
	}
}

func TestImportCommandPassesItsFlags(t *testing.T) {
	dir := importDir(t)

	// --from and the source argument: the file name tells no format.
	stdout, stderr, err := execImport(t, dir, "--from", "justfile", "--dry-run", "tasks.txt")
	if err == nil || cli.CodeFor(err) != cli.ExitTaskFail {
		t.Fatalf("err = %v, want the partial failure", err)
	}
	if !strings.Contains(stdout, "ok:\n    echo ok\n") || !strings.HasSuffix(stderr, "dry run: nothing written\n") {
		t.Errorf("stdout = %q\nstderr = %q", stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "Runefile")); err == nil {
		t.Error("--dry-run wrote a Runefile")
	}

	// --wrap and --output.
	_, stderr, err = execImport(t, dir, "--from", "justfile", "--wrap", "-o", "out.rune", "tasks.txt")
	if err != nil || !strings.Contains(stderr, "tasks.txt:4: wrapper: bad: ") || !strings.HasSuffix(stderr, "wrote out.rune\n") {
		t.Errorf("err = %v, stderr = %q", err, stderr)
	}

	// --force.
	if _, _, err = execImport(t, dir, "--from", "justfile", "--wrap", "--output", "out.rune", "tasks.txt"); cli.CodeFor(err) != cli.ExitUsage {
		t.Errorf("err = %v, want the existing output refused", err)
	}
	if _, _, err = execImport(t, dir, "--from", "justfile", "--wrap", "--output", "out.rune", "--force", "tasks.txt"); err != nil {
		t.Errorf("--force: %v", err)
	}
}

func TestImportCommandTakesOneSource(t *testing.T) {
	if _, _, err := execImport(t, importDir(t), "a", "b"); err == nil {
		t.Error("two source arguments were accepted")
	}
}

func TestImportFromCompletes(t *testing.T) {
	var opts cli.Options
	cmd := newImportCmd(&opts)
	complete, ok := cmd.GetFlagCompletionFunc("from")
	if !ok {
		t.Fatal("--from has no completion")
	}
	got, directive := complete(cmd, nil, "")
	if !slices.Equal(got, []cobra.Completion{"justfile", "make", "task", "npm"}) || directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("completion = %q, %v", got, directive)
	}
}

// A task named like a rune command is reported by the importer, which keeps
// its own list of those names. This keeps the list equal to the commands.
func TestImporterKnowsTheCommands(t *testing.T) {
	var opts cli.Options
	root := newRootCmd(&opts, "1.2.3", "abc", nil)
	addCommands(root, &opts)
	// Cobra adds help when the command runs. import is a keyword of the
	// Runefile language, so no task has that name.
	want := []string{"help"}
	for _, name := range subcommandNames(root) {
		if name != "import" {
			want = append(want, name)
		}
	}
	slices.Sort(want)
	if got := slices.Sorted(maps.Keys(taskimport.RuneCommands)); !slices.Equal(got, want) {
		t.Errorf("taskimport.RuneCommands = %q, want the commands %q", got, want)
	}
}
