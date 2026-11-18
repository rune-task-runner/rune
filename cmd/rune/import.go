package main

import (
	"github.com/spf13/cobra"

	"github.com/rune-task-runner/rune/internal/cli"
)

// newImportCmd builds the `import` command: it writes a Runefile from the
// task file of another runner (spec 054). It reads that file and runs
// nothing. A Runefile cannot have a task named import, which is a keyword of
// the language, so the command takes no name from a task.
func newImportCmd(opts *cli.Options) *cobra.Command {
	var flags cli.ImportFlags

	cmd := &cobra.Command{
		Use:   "import [source]",
		Short: "Write a Runefile from a justfile, Makefile, Taskfile or package.json",
		Long: `Read the task file of another runner and write a Runefile with the same tasks.
The source is only read: no task, no variable command and no script is run, no
include is fetched, and the source is never changed.

The source is a file, or a directory to look in (the working directory by
default). The format is taken from the file name: a justfile, a Makefile, a
Taskfile (version 3) or a package.json. --from names it for another name.

Every task is reported on standard error with its source line as converted,
review (converted, with a difference the line names), or unsupported (not
converted: the task is in the Runefile and fails with the reason). With --wrap
an unsupported task becomes a wrapper that runs the original runner.

The Runefile is written next to the source. An existing file is never
replaced unless --force is given; --output writes to another file.

Exit status: 0 when nothing is unsupported, 1 when something is (the Runefile
is still written), 2 for a usage error, 3 when the source cannot be parsed.

This is not the 'import' statement of a Runefile, which splices another
Runefile into the current one. To keep an existing Runefile, write the import
to another file and name that file in an import statement.`,
		Example: `  rune import                               # the one task file in this directory
  rune import --from make ./Makefile        # name the format and the file
  rune import --dry-run                     # print the Runefile and the report, write nothing
  rune import -o imported.rune              # keep the existing Runefile; add: import "imported.rune"
  rune import --wrap                        # unsupported tasks call the original runner`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			source := ""
			if len(args) == 1 {
				source = args[0]
			}
			return cli.Import(*opts, flags, source)
		},
	}

	f := cmd.Flags()
	f.StringVar(&flags.From, "from", "", "the format of the source: justfile, make, task or npm (default: from the file name)")
	f.StringVarP(&flags.Output, "output", "o", "", "write the Runefile to this path (default: Runefile next to the source)")
	f.BoolVar(&flags.DryRun, "dry-run", false, "print the Runefile to standard output and write no file")
	f.BoolVar(&flags.Wrap, "wrap", false, "turn each unsupported task into a wrapper that runs the original runner")
	f.BoolVar(&flags.Force, "force", false, "replace the file at the output path")

	formats := []cobra.Completion{"justfile", "make", "task", "npm"}
	// The flag exists, so registering its completion cannot fail.
	_ = cmd.RegisterFlagCompletionFunc("from", cobra.FixedCompletions(formats, cobra.ShellCompDirectiveNoFileComp))

	return cmd
}
