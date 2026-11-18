//go:build unix

package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// attempt is a body line that tries something with the shell and prints
// "wrote" or "denied", so that a failed redirection, whose message goes to
// standard error, still gives one line of output.
func attempt(command string) string {
	return "    @(" + command + ") 2>/dev/null && echo wrote || echo denied\n"
}

// tamperRunefile is a project whose task tries to change everything that
// decides what an agent may do.
func tamperRunefile() string {
	return "set dotenv := \".env\"\n" +
		"import \"sub/tasks.rune\"\n\n" +
		"# Try to change what decides the rules.\n" +
		"tamper audit:\n" +
		attempt("echo x >> Runefile") +
		attempt("rm Runefile") +
		attempt(": > scratch && mv scratch Runefile") +
		attempt("mv sub sub-moved") +
		attempt("echo x >> sub/tasks.rune") +
		attempt("echo x > .git/planted") +
		attempt("echo x > .rune/planted") +
		attempt("echo x >> .rune/plans/kept/record.json") +
		attempt("echo x >> .env") +
		attempt("ln Runefile runefile-copy") +
		attempt("echo x > {{audit}}/planted") +
		attempt("echo x > /run/planted") +
		attempt("echo x > /var/tmp/planted") +
		"    @./probe ls {{audit}}\n" +
		"    @git status --short >/dev/null && echo git-reads\n"
}

// tamperProject is a git repository with a Runefile, an imported file, a
// dotenv file and a stored plan, each of which a task must not change.
func tamperProject(t *testing.T) *sandboxFixture {
	t.Helper()
	f := sandboxProject(t, tamperRunefile())
	f.write(t, "sub/tasks.rune", "# Imported.\nimported:\n    @echo imported\n")
	f.write(t, ".env", "FROM_DOTENV=1\n")
	f.write(t, ".rune/plans/kept/record.json", `{"id":"kept"}`+"\n")
	if out, err := exec.Command("git", "-C", f.dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	// The log holds a day file once a call was recorded, so an audit
	// directory that lists as empty is hidden and not merely new.
	if err := os.MkdirAll(f.audit, 0o700); err != nil {
		t.Fatal(err)
	}
	return f
}

// protectedDigest is a digest of every protected path of a tamper project.
func protectedDigest(t *testing.T, f *sandboxFixture) string {
	t.Helper()
	var b strings.Builder
	for _, dir := range []string{".git", ".rune/plans", "sub"} {
		b.WriteString(treeDigest(t, filepath.Join(f.dir, dir)))
	}
	for _, file := range []string{"Runefile", ".env"} {
		data, err := os.ReadFile(filepath.Join(f.dir, file))
		if err != nil {
			t.Fatal(err)
		}
		b.Write(data)
	}
	return b.String()
}

// Quickstart F: a body reads the files that decide its rights and cannot
// change, remove, rename or replace them. The project is reached directly,
// through a symbolic link, and with --file through that link.
func TestSandboxRuleProtectedPaths(t *testing.T) {
	requireSandbox(t)
	starts := map[string]func(t *testing.T, f *sandboxFixture) (*mcp.ClientSession, *syncBuffer){
		"directly": func(t *testing.T, f *sandboxFixture) (*mcp.ClientSession, *syncBuffer) {
			cs, stderr, _ := f.ruleServer(t, nil)
			return cs, stderr
		},
		"through a symbolic link": func(t *testing.T, f *sandboxFixture) (*mcp.ClientSession, *syncBuffer) {
			link := filepath.Join(f.root, "linked-project")
			if err := os.Symlink(f.dir, link); err != nil {
				t.Fatal(err)
			}
			real := f.dir
			f.dir = link
			cs, stderr, _ := f.ruleServer(t, nil)
			f.dir = real
			return cs, stderr
		},
		"with --file through a symbolic link": func(t *testing.T, f *sandboxFixture) (*mcp.ClientSession, *syncBuffer) {
			link := filepath.Join(f.root, "linked-project")
			if err := os.Symlink(f.dir, link); err != nil {
				t.Fatal(err)
			}
			real := f.dir
			f.dir = f.root
			cs, stderr, _ := f.ruleServer(t, nil, "--file", filepath.Join(link, "Runefile"))
			f.dir = real
			return cs, stderr
		},
	}
	for name, start := range starts {
		t.Run(name, func(t *testing.T) {
			f := tamperProject(t)
			before := protectedDigest(t, f)
			cs, stderr := start(t, f)
			res := callTask(t, cs, "tamper", map[string]any{"audit": f.audit})
			want := []string{
				"denied", "denied", "denied", "denied", "denied", "denied", "denied", "denied", "denied", "denied",
				"denied", "denied", "denied",
				"ok ", // the audit directory lists as empty
				"git-reads",
			}
			wantLines(t, "tamper", res, want...)
			if lines := outLines(res.stdout); len(lines) == len(want) && lines[13] != "ok " {
				t.Errorf("the audit directory is not empty for a body: %q", lines[13])
			}
			if after := protectedDigest(t, f); after != before {
				t.Error("a protected path changed")
			}
			for _, planted := range []string{"runefile-copy", "sub-moved"} {
				if hasMarker(f.dir, planted) {
					t.Errorf("%s exists", planted)
				}
			}
			if _, err := os.Stat(filepath.Join(f.audit, "planted")); err == nil {
				t.Error("the body wrote into the audit directory")
			}
			if strings.Contains(stderr.String(), "reloaded Runefile") || strings.Contains(stderr.String(), "reload failed") {
				t.Errorf("the server reloaded:\n%s", stderr.String())
			}
		})
	}
}

// Quickstart R: a protected file that a mount cannot protect is a refusal
// that names it, before anything runs.
func TestSandboxRefusesLinkedRunefile(t *testing.T) {
	requireSandbox(t)
	f := sandboxProject(t, "# Build.\nbuild:\n    @: > build.ran\n")
	runefile := filepath.Join(f.dir, "Runefile")
	if err := os.Link(runefile, filepath.Join(f.dir, "second-name")); err != nil {
		t.Fatal(err)
	}
	cs, _, _ := f.server(t, nil)
	res := callTask(t, cs, "build", nil)
	want := `task "build" was not run: the sandbox cannot protect ` + runefile + `: it has 2 names (hard links), and a task could change it through another one. Remove the other names, or replace the file with a copy of itself.`
	if !res.isError || !strings.Contains(res.text, want) || hasMarker(f.dir, "build.ran") {
		t.Errorf("isError %v, text %q, want %q in it", res.isError, res.text, want)
	}
}

func TestSandboxRefusesSymlinkedImport(t *testing.T) {
	requireSandbox(t)
	f := sandboxProject(t, "import \"sub/tasks.rune\"\n\n# Build.\nbuild:\n    @: > build.ran\n")
	f.write(t, "real/tasks.rune", "# Imported.\nimported:\n    @echo imported\n")
	link := filepath.Join(f.dir, "sub")
	if err := os.Symlink(filepath.Join(f.dir, "real"), link); err != nil {
		t.Fatal(err)
	}
	cs, _, _ := f.server(t, nil)
	res := callTask(t, cs, "build", nil)
	want := `task "build" was not run: the sandbox cannot protect ` + filepath.Join(link, "tasks.rune") + `: it is reached through the symbolic link ` + link + `, which a task could replace. Use the real path.`
	if !res.isError || !strings.Contains(res.text, want) || hasMarker(f.dir, "build.ran") {
		t.Errorf("isError %v, text %q, want %q in it", res.isError, res.text, want)
	}
}

func TestSandboxRefusesMissingFile(t *testing.T) {
	requireSandbox(t)
	const remedy = `: the Runefile looks for it and it does not exist, so a task could create it. Create the file, or change the Runefile so that it is not looked for (give a module its path, or remove the optional import).`
	t.Run("an optional import", func(t *testing.T) {
		f := sandboxProject(t, "import? \"local.rune\"\n\n# Build.\nbuild:\n    @: > build.ran\n")
		cs, _, _ := f.server(t, nil)
		res := callTask(t, cs, "build", nil)
		want := `task "build" was not run: the sandbox cannot protect ` + filepath.Join(f.dir, "local.rune") + remedy
		if !res.isError || !strings.Contains(res.text, want) || hasMarker(f.dir, "build.ran") {
			t.Errorf("isError %v, text %q, want %q in it", res.isError, res.text, want)
		}
	})
	t.Run("a module found in a directory", func(t *testing.T) {
		f := sandboxProject(t, "mod api\n\n# Build.\nbuild:\n    @: > build.ran\n")
		f.write(t, "api/Runefile", "# Serve.\nserve:\n    @echo serving\n")
		cs, _, _ := f.server(t, nil)
		res := callTask(t, cs, "build", nil)
		want := `task "build" was not run: the sandbox cannot protect ` + filepath.Join(f.dir, "api.rune") + remedy
		if !res.isError || !strings.Contains(res.text, want) || hasMarker(f.dir, "build.ran") {
			t.Errorf("isError %v, text %q, want %q in it", res.isError, res.text, want)
		}
	})
	t.Run("a module with its path", func(t *testing.T) {
		f := sandboxProject(t, "mod api \"api/Runefile\"\n\n# Build.\nbuild:\n    @: > build.ran\n")
		f.write(t, "api/Runefile", "# Serve.\nserve:\n    @echo serving\n")
		cs, _, _ := f.server(t, nil)
		if res := callTask(t, cs, "build", nil); res.isError || !hasMarker(f.dir, "build.ran") {
			t.Errorf("a module with an explicit path was refused: %+v", res)
		}
	})
}

// Quickstart X: a repository a task prepared cannot make Rune run a command
// outside a sandbox. Rune asks git for the state of the repository on every
// call, and git runs what the repository's configuration names.
func TestSandboxRuleGitCannotEscape(t *testing.T) {
	requireSandbox(t)
	runefile := "# Prepare a repository whose configuration names a command.\n" +
		"plant marker:\n" +
		"    @git init -q .\n" +
		"    @git config filter.x.clean 'touch {{marker}}; cat'\n" +
		"    @echo '* filter=x' > .gitattributes\n" +
		"    @echo data > tracked.txt\n" +
		"    @git add tracked.txt\n" +
		"    @echo changed-content > tracked.txt\n\n" +
		"# Do nothing.\nfine:\n    @echo fine\n"
	f := sandboxProject(t, runefile)
	marker := filepath.Join(f.outside, "git-filter-ran")
	cs, _, _ := f.ruleServer(t, nil)
	if res := callTask(t, cs, "plant", map[string]any{"marker": marker}); res.exit != 0 {
		t.Fatalf("plant: %+v", res)
	}
	if res := callTask(t, cs, "fine", nil); res.exit != 0 {
		t.Fatalf("fine: %+v", res)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the repository's filter ran outside the sandbox")
	}
	call := lastLogCall(t, f.dir, f.logEnv())
	if call.Project == nil || call.Project.Git == nil || call.Project.Git.State != "unborn" {
		t.Errorf("the record's git state: %+v", call.Project)
	}
	if call.Project != nil && call.Project.Git != nil && (call.Project.Git.Dirty == nil || !*call.Project.Git.Dirty) {
		t.Errorf("git did not read the changed file: %+v", call.Project.Git)
	}
}

// Quickstart E: every way a task runs code is confined.
func TestSandboxRuleEveryExecutor(t *testing.T) {
	requireSandbox(t)
	main := "# Shell redirection.\nredirect outside:\n" + attempt("echo x > {{outside}}/redirect") +
		"\n# The shell's own cd.\nbuiltin outside:\n" + attempt("cd {{outside}} && echo x > builtin") +
		"\n# An interpreter.\n[script(\"sh\")]\nscript outside:\n    (echo x > {{outside}}/script) 2>/dev/null && echo wrote || echo denied\n" +
		"\n# A child process.\nchild outside:\n" + attempt("sh -c 'echo x > {{outside}}/child'")
	override := "set shell := [\"sh\", \"-eu\"]\n\n# A shell override.\noverride outside:\n    (echo x > {{outside}}/override) 2>/dev/null && echo wrote || echo denied\n"
	cases := []struct{ task, runefile string }{
		{"redirect", main}, {"builtin", main}, {"script", main}, {"child", main}, {"override", override},
	}
	for _, c := range cases {
		t.Run(c.task, func(t *testing.T) {
			f := sandboxProject(t, c.runefile)
			before := treeDigest(t, f.outside)
			cs, _, _ := f.ruleServer(t, nil)
			res := callTask(t, cs, c.task, map[string]any{"outside": f.outside})
			if res.stdout != "denied\n" {
				t.Errorf("stdout %q, stderr %q", res.stdout, res.stderr)
			}
			if treeDigest(t, f.outside) != before {
				t.Error("the directory outside the project changed")
			}
		})
	}
}

const perTaskRunefile = `# Reach the network.
[network]
fetch addr:
    @./probe connect {{addr}}

# Depend on a task with the network.
plain addr: (fetch addr)
    -@./probe connect {{addr}}

# Have the network, and a hook without it.
[network]
hooked addr: && (after addr)
    @./probe connect {{addr}}

after addr:
    -@./probe connect {{addr}}

# Have the network, fail, and be diagnosed without it.
[network]
failing addr: || (rescue addr)
    @./probe connect {{addr}}
    @exit 1

rescue addr:
    -@./probe connect {{addr}}

# Try without the network.
try addr:
    -@./probe connect {{addr}}

# Start Rune for a task with the network.
nested rune addr:
    -@{{rune}} fetch {{addr}}
`

// Quickstart G: [network] gives the network to the body of the task that
// carries it, not to the tasks it depends on, its hooks or its callers.
func TestSandboxRuleNetworkIsPerTask(t *testing.T) {
	requireSandbox(t)
	f := sandboxProject(t, perTaskRunefile)
	cs, _, _ := f.ruleServer(t, nil)
	args := map[string]any{"addr": f.tcp}

	wantLines(t, "a plain task with a [network] dependency", callTask(t, cs, "plain", args), "ok", "fail")
	wantLines(t, "a [network] task with a plain hook", callTask(t, cs, "hooked", args), "ok", "fail")
	failing := callTask(t, cs, "failing", args)
	wantLines(t, "a failing [network] task", failing, "ok")
	if !strings.Contains(failing.text, "error:") || strings.Count(failing.text, "\nok") > 1 {
		t.Errorf("the plain failure hook reached the network:\n%s", failing.text)
	}
	if got := f.tcpCount.Load(); got != 3 {
		t.Errorf("the listener saw %d connections, want 3: one for each [network] body", got)
	}
}

// A task of a module is confined like any other.
func TestSandboxRuleModuleTask(t *testing.T) {
	requireSandbox(t)
	f := sandboxProject(t, "mod tools \"tools.rune\"\n\n# Build.\nbuild:\n    @echo built\n")
	f.write(t, "tools.rune", "# Write outside.\nwrite outside:\n"+attempt("echo x > {{outside}}/from-module"))
	before := treeDigest(t, f.outside)
	cs, _, _ := f.ruleServer(t, nil)
	res := callTask(t, cs, "tools__write", map[string]any{"outside": f.outside})
	if res.stdout != "denied\n" || treeDigest(t, f.outside) != before {
		t.Errorf("a module's task wrote outside: %+v", res)
	}
}

// Quickstart H: calls that run at the same time keep their own rights.
func TestSandboxRuleConcurrentCalls(t *testing.T) {
	requireSandbox(t)
	f := sandboxProject(t, perTaskRunefile)
	cs, _, _ := f.ruleServer(t, nil)
	const each = 10
	var (
		wg                 sync.WaitGroup
		mu                 sync.Mutex
		connected, refused int
	)
	for i := range 2 * each {
		task := "try"
		if i%2 == 0 {
			task = "fetch"
		}
		wg.Go(func() {
			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: task, Arguments: map[string]any{"addr": f.tcp}})
			if err != nil {
				t.Errorf("%s: %v", task, err)
				return
			}
			text := ""
			for _, c := range res.Content {
				if tc, ok := c.(*mcp.TextContent); ok {
					text += tc.Text
				}
			}
			mu.Lock()
			defer mu.Unlock()
			switch {
			case task == "fetch" && strings.HasPrefix(text, "ok"):
				connected++
			case task == "try" && strings.HasPrefix(text, "error:"):
				refused++
			default:
				t.Errorf("%s returned %q", task, text)
			}
		})
	}
	wg.Wait()
	if connected != each || refused != each {
		t.Errorf("%d [network] calls connected and %d plain calls were refused, want %d and %d", connected, refused, each, each)
	}
	if got := f.tcpCount.Load(); got != each {
		t.Errorf("the listener saw %d connections, want %d", got, each)
	}
}

// Quickstart M: a body cannot widen its rights by starting Rune.
func TestSandboxRuleNestedRune(t *testing.T) {
	requireSandbox(t)
	f := sandboxProject(t, perTaskRunefile)
	cs, _, _ := f.ruleServer(t, nil)
	res := callTask(t, cs, "nested", map[string]any{"rune": runeBin, "addr": f.tcp})
	if !strings.Contains(res.stdout, "error:") || strings.Contains(res.stdout, "ok") {
		t.Errorf("the inner [network] task: stdout %q, stderr %q", res.stdout, res.stderr)
	}
	if got := f.tcpCount.Load(); got != 0 {
		t.Errorf("the listener saw %d connections", got)
	}
}

// Quickstart M: the [context] task a server runs at start is confined too.
func TestSandboxRuleContextTask(t *testing.T) {
	requireSandbox(t)
	// The Runefile names the directory outside the project, which is known
	// once the fixture exists.
	f := sandboxProject(t, "# Build.\nbuild:\n    @echo built\n")
	f.write(t, "Runefile", "# Report the project state.\n[context]\nhealth:\n"+
		attempt("echo x > "+f.outside+"/from-context")+
		"    @echo healthy\n\n# Build.\nbuild:\n    @echo built\n")
	before := treeDigest(t, f.outside)
	cs, _, _ := f.ruleServer(t, nil)
	if got := cs.InitializeResult().Instructions; got != "denied\nhealthy" {
		t.Errorf("instructions = %q", got)
	}
	if treeDigest(t, f.outside) != before {
		t.Error("the [context] task wrote outside the project")
	}
}

// A server started below its Runefile's directory found the Runefile by
// searching up. A task could put a nearer one where the search starts, so
// the call is refused. Naming the Runefile makes the choice a fixed one.
func TestSandboxRefusesSearchedRunefile(t *testing.T) {
	requireSandbox(t)
	f := sandboxProject(t, "# Build.\nbuild:\n    @: > build.ran\n")
	below := filepath.Join(f.dir, "pkg")
	if err := os.Mkdir(below, 0o755); err != nil {
		t.Fatal(err)
	}
	project := f.dir
	f.dir = below
	cs, _, _ := f.server(t, nil)
	res := callTask(t, cs, "build", nil)
	want := `task "build" was not run: the sandbox cannot protect the choice of the Runefile: rune found ` + filepath.Join(project, "Runefile") + ` by searching up from ` + below + `, where a task could create a nearer one. Start rune in ` + project + `, or name the Runefile with --file.`
	if !res.isError || !strings.Contains(res.text, want) || hasMarker(project, "build.ran") {
		t.Errorf("isError %v, text %q, want %q in it", res.isError, res.text, want)
	}

	named, _, _ := f.server(t, nil, "--file", filepath.Join(project, "Runefile"))
	if res := callTask(t, named, "build", nil); res.isError || !hasMarker(project, "build.ran") {
		t.Errorf("with --file: %+v", res)
	}
}
