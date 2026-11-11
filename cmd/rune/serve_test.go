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

func TestServeFileFlag(t *testing.T) {
	var opts cli.Options
	cmd := newServeCmd(&opts)
	if f := cmd.Flags().Lookup("file"); f == nil {
		t.Fatal("serve has no --file flag")
	}
	if err := cmd.ParseFlags([]string{"-f", "../Runefile"}); err != nil {
		t.Fatal(err)
	}
	if got, want := opts.File, "../Runefile"; got != want {
		t.Fatalf("file = %q, want %q", got, want)
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
