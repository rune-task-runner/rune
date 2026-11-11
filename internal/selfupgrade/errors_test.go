package selfupgrade

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrorMessages(t *testing.T) {
	cause := errors.New("boom")
	tests := []struct {
		err  *Error
		want string
	}{
		{&Error{Reason: ReasonOffline, Subject: "api.github.com", Err: cause}, "cannot reach api.github.com: boom; check the network connection and try again"},
		{&Error{Reason: ReasonTimeout, Subject: "github.com", Limit: "15s"}, "github.com did not answer within 15s; try again later"},
		{&Error{Reason: ReasonRateLimited, Until: "14:05:00 UTC"}, "the release service rate limit is used up until 14:05:00 UTC; try again then, or download from https://github.com/rune-task-runner/rune/releases/latest"},
		{&Error{Reason: ReasonReleaseLookup, Status: 500}, "unexpected answer from the release service: HTTP 500"},
		{&Error{Reason: ReasonReleaseLookup, Err: cause}, "unexpected answer from the release service: boom"},
		{&Error{Reason: ReasonUnsupportedPlatform, Target: "0.8.0", Subject: "plan9/amd64"}, "release 0.8.0 has no archive for plan9/amd64; build from source: https://github.com/rune-task-runner/rune/blob/main/docs/installation.md#from-source-go"},
		{&Error{Reason: ReasonUnverified, Target: "0.8.0", Err: cause}, "release 0.8.0 could not be verified: boom; nothing was installed"},
		{&Error{Reason: ReasonDownloadFailed, Subject: "checksums.txt", Status: 404}, "could not download checksums.txt: HTTP 404; nothing was installed"},
		{&Error{Reason: ReasonDownloadFailed, Subject: "a.tar.gz", Err: cause}, "could not download a.tar.gz: boom; nothing was installed"},
		{&Error{Reason: ReasonChecksumMismatch, Subject: "a.tar.gz"}, "a.tar.gz does not match the signed checksum; nothing was installed"},
		{&Error{Reason: ReasonTooLarge, Subject: "a.tar.gz", Limit: "100 MiB"}, "a.tar.gz is larger than 100 MiB; nothing was installed"},
		{&Error{Reason: ReasonBadArchive, Subject: "a.tar.gz"}, "a.tar.gz does not contain exactly one rune binary; nothing was installed"},
		{&Error{Reason: ReasonNotWritable, Subject: "/usr/local/bin", Err: cause}, "cannot write to /usr/local/bin: boom; rune does not ask for elevated rights. Rerun with the rights needed for that directory, or download from https://github.com/rune-task-runner/rune/releases/latest"},
		{&Error{Reason: ReasonInProgress, Subject: "/usr/local/bin/rune"}, "another self-upgrade of /usr/local/bin/rune is running; try again when it finishes"},
		{&Error{Reason: ReasonReplaceFailed, Subject: "/usr/local/bin/rune", Err: cause}, "could not replace /usr/local/bin/rune: boom; the installed rune is unchanged"},
	}
	// A replacement that could not be rolled back must not claim nothing changed.
	tests = append(tests, struct {
		err  *Error
		want string
	}{
		&Error{Reason: ReasonReplaceFailed, Subject: `C:\bin\rune.exe`, Err: &rollbackError{err: cause, old: `C:\bin\rune.exe.old`}},
		`could not replace C:\bin\rune.exe: boom; it is now missing. Restore it by renaming C:\bin\rune.exe.old back to C:\bin\rune.exe`,
	})
	seen := map[Reason]bool{}
	for _, tt := range tests {
		seen[tt.err.Reason] = true
		if got := tt.err.Error(); got != tt.want {
			t.Errorf("%s:\n got  %q\n want %q", tt.err.Reason, got, tt.want)
		}
		var e *Error
		if !errors.As(fmt.Errorf("wrapped: %w", tt.err), &e) {
			t.Errorf("%s: errors.As did not find *Error", tt.err.Reason)
		}
	}
	for _, r := range []Reason{
		"offline", "timeout", "rate-limited", "release-lookup", "unsupported-platform", "unverified",
		"checksum-mismatch", "download-failed", "too-large", "bad-archive", "not-writable", "in-progress",
		"replace-failed",
	} {
		if !seen[r] {
			t.Errorf("reason %q has no message test", r)
		}
	}
}

func TestErrorUnwrap(t *testing.T) {
	cause := errors.New("boom")
	if !errors.Is(&Error{Reason: ReasonOffline, Err: cause}, cause) {
		t.Error("Error does not unwrap to its cause")
	}
}

func TestRollbackErrorUnwrap(t *testing.T) {
	cause := errors.New("boom")
	err := &rollbackError{err: cause, old: "x.old"}
	if !errors.Is(err, cause) || err.Error() != "boom" {
		t.Errorf("rollbackError = %q, unwraps to cause: %v", err.Error(), errors.Is(err, cause))
	}
}

func TestFormatSize(t *testing.T) {
	for n, want := range map[int64]string{1 << 20: "1 MiB", 100 << 20: "100 MiB", 10: "10 bytes", 1<<20 + 1: "1048577 bytes"} {
		if got := formatSize(n); got != want {
			t.Errorf("formatSize(%d) = %q, want %q", n, got, want)
		}
	}
}
