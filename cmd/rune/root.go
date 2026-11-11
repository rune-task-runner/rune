package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/rune-task-runner/rune/internal/cli"
)

// newRootCmd builds the root command: Rune's task dispatcher. Built-in
// capabilities (serve/mcp, version, completion, plus Cobra's auto-added help)
// are registered as subcommands by the caller. Any first positional that does
// not match a subcommand is a dynamic task invocation handled by RunE.
//
// upgrade performs `--self-upgrade`. It runs before cli.Run, which is where
// Runefile discovery starts, so the flag works with no Runefile or a broken one.
func newRootCmd(opts *cli.Options, version, commit string, upgrade upgradeFunc) *cobra.Command {
	var (
		colorFlag   string
		selfUpgrade bool
	)
	root := &cobra.Command{
		Use:   "rune [global flags] [VAR=VALUE ...] [TASK [ARGS...]] ...",
		Short: "A shared task runner for humans and AI agents",
		// The root command's --help is rendered by applyHelp (see help.go), so
		// no Long/Example is set here; Short is still used in completions.
		Version:       fmt.Sprintf("%s (commit %s)", version, commit),
		SilenceUsage:  true,
		SilenceErrors: true,
		// Tasks are dynamic positionals, not subcommands. Without this, adding
		// subcommands makes Cobra's default validator reject an unknown first
		// arg ("unknown command") instead of routing it to RunE as a task.
		Args: cobra.ArbitraryArgs,
		// Dynamic completion of task names from the current Runefile, merged by
		// Cobra with the built-in command names. Runs side-effect-free, so it
		// resolves the working directory itself rather than relying on
		// PersistentPreRunE (which Cobra does not invoke during completion).
		ValidArgsFunction: func(_ *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
			o := *opts
			o.Cwd, _ = os.Getwd()
			var out []cobra.Completion
			for _, c := range cli.TaskCandidates(o) {
				out = append(out, cobra.CompletionWithDesc(c.Name, c.Doc))
			}
			return out, cobra.ShellCompDirectiveNoFileComp
		},
		// Resolve streams/context once, before the task path OR any subcommand,
		// so every command observes the same I/O and cancellation.
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			mode, err := parseColorMode(colorFlag)
			if err != nil {
				return &cli.UsageError{Err: err}
			}
			opts.Cwd, _ = os.Getwd()
			opts.Stdin = cmd.InOrStdin()
			opts.Stdout = cmd.OutOrStdout()
			opts.Stderr = cmd.ErrOrStderr()
			// Per-stream color decisions: --list/--help write to stdout, Rune's
			// own messages (status/echo/diagnostics) to stderr. Each uses the TTY
			// status of the stream it targets (FR-004).
			opts.ColorStdout = resolveColor(mode, streamIsTTY(opts.Stdout))
			opts.ColorStderr = resolveColor(mode, streamIsTTY(opts.Stderr))
			opts.Version = version
			opts.Ctx = cmd.Context()
			opts.Commands = subcommandNames(cmd.Root())
			return nil
		},
		// This fires only when no subcommand matched the first positional:
		// built-in commands take precedence; `rune -- <task>` escapes to here.
		// Trailing task flags pass through untouched (see SetInterspersed below).
		RunE: func(cmd *cobra.Command, args []string) error {
			if selfUpgrade {
				if err := upgradeConflict(cmd.Flags()); err != nil {
					return err
				}
				return upgrade(cmd.Context(), opts)
			}
			return cli.Run(*opts, args)
		},
	}

	// Rune ships its own completion command (see newCompletionCmd) so an
	// unsupported shell is a clear error; disable Cobra's default so there is
	// exactly one. The hidden __complete driver is unaffected.
	root.CompletionOptions.DisableDefaultCmd = true

	// Stop global-flag parsing at the first positional so trailing task flags
	// pass through to the task untouched.
	root.Flags().SetInterspersed(false)

	f := root.Flags()
	f.StringVarP(&opts.File, "file", "f", "", "use a specific Runefile instead of upward discovery")
	f.BoolVar(&opts.List, "list", false, "list non-private tasks with docs; run nothing")
	f.BoolVar(&opts.DryRun, "dry-run", false, "print the resolved execution plan; run nothing")
	f.BoolVar(&opts.Summary, "summary", false, "print task names that would run, one per line")
	f.BoolVar(&opts.Dump, "dump", false, "emit the parsed Runefile (canonical text, or JSON)")
	f.StringVar(&opts.DumpFormat, "format", "", "output format for --dump (json)")
	f.StringArrayVar(&opts.Set, "set", nil, "override a variable: --set NAME VALUE")
	f.BoolVar(&opts.Watch, "watch", false, "re-run on file changes")
	f.BoolVar(&opts.Choose, "choose", false, "interactive task picker")
	f.BoolVar(&opts.Yes, "yes", false, "approve [confirm] tasks without prompting (required when stdin is not a terminal)")
	f.BoolVar(&opts.Quiet, "quiet", false, "suppress command echo")
	f.BoolVar(&opts.Fmt, "fmt", false, "rewrite the Runefile in canonical formatting")
	f.BoolVar(&opts.ClearCache, "clear-cache", false, "remove the project-local .rune/cache directory")
	f.BoolVar(&opts.IgnoreVersion, "ignore-version", false, "bypass the Runefile's minimum_version check (prints a warning)")
	f.BoolVar(&selfUpgrade, "self-upgrade", false, "upgrade this rune to the latest stable release; runs no task")
	// --color is persistent so every subcommand (analyze, serve, help, …)
	// inherits the same control — the one gate for all styled surfaces (FR-008).
	root.PersistentFlags().StringVar(&colorFlag, "color", "auto", "when to colorize output: auto|always|never")

	return root
}

// selfUpgradeConflict reports `--self-upgrade` combined with anything but
// --color or --help. The root command enforces the same rule itself; this
// parses a copy of the arguments with a throwaway root before Cobra
// dispatches, because Cobra answers --version, and hands a leading built-in
// command name to that command, before the root's own check can run. A parse
// error is left for the real dispatch to report.
func selfUpgradeConflict(args []string) error {
	// Every ordinary invocation stops here, before a second root is built.
	if !mentionsSelfUpgrade(args) {
		return nil
	}
	probe := newRootCmd(&cli.Options{}, "", "", nil)
	probe.InitDefaultHelpFlag()
	probe.InitDefaultVersionFlag()
	if err := probe.ParseFlags(args); err != nil {
		return nil
	}
	flags := probe.Flags()
	if set, _ := flags.GetBool("self-upgrade"); !set {
		return nil
	}
	// --help keeps its usual precedence: it prints help and starts nothing.
	if help, _ := flags.GetBool("help"); help {
		return nil
	}
	return upgradeConflict(flags)
}

func mentionsSelfUpgrade(args []string) bool {
	for _, a := range args {
		if a == "--self-upgrade" || strings.HasPrefix(a, "--self-upgrade=") {
			return true
		}
	}
	return false
}

// upgradeConflict names what a parsed command line combines with
// --self-upgrade, if anything: only --color (and --help, which Cobra answers
// first) may accompany it.
func upgradeConflict(flags *pflag.FlagSet) error {
	conflict := ""
	if flags.NArg() > 0 {
		conflict = "task arguments"
	}
	// Unsorted, Visit walks the flags in the order they were typed, so the
	// message names the first one.
	sorted := flags.SortFlags
	flags.SortFlags = false
	flags.Visit(func(f *pflag.Flag) {
		if conflict == "" && f.Name != "self-upgrade" && f.Name != "color" {
			conflict = "--" + f.Name
		}
	})
	flags.SortFlags = sorted
	if conflict == "" {
		return nil
	}
	return &cli.UsageError{Err: fmt.Errorf("--self-upgrade cannot be combined with %s", conflict)}
}

// subcommandNames returns the names and aliases of root's visible subcommands,
// used to enrich the "unknown task" error with a did-you-mean suggestion.
func subcommandNames(root *cobra.Command) []string {
	var names []string
	for _, c := range root.Commands() {
		if c.Hidden {
			continue
		}
		names = append(names, c.Name())
		names = append(names, c.Aliases...)
	}
	return names
}
