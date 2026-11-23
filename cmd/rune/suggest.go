package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rune-task-runner/rune/internal/cli"
	"github.com/rune-task-runner/rune/internal/tasksuggest"
)

// newSuggestCmd builds `suggest` (spec 058): it lists the commands an agent
// repeated, as `rune agent observe` recorded them, with the task Rune would
// add for each. `suggest apply` adds the chosen ones to the Runefile and
// `suggest forget` deletes the records. The name hides a Runefile task named
// suggest on the command line; that task is run with `rune -- suggest`.
func newSuggestCmd(opts *cli.Options) *cobra.Command {
	var flags cli.SuggestFlags

	cmd := &cobra.Command{
		Use:   "suggest",
		Short: "List the commands an agent repeated, as tasks to add",
		Long: `List the shell commands an agent repeated in this project, as 'rune agent
observe claude' recorded them, with the task Rune would add for each.

A command is listed when it ran at least --min-runs times (default 3). Runs
are counted once per call, whatever events the client sent for it, and their
outcomes are shown. A command that is already the body of a task, and any
command that runs rune, is left out. Commands that differ in one plain word
become one task with a parameter. A command Rune cannot write as a task
safely, such as a loop or a command substitution, is listed for review with
the reason.

Nothing is run: no recorded command and no task. Without a Runefile the
project is this directory.

Exit status: 0 on success, 1 when the records could not be read, 2 for a usage
error, 3 when the Runefile is not valid.

A Runefile task named 'suggest' is run with 'rune -- suggest'.`,
		Example: `  rune suggest                       # what did the agent repeat?
  rune suggest --min-runs 2          # list commands that ran twice too
  rune suggest apply 3f9a1c2e        # add that task to the Runefile
  rune suggest apply 3f9a1c2e --dry-run
  rune suggest forget                # delete the records`,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return &cli.UsageError{Err: fmt.Errorf(`unknown command %q for "rune suggest"; to run a task named suggest, use: rune -- suggest`, args[0])}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := fileFlag(cmd, opts); err != nil {
				return err
			}
			return cli.Suggest(*opts, flags)
		},
	}
	addSuggestFlags(cmd, opts, &flags)

	apply := &cobra.Command{
		Use:   "apply <id>...",
		Short: "Add the tasks of the given suggestions to the Runefile",
		Long: `Add the tasks of the suggestions with these IDs, as 'rune suggest' lists them
now, at the end of the Runefile, or create the Runefile when there is none.

Every byte already in the Runefile stays. The new Runefile must pass analysis
before it is written, and it is written in one step. When an ID is unknown, or
the Runefile changed while the tasks were being added, nothing is written.
--dry-run prints the difference and writes nothing.

The tasks have no attributes: an agent can call them through 'rune mcp'
without approval. Add [confirm] to a task that changes state.

Exit status: 0 on success, 1 when the Runefile could not be written, 2 for a
usage error, an unknown ID or a Runefile that changed, 3 when the Runefile is
not valid.`,
		Example: `  rune suggest apply 3f9a1c2e
  rune suggest apply 3f9a1c2e 8d02b7e1 --dry-run`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := fileFlag(cmd, opts); err != nil {
				return err
			}
			return cli.SuggestApply(*opts, flags, args)
		},
	}
	addSuggestFlags(apply, opts, &flags)
	apply.Flags().BoolVar(&flags.DryRun, "dry-run", false, "print the difference to standard output and write nothing")

	forget := &cobra.Command{
		Use:   "forget",
		Short: "Delete what was recorded for this project",
		Long: `Delete the commands recorded for this project and the counts of the events
that were not recorded. Recording goes on while the hook is installed; turn it
off with 'rune agent observe claude --remove'.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := fileFlag(cmd, opts); err != nil {
				return err
			}
			return cli.SuggestForget(*opts)
		},
	}
	forget.Flags().StringVarP(&opts.File, "file", "f", "", "the project of this Runefile instead of the one found from the working directory")

	cmd.AddCommand(apply, forget)
	return cmd
}

func addSuggestFlags(cmd *cobra.Command, opts *cli.Options, flags *cli.SuggestFlags) {
	f := cmd.Flags()
	f.StringVarP(&opts.File, "file", "f", "", "the project of this Runefile instead of the one found from the working directory")
	f.IntVar(&flags.MinRuns, "min-runs", tasksuggest.DefaultMinRuns, "list a command that ran at least this many times (1 to 1000)")
}

// fileFlag refuses an empty --file: read as "no flag", it would use whatever
// project is above the working directory.
func fileFlag(cmd *cobra.Command, opts *cli.Options) error {
	if cmd.Flags().Changed("file") && opts.File == "" {
		return &cli.UsageError{Err: errors.New("--file needs a path")}
	}
	return nil
}
