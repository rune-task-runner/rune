package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/rune-task-runner/rune/internal/cli"
	"github.com/rune-task-runner/rune/internal/selfupgrade"
	"github.com/rune-task-runner/rune/internal/style"
)

// The only signer a self-upgrade accepts: the release workflow on main, as
// certified by GitHub Actions. Renaming that workflow or releasing from
// another ref strands every installed binary (see docs/releasing.md).
const (
	upgradeIssuer  = "https://token.actions.githubusercontent.com"
	upgradeSubject = "https://github.com/rune-task-runner/rune/.github/workflows/release.yml@refs/heads/main"
)

// upgradeFunc performs the self-upgrade; the root command receives it so
// tests can substitute a stub.
type upgradeFunc func(ctx context.Context, opts *cli.Options) error

// productionUpgradeConfig fills in where releases live and whom to trust.
func productionUpgradeConfig(cfg selfupgrade.Config) selfupgrade.Config {
	client := &http.Client{}
	cfg.HTTP = client
	cfg.APIBase = "https://api.github.com"
	cfg.DownloadBase = "https://github.com"
	cfg.Trust = selfupgrade.PublicTrust(client)
	cfg.Issuer = upgradeIssuer
	cfg.Subject = upgradeSubject
	return cfg
}

// runSelfUpgrade upgrades the running binary and reports what happened.
// Progress goes to stderr like Rune's other messages.
func runSelfUpgrade(ctx context.Context, opts *cli.Options, version string, stamped bool) error {
	exe, err := os.Executable()
	if err != nil {
		return &cli.CommandFailure{Err: fmt.Errorf("self-upgrade: cannot locate the running executable: %w", err)}
	}
	stderr := opts.Stderr
	cfg, err := upgradeConfig(selfupgrade.Config{
		Version: version,
		Stamped: stamped,
		ExePath: exe,
		GOOS:    runtime.GOOS,
		GOARCH:  runtime.GOARCH,
		Getenv:  os.Getenv,
		Now:     time.Now,
		Logf: func(format string, args ...any) {
			fmt.Fprintf(stderr, "rune: "+format+"\n", args...)
		},
	})
	if err != nil {
		return &cli.CommandFailure{Err: fmt.Errorf("self-upgrade: %w", err)}
	}
	u, err := selfupgrade.New(cfg)
	if err != nil {
		return &cli.CommandFailure{Err: fmt.Errorf("self-upgrade: %w", err)}
	}
	res, err := u.Run(ctx)
	return reportUpgrade(stderr, style.New(opts.ColorStderr, stderr), res, err)
}

// reportUpgrade prints the outcome line of a successful run and maps a
// failure to its exit code: 130 when cancelled, 1 for everything else. No
// failure may fall through to the usage exit code.
func reportUpgrade(w io.Writer, th style.Theme, res selfupgrade.Result, err error) error {
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return &cli.Interrupted{Err: errors.New("self-upgrade interrupted; the installed rune is unchanged")}
		}
		return &cli.CommandFailure{Err: fmt.Errorf("self-upgrade: %w", err)}
	}
	switch res.Outcome {
	case selfupgrade.OutcomeUpgraded:
		fmt.Fprintf(w, "%s %s -> %s (%s)\n", th.Success.Render("upgraded:"), res.Installed, res.Target, res.Path)
	case selfupgrade.OutcomeCurrent:
		fmt.Fprintf(w, "%s nothing to upgrade\n", th.Success.Render("current:"))
	}
	return nil
}
