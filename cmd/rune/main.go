// Command rune is a shared task runner for humans and AI agents. It parses a
// Runefile, statically validates it, and runs tasks — from the CLI or, via MCP,
// from agents and IDEs.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/rune-task-runner/rune/internal/cli"
)

// Build metadata, overridden via -ldflags at release time.
var (
	version = devVersion
	commit  = "none"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	// Cancel running tasks on SIGINT/SIGTERM; the scheduler/executors observe
	// the context and child processes are terminated (exit 130).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var opts cli.Options

	// The reported version is the build-stamped one in production, with a
	// build-info fallback so `go install …@version` binaries report their
	// release too (see resolveVersion). Only a binary built with
	// `-tags runetest` lets integration tests override the result via
	// RUNE_TEST_VERSION (see versionhook*.go). A released binary's version is
	// authoritative and cannot be spoofed at runtime.
	installed := installedVersion(resolveVersion(version))
	// Release builds stamp main.version; a `go install` build reports a
	// release without one. --self-upgrade tells the two apart by this.
	stamped := installedStamped(version != devVersion)
	root := newRootCmd(&opts, installed, commit, func(ctx context.Context, o *cli.Options) error {
		return runSelfUpgrade(ctx, o, installed, stamped)
	})
	// Built-in subcommands. Registering any subcommand also makes Cobra add its
	// `help` command automatically; `completion` is our own (newCompletionCmd).
	root.AddCommand(newServeCmd(&opts), newVersionCmd(&opts), newCompletionCmd(), newAnalyzeCmd(&opts), newLSPCmd(&opts))
	applyHelp(root)

	// Rune's own messages go to stderr so stdout stays clean for piping.
	root.SetOut(os.Stdout)
	root.SetErr(os.Stderr)
	root.SetArgs(args)

	err := selfUpgradeConflict(args)
	if err == nil {
		err = root.ExecuteContext(ctx)
	}
	// BannerMessage suppresses banners already rendered by the pipeline
	// (validation diagnostics) or intentionally silenced ([no-exit-message]).
	// The banner runs after Execute returned, where PersistentPreRunE (the
	// normal color-resolution point) may never have fired on a flag-parse
	// error; tolerantTheme re-resolves --color so an explicit choice still
	// governs. Only the "rune:" prefix is styled; the message stays plain (C1).
	if msg := cli.BannerMessage(err); msg != "" {
		fmt.Fprintln(os.Stderr, cli.BannerLine(tolerantTheme(root, os.Stderr), msg))
	}
	return cli.CodeFor(err)
}
