package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/rune-task-runner/rune/internal/cli"
)

// US3: serve flag validation. addr/token-file are HTTP-only; supplying them
// without --http is a usage error. (HTTP-requires-token is checked in ServeMCP,
// not here — see validateServeFlags doc.)

func TestValidateServeFlags(t *testing.T) {
	cases := []struct {
		name            string
		useHTTP         bool
		addr, tokenFile string
		wantErr         bool
	}{
		{"stdio default", false, "", "", false},
		{"http with addr and token", true, ":8080", "tok", false},
		{"addr without http", false, ":8080", "", true},
		{"token-file without http", false, "", "tok", true},
	}
	for _, c := range cases {
		err := validateServeFlags(c.useHTTP, c.addr, c.tokenFile)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: validateServeFlags(%v,%q,%q) err=%v, wantErr=%v",
				c.name, c.useHTTP, c.addr, c.tokenFile, err, c.wantErr)
		}
	}
}

// Spec 031: --allow-destructive is a repeatable string array on serve/mcp.
// Values stay raw here; internal/cli splits and validates them.
func TestServeAllowDestructiveFlag(t *testing.T) {
	var opts cli.Options
	cmd := newServeCmd(&opts)
	f := cmd.Flags().Lookup("allow-destructive")
	if f == nil {
		t.Fatal("serve has no --allow-destructive flag")
	}
	if got := f.Value.Type(); got != "stringArray" {
		t.Errorf("flag type = %q, want stringArray", got)
	}
	if want := "let agents run these [confirm] tasks (comma-separated, repeatable)"; f.Usage != want {
		t.Errorf("usage = %q, want %q", f.Usage, want)
	}
	if err := cmd.ParseFlags([]string{"--allow-destructive", "clean,reset", "--allow-destructive", "x"}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"clean,reset", "x"}; !reflect.DeepEqual(opts.MCPAllowDestructive, want) {
		t.Errorf("MCPAllowDestructive = %q, want %q", opts.MCPAllowDestructive, want)
	}
}

// Spec 031 FR-007: --yes is not a serve/mcp flag in either position, so no
// switch approves every destructive task; a bare --allow-destructive is a
// usage error too.
func TestServeRejectsYes(t *testing.T) {
	const unknownYes = "unknown flag: --yes"
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"mcp", "--yes"}, unknownYes},
		{[]string{"--yes", "mcp"}, unknownYes},
		{[]string{"serve", "--yes"}, unknownYes},
		{[]string{"mcp", "-f", "x", "--yes"}, unknownYes},
		{[]string{"-f", "x", "--yes", "mcp"}, unknownYes},
		{[]string{"mcp", "--allow-destructive"}, "flag needs an argument: --allow-destructive"},
	} {
		if code := run(c.args); code != 2 {
			t.Errorf("run(%q) = %d, want 2", c.args, code)
		}
		// Exit code 2 alone would pass with --yes accepted: the missing file x
		// is a usage error too.
		root, _ := commandTree(t, &cli.Options{})
		root.SetArgs(c.args)
		if err := root.Execute(); err == nil || err.Error() != c.want {
			t.Errorf("rune %q: error = %v, want %q", c.args, err, c.want)
		}
	}
}

// Spec 031 FR-017: `rune serve --help` documents the opt-in and shows it.
func TestServeHelpMentionsAllowDestructive(t *testing.T) {
	var opts cli.Options
	cmd := newServeCmd(&opts)
	if !strings.Contains(cmd.Long, "--allow-destructive") || !strings.Contains(cmd.Long, "[confirm]") {
		t.Errorf("Long does not explain --allow-destructive for [confirm] tasks:\n%s", cmd.Long)
	}
	want := "  rune mcp --allow-destructive clean                  # let agents run the [confirm] task 'clean'"
	if !strings.Contains(cmd.Example, want+"\n") && !strings.HasSuffix(cmd.Example, want) {
		t.Errorf("Example lacks the line %q:\n%s", want, cmd.Example)
	}
}

// Spec 041: --allow and --deny are repeatable string arrays on serve/mcp.
// Values stay raw here; internal/cli splits and validates them.
func TestServeAllowDenyFlags(t *testing.T) {
	var opts cli.Options
	cmd := newServeCmd(&opts)
	for name, usage := range map[string]string{
		"allow": "expose only these tasks to agents (comma-separated, repeatable)",
		"deny":  "hide these tasks from agents (comma-separated, repeatable)",
	} {
		f := cmd.Flags().Lookup(name)
		if f == nil {
			t.Fatalf("serve has no --%s flag", name)
		}
		if got := f.Value.Type(); got != "stringArray" {
			t.Errorf("--%s type = %q, want stringArray", name, got)
		}
		if f.Usage != usage {
			t.Errorf("--%s usage = %q, want %q", name, f.Usage, usage)
		}
	}
	args := []string{"--allow", "build,test", "--allow", "lint", "--deny", "deploy", "--allow-destructive", "clean"}
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	if want := []string{"build,test", "lint"}; !reflect.DeepEqual(opts.MCPAllow, want) {
		t.Errorf("MCPAllow = %q, want %q", opts.MCPAllow, want)
	}
	if want := []string{"deploy"}; !reflect.DeepEqual(opts.MCPDeny, want) {
		t.Errorf("MCPDeny = %q, want %q", opts.MCPDeny, want)
	}
	if want := []string{"clean"}; !reflect.DeepEqual(opts.MCPAllowDestructive, want) {
		t.Errorf("MCPAllowDestructive = %q, want %q", opts.MCPAllowDestructive, want)
	}
}

// Spec 041 FR-019: the help text names both flags with an example each.
func TestServeHelpMentionsAllowDeny(t *testing.T) {
	var opts cli.Options
	cmd := newServeCmd(&opts)
	for _, want := range []string{"--allow", "--deny", "still run"} {
		if !strings.Contains(cmd.Long, want) {
			t.Errorf("Long lacks %q:\n%s", want, cmd.Long)
		}
	}
	for _, want := range []string{"rune mcp --allow build,test", "rune mcp --deny deploy"} {
		if !strings.Contains(cmd.Example, want) {
			t.Errorf("Example lacks %q:\n%s", want, cmd.Example)
		}
	}
}

// A task list is one comma-separated value. A space-separated one would leave
// every name after the first as a positional argument; dropping it silently
// would expose a task the operator meant to hide.
func TestServeRejectsPositionalArguments(t *testing.T) {
	for _, args := range [][]string{
		{"mcp", "--deny", "build", "deploy"},
		{"mcp", "--allow", "build", "test"},
		{"serve", "extra"},
	} {
		if code := run(args); code != 2 {
			t.Errorf("run(%q) = %d, want 2", args, code)
		}
	}
}

// Spec 042: --output-limit is one string, and only a flag the command line
// set reaches the options, so an empty value is reported and not ignored.
func TestServeOutputLimitFlag(t *testing.T) {
	var opts cli.Options
	cmd := newServeCmd(&opts)
	f := cmd.Flags().Lookup("output-limit")
	if f == nil {
		t.Fatal("serve has no --output-limit flag")
	}
	if got := f.Value.Type(); got != "string" {
		t.Errorf("flag type = %q, want string", got)
	}
	if want := "cap each of stdout and stderr in a tool result (default 64KiB; such as 256KiB or 1MiB)"; f.Usage != want {
		t.Errorf("usage = %q, want %q", f.Usage, want)
	}

	cases := []struct {
		args []string
		want *string
	}{
		{nil, nil},
		{[]string{"--output-limit", "256KiB"}, new("256KiB")},
		{[]string{"--output-limit", ""}, new("")},
		{[]string{"--output-limit=1KiB", "--output-limit", "4KiB"}, new("4KiB")},
	}
	for _, c := range cases {
		cmd := newServeCmd(&cli.Options{})
		if err := cmd.ParseFlags(c.args); err != nil {
			t.Fatal(err)
		}
		got := givenFlag(cmd, "output-limit", cmd.Flags().Lookup("output-limit").Value.String())
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("%q: got %v, want %v", c.args, got, c.want)
		}
	}
}

// The value reaches cli.Options through the command itself. An invalid one
// fails in ServeMCP before a Runefile is looked for, which shows it arrived.
func TestServeOutputLimitReachesServeMCP(t *testing.T) {
	var opts cli.Options
	cmd := newServeCmd(&opts)
	cmd.SetArgs([]string{"--output-limit", ""})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	err := cmd.Execute()
	if err == nil || !strings.HasPrefix(err.Error(), `--output-limit: invalid size ""`) {
		t.Errorf("error = %v, want the size usage error", err)
	}
	if opts.MCPOutputLimit == nil || *opts.MCPOutputLimit != "" {
		t.Errorf("MCPOutputLimit = %v, want an empty value", opts.MCPOutputLimit)
	}
}

func TestServeHelpMentionsOutputLimit(t *testing.T) {
	cmd := newServeCmd(&cli.Options{})
	for _, want := range []string{"--output-limit", "[truncated N bytes]", "mcp_output_limit"} {
		if !strings.Contains(cmd.Long, want) {
			t.Errorf("Long lacks %q:\n%s", want, cmd.Long)
		}
	}
	if want := "rune mcp --output-limit 256KiB"; !strings.Contains(cmd.Example, want) {
		t.Errorf("Example lacks %q:\n%s", want, cmd.Example)
	}
}

// Spec 043: --no-reload is off by default and reaches the options.
func TestServeNoReloadFlag(t *testing.T) {
	var opts cli.Options
	cmd := newServeCmd(&opts)
	if f := cmd.Flags().Lookup("no-reload"); f == nil || f.DefValue != "false" {
		t.Fatalf("--no-reload flag = %+v, want a boolean that defaults to false", f)
	}
	if err := cmd.ParseFlags([]string{"--no-reload"}); err != nil {
		t.Fatal(err)
	}
	if !opts.MCPNoReload {
		t.Error("--no-reload did not set MCPNoReload")
	}
	if want := "--no-reload"; !strings.Contains(cmd.Long, want) || !strings.Contains(cmd.Example, "rune mcp --no-reload") {
		t.Errorf("help does not explain %s:\n%s\n%s", want, cmd.Long, cmd.Example)
	}
}

// Spec 044: the help says a result holds the output as text and as fields,
// so the size an operator expects from --output-limit is the real one.
func TestServeHelpMentionsStructuredFields(t *testing.T) {
	cmd := newServeCmd(&cli.Options{})
	for _, want := range []string{"exitCode, stdout, stderr", "fixSuggestion", "twice"} {
		if !strings.Contains(cmd.Long, want) {
			t.Errorf("Long lacks %q:\n%s", want, cmd.Long)
		}
	}
}

// Spec 045 FR-014: the help says what a bind beyond loopback means, and its
// HTTP example is not such a bind.
func TestServeHelpMentionsCleartextWarning(t *testing.T) {
	cmd := newServeCmd(&cli.Options{})
	for _, want := range []string{"non-loopback", "cleartext", "no TLS"} {
		if !strings.Contains(cmd.Long, want) {
			t.Errorf("Long lacks %q:\n%s", want, cmd.Long)
		}
	}
	if want := "rune serve --http --addr 127.0.0.1:7777"; !strings.Contains(cmd.Example, want) {
		t.Errorf("Example lacks %q:\n%s", want, cmd.Example)
	}
	if strings.Contains(cmd.Example, "--addr :") {
		t.Errorf("Example binds every interface:\n%s", cmd.Example)
	}
	if usage := cmd.Flags().Lookup("addr").Usage; !strings.Contains(usage, "127.0.0.1:7777") {
		t.Errorf("--addr usage = %q, want it to name the default", usage)
	}
}

// Spec 048: serve/mcp take -f/--file, bound to the field the root's flag of
// the same name sets.
func TestServeFileFlag(t *testing.T) {
	f := newServeCmd(&cli.Options{}).Flags().Lookup("file")
	if f == nil {
		t.Fatal("serve has no --file flag")
	}
	if f.Shorthand != "f" || f.DefValue != "" || f.Value.Type() != "string" {
		t.Errorf("--file: shorthand %q, default %q, type %q; want f, empty, string", f.Shorthand, f.DefValue, f.Value.Type())
	}
	if want := "serve this Runefile instead of the one found from the working directory"; f.Usage != want {
		t.Errorf("usage = %q, want %q", f.Usage, want)
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"-f", "x"}, "x"},
		{[]string{"--file=y"}, "y"},
		{[]string{"--file", "z"}, "z"},
		{[]string{"--file", "first", "-f", "last"}, "last"},
		{nil, ""},
	} {
		var opts cli.Options
		if err := newServeCmd(&opts).ParseFlags(c.args); err != nil {
			t.Fatalf("%q: %v", c.args, err)
		}
		if opts.File != c.want {
			t.Errorf("%q: File = %q, want %q", c.args, opts.File, c.want)
		}
	}
}

// Spec 048 FR-016: an empty value is a mistake to report. Read as "no flag",
// it would serve whatever Runefile is above the directory the client started
// the server in.
func TestServeFileFlagEmptyValue(t *testing.T) {
	// No Runefile here or above: a command that went on to look for one fails
	// with another message, and starts no server.
	t.Chdir(t.TempDir())
	for _, args := range [][]string{{"--file", ""}, {"-f", ""}, {"--file="}, {"-f", "x", "--file", ""}} {
		cmd := newServeCmd(&cli.Options{})
		cmd.SetArgs(args)
		cmd.SilenceUsage, cmd.SilenceErrors = true, true
		err := cmd.Execute()
		var usage *cli.UsageError
		if !errors.As(err, &usage) || err.Error() != "--file needs a path" {
			t.Errorf("%q: error = %v, want the usage error --file needs a path", args, err)
		}
	}
}

// commandTree is the root command with the commands main registers.
func commandTree(t *testing.T, opts *cli.Options) (root, serve *cobra.Command) {
	t.Helper()
	root = newRootCmd(opts, "", "", nil)
	addCommands(root, opts)
	serve, _, err := root.Find([]string{"serve"})
	if err != nil || serve == root {
		t.Fatalf("main registers no serve command: %v", err)
	}
	return root, serve
}

// Spec 048 FR-011: the server shares --file with the root command and gained
// nothing else from it. A root flag made persistent, or a serve flag added
// under a root flag's name, fails here before it reaches the agent surface.
// --yes is the one that matters most: it approves every [confirm] task.
func TestServeHasNoRootFlags(t *testing.T) {
	root, serve := commandTree(t, &cli.Options{})
	// ignore-version was on both before spec 048, each with its own meaning.
	shared := map[string]bool{"file": true, "ignore-version": true}
	// Named outright, so that one of them made persistent is caught: it would
	// then be missing from the root's own flags walked below.
	for _, name := range []string{"yes", "quiet", "set", "watch", "list", "dry-run"} {
		if serve.Flag(name) != nil {
			t.Errorf("serve accepts the root flag --%s", name)
		}
	}
	walked := 0
	root.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) {
		walked++
		if serve.Flag(f.Name) != nil && !shared[f.Name] {
			t.Errorf("serve accepts the root flag --%s", f.Name)
		}
	})
	if walked == 0 {
		t.Fatal("the root command has no flags of its own, so this test checks nothing")
	}
	for name := range shared {
		if serve.Flags().Lookup(name) == nil {
			t.Errorf("serve has no --%s flag of its own", name)
		}
	}
}

// Spec 048 FR-013: only the server gained the flag. Spec 052 adds `log`,
// which reads the log of the Runefile a server was given with the same flag.
func TestOnlyServeHasFileFlag(t *testing.T) {
	root, serve := commandTree(t, &cli.Options{})
	for _, cmd := range root.Commands() {
		if cmd != serve && cmd.Name() != "log" && cmd.Flag("file") != nil {
			t.Errorf("%s accepts --file", cmd.Name())
		}
	}
}

// Spec 048 FR-019.
func TestServeHelpMentionsFile(t *testing.T) {
	cmd := newServeCmd(&cli.Options{})
	for _, want := range []string{"--file", "Runefile's directory", "absolute path"} {
		if !strings.Contains(cmd.Long, want) {
			t.Errorf("Long lacks %q:\n%s", want, cmd.Long)
		}
	}
	if want := "rune mcp -f /path/to/project/Runefile"; !strings.Contains(cmd.Example, want) {
		t.Errorf("Example lacks %q:\n%s", want, cmd.Example)
	}
}

// Spec 049 FR-025: `rune serve --help` says a [confirm] call can be approved
// in the client, beside the grant.
func TestServeHelpMentionsApproval(t *testing.T) {
	var opts cli.Options
	cmd := newServeCmd(&opts)
	for _, want := range []string{
		"A client that can show an\nMCP form request is asked to approve a call to any other [confirm] task",
		"that call runs once on approval",
		"needs --allow-destructive or an approval",
	} {
		if !strings.Contains(cmd.Long, want) {
			t.Errorf("Long lacks %q:\n%s", want, cmd.Long)
		}
	}
}
