package main

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/rune-task-runner/rune/internal/cli"
)

// newServeCmd builds the `serve` command (aliased `mcp`): it starts the MCP
// server over stdio by default, or Streamable HTTP with --http. It replaces the
// hand-rolled argument loop that previously lived in main.go.
func newServeCmd(opts *cli.Options) *cobra.Command {
	var (
		useHTTP   bool
		addr      string
		tokenFile string
		mcpFlag   bool
	)

	cmd := &cobra.Command{
		Use:     "serve",
		Aliases: []string{"mcp"},
		Short:   "Run the MCP server for agents and IDEs",
		Long: `Run the Model Context Protocol (MCP) server so agents and IDEs can call your
Runefile's non-private tasks as tools. Tasks restricted to another OS via
[linux]/[macos]/[windows]/[unix] are not exposed on this host.

By default the server speaks MCP over stdio. Use --http to serve over Streamable
HTTP, which additionally requires --token-file for bearer-token authentication.
The 'mcp' alias is shorthand for stdio serving.

Tasks marked [confirm] are refused unless you name each one with
--allow-destructive; no option allows all of them.

Use --allow to expose only the tasks you name, and --deny to hide tasks. A
task named in both is hidden. A hidden task is not listed and cannot be
called, but the dependencies and hooks of an exposed task still run. A
[confirm] task needs --allow-destructive whichever of these you set.`,
		Example: `  rune mcp                                            # stdio (for local agents/IDEs)
  rune serve                                          # same as 'rune mcp'
  rune mcp --allow-destructive clean                  # let agents run the [confirm] task 'clean'
  rune mcp --allow build,test                         # expose only 'build' and 'test'
  rune mcp --deny deploy                              # expose everything except 'deploy'
  rune serve --http --addr :7777 --token-file ./tok   # HTTP transport`,
		// A task list is one comma-separated value. Without this, the names
		// after a space would be dropped and the tasks left exposed.
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := validateServeFlags(useHTTP, addr, tokenFile); err != nil {
				return err
			}
			_ = mcpFlag // MCP is the only protocol; --mcp is accepted for clarity.
			return cli.ServeMCP(*opts, useHTTP, addr, tokenFile)
		},
	}

	f := cmd.Flags()
	f.BoolVar(&useHTTP, "http", false, "serve over Streamable HTTP instead of stdio")
	f.StringVar(&addr, "addr", "", "HTTP listen address (with --http)")
	f.StringVar(&tokenFile, "token-file", "", "bearer-token file for HTTP auth (with --http)")
	f.BoolVar(&mcpFlag, "mcp", false, "accepted for clarity; MCP is the only protocol")
	f.BoolVar(&opts.MCPAllowIgnoreVersion, "ignore-version", false, "serve even if the Runefile's minimum_version is unmet (operator opt-in)")
	f.StringArrayVar(&opts.MCPAllowDestructive, "allow-destructive", nil, "let agents run these [confirm] tasks (comma-separated, repeatable)")
	f.StringArrayVar(&opts.MCPAllow, "allow", nil, "expose only these tasks to agents (comma-separated, repeatable)")
	f.StringArrayVar(&opts.MCPDeny, "deny", nil, "hide these tasks from agents (comma-separated, repeatable)")

	return cmd
}

// validateServeFlags rejects HTTP-only flags supplied without --http. The
// complementary rule — HTTP requires a token file — is enforced inside
// cli.ServeMCP, so it is intentionally NOT duplicated here.
func validateServeFlags(useHTTP bool, addr, tokenFile string) error {
	if !useHTTP && (addr != "" || tokenFile != "") {
		return &cli.UsageError{Err: errors.New("--addr and --token-file require --http")}
	}
	return nil
}
