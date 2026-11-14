package main

import (
	"reflect"
	"strings"
	"testing"

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
	for _, args := range [][]string{
		{"mcp", "--yes"},
		{"--yes", "mcp"},
		{"serve", "--yes"},
		{"mcp", "--allow-destructive"},
	} {
		if code := run(args); code != 2 {
			t.Errorf("run(%q) = %d, want 2", args, code)
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
