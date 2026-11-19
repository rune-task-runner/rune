package main

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/rune-task-runner/rune/internal/agentdocs"
	"github.com/rune-task-runner/rune/internal/cli"
)

// newExportCmd builds the `export` command: it writes, from the Runefile,
// the text an agent with no MCP integration needs to find and call the
// tasks (spec 056). The name hides a Runefile task named export on the
// command line, as `agent` hides one named agent; that task is run with
// `rune -- export`.
func newExportCmd(opts *cli.Options) *cobra.Command {
	var flags cli.ExportFlags

	cmd := &cobra.Command{
		Use:   "export <format>",
		Short: "Write guidance for agents from the Runefile: an AGENTS.md section or a Claude skill",
		Long: `Describe the public tasks of the Runefile for an agent that has no MCP
integration: how a task is called, an entry for each task with its
parameters, and the limits its author declared.

  agents-md      one Markdown section between two marker lines, for AGENTS.md
  claude-skill   a skill directory: SKILL.md, and tasks.md for a large Runefile

The export is printed on standard output. --write writes it to its place in
the project, the directory of the Runefile:

  agents-md      AGENTS.md
  claude-skill   .claude/skills/rune-tasks/

--output writes it to another file (agents-md) or skill directory
(claude-skill). In a Markdown file only the lines from "rune:begin tasks" to
"rune:end tasks" are Rune's: everything else in the file is kept, and a file
with no such lines gains the section at its end. A skill file that Rune did
not generate is never replaced.

--check says whether the export at that place is what Rune would write now.
It writes nothing and exits 1 when the export is missing or stale, which
makes it a step for CI.

Nothing of the Runefile runs and nothing of it is evaluated: no task, no
hook, no default. The environment is not read.

The text is guidance. It states the author's [confirm] and [agent-allow]
declarations and tells an agent what to do about them, and it does not limit
what a shell can run. Rune enforces those declarations only for calls
through its MCP server, 'rune mcp'.

Exit status: 0 when the export was printed, written or found current, 1 when
a check found it missing or stale or a file could not be read or written, 2
for a usage error or a file Rune will not change, 3 when the Runefile is not
valid.

A Runefile task named 'export' is run with 'rune -- export'.`,
		Example: `  rune export agents-md                    # print the section
  rune export agents-md --write            # create or update AGENTS.md
  rune export agents-md --check            # is AGENTS.md current? for CI
  rune export agents-md -o docs/AGENTS.md  # another file
  rune export claude-skill --write         # .claude/skills/rune-tasks/`,
		// Any arguments reach cli.Export, which knows the formats and
		// prints the note about a task named export before its error.
		Args: cobra.ArbitraryArgs,
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			formats := make([]cobra.Completion, len(agentdocs.Formats()))
			for i, f := range agentdocs.Formats() {
				formats[i] = cobra.Completion(f)
			}
			return formats, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// Read as "no flag", an empty value would export whatever
			// Runefile is above the working directory.
			if cmd.Flags().Changed("file") && opts.File == "" {
				return &cli.UsageError{Err: errors.New("--file needs a path")}
			}
			// The same for --output: an empty value would print the export,
			// or check the documented location, and exit 0.
			if cmd.Flags().Changed("output") && flags.Output == "" {
				return &cli.UsageError{Err: errors.New("--output needs a path")}
			}
			return cli.Export(*opts, flags, args)
		},
	}

	f := cmd.Flags()
	// Declared here, not inherited from the root, as for serve and agent
	// init: the command takes none of the run flags.
	f.StringVarP(&opts.File, "file", "f", "", "export this Runefile instead of the one found from the working directory")
	f.BoolVar(&flags.Write, "write", false, "write the export to its place in the project")
	f.StringVarP(&flags.Output, "output", "o", "", "write the export to this file (agents-md) or skill directory (claude-skill)")
	f.BoolVar(&flags.Check, "check", false, "say whether the export is what rune would write now; write nothing")

	return cmd
}
