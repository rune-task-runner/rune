package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/rune-task-runner/rune/internal/cli"
	"github.com/rune-task-runner/rune/internal/selfupgrade"
	"github.com/rune-task-runner/rune/internal/style"
)

func report(res selfupgrade.Result, err error) (string, error) {
	var out bytes.Buffer
	got := reportUpgrade(&out, style.New(false, &out), res, err)
	return out.String(), got
}

func TestReportUpgradeOutcomes(t *testing.T) {
	out, err := report(selfupgrade.Result{Outcome: selfupgrade.OutcomeUpgraded, Installed: "0.7.0", Target: "9.9.9", Path: "/x/rune"}, nil)
	if err != nil || out != "upgraded: 0.7.0 -> 9.9.9 (/x/rune)\n" {
		t.Errorf("upgraded: %q, %v", out, err)
	}
	out, err = report(selfupgrade.Result{Outcome: selfupgrade.OutcomeCurrent, Installed: "9.9.9", Target: "9.9.9"}, nil)
	if err != nil || out != "current: nothing to upgrade\n" {
		t.Errorf("current: %q, %v", out, err)
	}
}

func TestReportUpgradeFailures(t *testing.T) {
	failure := &selfupgrade.Error{Reason: selfupgrade.ReasonChecksumMismatch, Subject: "a.tar.gz"}
	tests := map[string]struct {
		err  error
		want string
	}{
		"expected failure": {failure, "self-upgrade: a.tar.gz does not match the signed checksum; nothing was installed"},
		"wrapped failure":  {fmt.Errorf("ctx: %w", failure), "self-upgrade: ctx: a.tar.gz does not match the signed checksum; nothing was installed"},
		"unknown error":    {errors.New("boom"), "self-upgrade: boom"},
	}
	for name, tt := range tests {
		out, err := report(selfupgrade.Result{Installed: "0.7.0"}, tt.err)
		var cf *cli.CommandFailure
		if !errors.As(err, &cf) || err.Error() != tt.want {
			t.Errorf("%s: err = %v (%T), want CommandFailure %q", name, err, err, tt.want)
		}
		if cli.CodeFor(err) != cli.ExitTaskFail {
			t.Errorf("%s: exit code %d, want %d", name, cli.CodeFor(err), cli.ExitTaskFail)
		}
		if out != "" {
			t.Errorf("%s: printed %q; the banner prints failures", name, out)
		}
	}
}

func TestReportUpgradeCancelled(t *testing.T) {
	for name, cause := range map[string]error{"bare": context.Canceled, "wrapped": fmt.Errorf("download: %w", context.Canceled)} {
		_, err := report(selfupgrade.Result{}, cause)
		var in *cli.Interrupted
		if !errors.As(err, &in) || cli.CodeFor(err) != cli.ExitInterrupt {
			t.Errorf("%s: err = %v (%T), want Interrupted", name, err, err)
		}
	}
}

func TestReportUpgradeRefused(t *testing.T) {
	refusal := &selfupgrade.Refused{Origin: selfupgrade.OriginGoInstall}
	out, err := report(selfupgrade.Result{Installed: "0.7.0"}, refusal)
	var cf *cli.CommandFailure
	want := "self-upgrade: this rune was built with go install; run: go install github.com/rune-task-runner/rune/cmd/rune@latest"
	if !errors.As(err, &cf) || err.Error() != want || cli.CodeFor(err) != cli.ExitTaskFail {
		t.Errorf("err = %v (%T), want CommandFailure %q", err, err, want)
	}
	if out != "" {
		t.Errorf("printed %q", out)
	}
}
