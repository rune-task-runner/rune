package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rune-task-runner/rune/internal/cli"
)

// newCICmd builds the `ci` command, the parent of `ci github` and `ci run`
// (spec 059). The name hides a Runefile task named ci on the command line,
// as `agent` hides one named agent; that task is run with `rune -- ci`.
func newCICmd(opts *cli.Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ci",
		Short: "Generate a CI workflow from the Runefile, and run one of its jobs",
		Long: `Commands that run the Runefile's tasks in CI the way they run locally.

  github   write a GitHub Actions workflow for the root tasks you name
  run      run the body of one task, as each job of that workflow does

A Runefile task named 'ci' is run with 'rune -- ci'.`,
		Example: `  rune ci github lint test                   # print the workflow
  rune ci github --write lint test           # .github/workflows/rune.yml
  rune ci github --check lint test           # is it current? for CI`,
		// Without this Cobra prints the help and exits 0 for any argument,
		// so a script that ran a task named ci would do nothing and succeed.
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return &cli.UsageError{Err: fmt.Errorf(`unknown command %q for "rune ci"; to run a task named ci, use: rune -- ci`, args[0])}
			}
			cli.CITaskNote(*opts)
			return cmd.Help()
		},
	}
	cmd.AddCommand(newCIGithubCmd(opts), newCIRunCmd(opts))
	return cmd
}

// newCIGithubCmd builds `ci github`: the workflow of the named root tasks.
func newCIGithubCmd(opts *cli.Options) *cobra.Command {
	var flags cli.CIGithubFlags
	cmd := &cobra.Command{
		Use:   "github [flags] TASK [ARG...]...",
		Short: "Write a GitHub Actions workflow for the root tasks you name",
		Long: `Write a GitHub Actions workflow that runs the tasks you name, and everything
they reach, as a local run of them would.

Each task with its arguments becomes one job. Its prerequisites become needs:
a dependency of a task without [parallel] waits for the dependencies listed
before it, a post-hook waits for its task, and the tasks you name do not wait
for each other. A job installs the pinned Rune release, checked against its
published checksums, and runs 'rune ci run TASK ARG...', which runs that body
and nothing it depends on. The outputs a [cache] task declares travel to the
jobs after it, and its cache key is Rune's own fingerprint of the task.

The workflow reads the repository and nothing else: no secret, no write
permission, no deployment. A task with [confirm] or [applies], one run by an
agent, and anything a job could not run as a local run would, is refused
with the reason.

Nothing runs: no task body, no hook, no network. The environment is not read.

The workflow is printed on standard output. --write writes it to
.github/workflows/rune.yml next to the Runefile, --output to another file.
Rune replaces only a file it generated. --check says whether the file is what
Rune would write now, writes nothing, and exits 1 when it is not.

Runners: --os linux (ubuntu-latest, the default) or macos (macos-latest).

Exit status: 0 when the workflow was printed, written or found current, 1
when a check found it missing or stale or a file could not be written, 2 for
a usage error or a file Rune will not replace, 3 when the Runefile is not
valid or its tasks cannot run as a workflow.`,
		Example: `  rune ci github lint test
  rune ci github --write --os linux --os macos test
  rune ci github --check lint test
  rune ci github --rune-version 0.11.0 -o .github/workflows/ci.yml test`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := fileFlag(cmd, opts); err != nil {
				return err
			}
			if cmd.Flags().Changed("output") && flags.Output == "" {
				return &cli.UsageError{Err: errors.New("--output needs a path")}
			}
			return cli.CIGithub(*opts, flags, args)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&opts.File, "file", "f", "", "use this Runefile instead of the one found from the working directory")
	f.StringArrayVar(&flags.OS, "os", nil, "runner OS: linux (default) or macos; repeat for both")
	f.StringVar(&flags.RuneVersion, "rune-version", "", "the Rune release the jobs install (default: this rune, when it is a release)")
	f.StringVar(&flags.Branch, "branch", "main", "the branch whose pushes run the workflow")
	f.BoolVar(&flags.Write, "write", false, "write .github/workflows/rune.yml next to the Runefile")
	f.StringVarP(&flags.Output, "output", "o", "", "write the workflow to this file")
	f.BoolVar(&flags.Check, "check", false, "say whether the workflow file is what rune would write now; write nothing")
	return cmd
}

// newCIRunCmd builds `ci run`: what each job of a generated workflow runs.
func newCIRunCmd(opts *cli.Options) *cobra.Command {
	var flags cli.CIRunFlags
	cmd := &cobra.Command{
		Use:   "run [flags] [--] TASK [ARG...]",
		Short: "Run the body of one task, without its dependencies, as a CI job does",
		Long: `Run the body of one task with these arguments, as 'rune TASK ARG...' runs it,
but without its dependencies and post-hooks: in a workflow from
'rune ci github', the jobs before this one ran those. When the body fails, its
failure hooks run, as locally.

--dir names the job's directory for bundles. Before the body, the bundles of
earlier jobs under DIR/in, and the restored DIR/bundle.tar, are unpacked into
the project. After a successful [cache] body, its declared outputs and cache
record are written to DIR/bundle.tar.

--key prints the cache key of a [cache] task, key=..., and runs nothing.

--reuse-key and --reuse-max-age skip a [reuse] task that a trusted receipt
shows passed on exactly these files, as on the command line.

Exit status: as 'rune TASK' exits, 2 for a usage error, and 1 when a bundle
cannot be read or written.`,
		Example: `  rune ci run build linux
  rune ci run --dir "$RUNNER_TEMP/rune-ci" -- build linux`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := fileFlag(cmd, opts); err != nil {
				return err
			}
			if cmd.Flags().Changed("dir") && flags.Dir == "" {
				return &cli.UsageError{Err: errors.New("--dir needs a path")}
			}
			if err := checkReuseFlags(cmd, opts); err != nil {
				return err
			}
			return cli.CIRun(*opts, flags, args)
		},
	}
	cmd.Flags().SetInterspersed(false)
	f := cmd.Flags()
	f.StringVarP(&opts.File, "file", "f", "", "use this Runefile instead of the one found from the working directory")
	f.StringVar(&flags.Dir, "dir", "", "the job's directory for the bundles of outputs")
	f.BoolVar(&flags.Key, "key", false, "print the cache key of a [cache] task and run nothing")
	reuseFlags(f, opts)
	return cmd
}
