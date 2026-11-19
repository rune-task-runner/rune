package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rune-task-runner/rune/internal/agentinit"
	"github.com/rune-task-runner/rune/internal/cli"
)

// newAgentCmd builds the `agent` command, the parent of `agent init`
// (spec 055). The name hides a Runefile task named agent on the command
// line, as `log` hides one named log; that task is run with `rune -- agent`.
func newAgentCmd(opts *cli.Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Set up an agent client to work through Rune",
		Long: `Commands that prepare an agent client, such as Claude Code, Codex CLI or VS
Code, to work on this project through its Runefile.

A Runefile task named 'agent' is run with 'rune -- agent'.`,
		Example: `  rune agent init claude            # set up Claude Code for this project
  rune agent init codex --dry-run   # show what would change for Codex CLI`,
		// Without this Cobra prints the help and exits 0 for any argument,
		// so a script that ran a task named agent would do nothing and
		// succeed.
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return &cli.UsageError{Err: fmt.Errorf(`unknown command %q for "rune agent"; to run a task named agent, use: rune -- agent`, args[0])}
			}
			cli.AgentTaskNote(*opts)
			return cmd.Help()
		},
	}
	cmd.AddCommand(newAgentInitCmd(opts))
	return cmd
}

// newAgentInitCmd builds `agent init <client>`: it writes the project files
// that register `rune mcp` with a client and remove the client's own
// command-running tools. It runs nothing of the Runefile.
func newAgentInitCmd(opts *cli.Options) *cobra.Command {
	var flags cli.AgentInitFlags

	cmd := &cobra.Command{
		Use:   "init <client>",
		Short: "Write the project files that give an agent client Rune's tools and no shell",
		Long: `Set a client up for this project: claude (Claude Code), codex (Codex CLI) or
vscode (VS Code with Copilot agent mode). The command writes the client's
project files: it registers 'rune mcp' for this project, removes the client's
own tools that run commands, and lets Rune's tools run.

  claude   .mcp.json and .claude/settings.json
  codex    .codex/config.toml
  vscode   .vscode/mcp.json, .mcp.json and .github/agents/rune.agent.md

Only files inside the project are written. Other servers, other settings,
comments and existing deny rules are kept, and a deny rule is never removed. A
value that is already there with another value stops the run and is named
with its line; --force replaces it. No task runs.

The report on standard error ends with one of three outcomes:

  Rune-only                the agent has Rune's tools and no tool that runs a
                           command, in every session of the project
  Rune-only within SCOPE   the same, inside one custom agent
  Registered only          Rune's tools are there, and so are the client's own

'Rune-only' is said only for a client version that was checked. For any other
version, and for VS Code, the files are written and the outcome is
'Registered only'. The steps left to you, such as trusting the project, are
listed, and so is a check you can run yourself.

The server is registered with the path of this project's Runefile, which is
right for one checkout. --unpinned registers it without the path, for files
you commit. --dry-run prints the difference and writes nothing. --check says
whether the setup is in the project's files; it writes nothing and starts no
program.

Exit status: 0 for 'Rune-only' and for a check that found the setup complete,
1 for 'Registered only', for a check that did not, and for a file that could
not be read or written, 2 for a usage error or a conflict, 3 when the
Runefile or a client file is not valid.

A Runefile task named 'agent' is run with 'rune -- agent'.`,
		Example: `  rune agent init claude              # write .mcp.json and .claude/settings.json
  rune agent init codex --dry-run     # print the difference, write nothing
  rune agent init vscode              # the server and the custom agent 'rune'
  rune agent init claude --unpinned   # no path in the registration: for a committed file
  rune agent init claude --force      # replace a value that is already there
  rune agent init codex --check       # is the setup still in the files?`,
		Args: func(_ *cobra.Command, args []string) error {
			switch len(args) {
			case 1:
				return nil
			case 0:
				return &cli.UsageError{Err: errors.New("rune agent init needs a client: " + agentinit.ClientList("or"))}
			}
			return &cli.UsageError{Err: errors.New("rune agent init takes one client: " + agentinit.ClientList("or"))}
		},
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			clients := make([]cobra.Completion, len(agentinit.Clients()))
			for i, c := range agentinit.Clients() {
				clients[i] = cobra.Completion(c)
			}
			return clients, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// Read as "no flag", an empty value would write the files of
			// whatever project is above the working directory.
			if cmd.Flags().Changed("file") && opts.File == "" {
				return &cli.UsageError{Err: errors.New("--file needs a path")}
			}
			return cli.AgentInit(*opts, flags, args[0])
		},
	}

	f := cmd.Flags()
	// Declared here, not inherited from the root, as for serve and log: the
	// command takes none of the run flags.
	f.StringVarP(&opts.File, "file", "f", "", "set up the project of this Runefile instead of the one found from the working directory")
	f.BoolVar(&flags.Unpinned, "unpinned", false, "register the server without the Runefile's path, for files you commit")
	f.BoolVar(&flags.Force, "force", false, "replace a value the command owns that is already there with another value")
	f.BoolVar(&flags.DryRun, "dry-run", false, "print the difference for each file to standard output and write nothing")
	f.BoolVar(&flags.Check, "check", false, "say whether the setup is in the project's files; write nothing, start nothing")

	return cmd
}
