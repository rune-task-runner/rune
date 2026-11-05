package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/rune-task-runner/rune/internal/config"
	"github.com/rune-task-runner/rune/internal/style"
)

// errorf is a thin wrapper kept package-local so error construction is uniform.
func errorf(format string, args ...any) error { return fmt.Errorf(format, args...) }

// themeStdout builds the styling theme for stdout surfaces (--list, --help).
func (o Options) themeStdout() style.Theme { return style.New(o.ColorStdout, o.Stdout) }

// themeStderr builds the styling theme for stderr surfaces (status/echo/cache,
// diagnostics).
func (o Options) themeStderr() style.Theme { return style.New(o.ColorStderr, o.Stderr) }

// Options carries the resolved global CLI flags and I/O streams for one
// invocation. main.go populates it from cobra and passes it to Run.
type Options struct {
	File       string // -f/--file
	List       bool   // --list
	DryRun     bool   // --dry-run
	Summary    bool   // --summary
	Dump       bool   // --dump
	DumpFormat string // --format (with --dump)
	Set        []string
	Watch      bool
	Choose     bool
	Yes        bool
	Quiet      bool
	Fmt        bool
	ClearCache bool

	// IgnoreVersion bypasses the Runefile's minimum_version gate for this run
	// (the CLI-only --ignore-version flag). It is never settable from a Runefile.
	IgnoreVersion bool
	// MCPAllowIgnoreVersion lets the MCP/agent server run despite an unmet
	// minimum_version. It is an operator-only opt (a `rune serve` flag), disabled
	// by default, and never settable from a Runefile.
	MCPAllowIgnoreVersion bool
	// MCPAllowDestructive holds the raw --allow-destructive values: the
	// [confirm] tasks the operator lets agents run. It is an operator-only
	// `rune serve` flag, never settable from a Runefile; ServeMCP splits and
	// validates it (spec 031).
	MCPAllowDestructive []string

	// Resolved per-stream color decisions (see cmd/rune): ColorStdout gates
	// --list and --help (which write to stdout); ColorStderr gates Rune's own
	// messages — status/echo/cache lines and diagnostics — on stderr.
	ColorStdout bool
	ColorStderr bool
	Version     string
	Cwd         string
	Ctx         context.Context // cancelled on SIGINT (nil => Background)
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer

	// Commands are the reserved subcommand names (and aliases). They are used
	// only to enrich the "unknown task" error with a did-you-mean suggestion.
	Commands []string
}

// ctx returns the invocation context, defaulting to Background.
func (o Options) ctx() context.Context {
	if o.Ctx != nil {
		return o.Ctx
	}
	return context.Background()
}

// Run executes one CLI invocation. args is everything after the global flags:
// VAR=VALUE overrides interleaved with task names and their arguments.
//
// The full pipeline (lex → parse → analyze → schedule → execute) is wired in
// run.go; this function resolves the Runefile and delegates.
func Run(opts Options, args []string) error {
	runefile, err := config.Resolve(opts.File, opts.Cwd)
	if err != nil {
		return &UsageError{Err: err}
	}
	if opts.Choose {
		return chooseAndRun(opts, runefile, args)
	}
	if opts.Watch {
		return watch(opts, runefile, args)
	}
	return execute(opts, runefile, args)
}
