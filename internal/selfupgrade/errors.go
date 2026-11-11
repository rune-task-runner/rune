// Package selfupgrade replaces the running rune binary with the latest stable
// release. Nothing is extracted or installed until the release's checksum list
// is proven to be signed by the release workflow and the downloaded archive
// matches it.
package selfupgrade

import (
	"errors"
	"fmt"
)

const (
	releasesURL = "https://github.com/rune-task-runner/rune/releases/latest"
	sourceURL   = "https://github.com/rune-task-runner/rune/blob/main/docs/installation.md#from-source-go"
)

// Reason names why an upgrade did not happen. Each has its own message.
type Reason string

const (
	ReasonOffline             Reason = "offline"
	ReasonTimeout             Reason = "timeout"
	ReasonRateLimited         Reason = "rate-limited"
	ReasonReleaseLookup       Reason = "release-lookup"
	ReasonUnsupportedPlatform Reason = "unsupported-platform"
	ReasonUnverified          Reason = "unverified"
	ReasonChecksumMismatch    Reason = "checksum-mismatch"
	ReasonDownloadFailed      Reason = "download-failed"
	ReasonTooLarge            Reason = "too-large"
	ReasonBadArchive          Reason = "bad-archive"
	ReasonNotWritable         Reason = "not-writable"
	ReasonInProgress          Reason = "in-progress"
	ReasonReplaceFailed       Reason = "replace-failed"
)

// Error is an expected failure of an upgrade. The installed binary is
// unchanged whenever one is returned, with one exception the message spells
// out: a Windows replacement that failed and could not be rolled back.
type Error struct {
	Reason    Reason
	Installed string
	Target    string
	// Subject is what the message is about: a host, a file, a directory, a
	// path, or an os/arch pair, depending on Reason.
	Subject string
	Limit   string
	Until   string
	// Status is the HTTP status behind a failed request, when there was one.
	Status int
	Err    error
}

func (e *Error) Unwrap() error { return e.Err }

func (e *Error) Error() string {
	switch e.Reason {
	case ReasonOffline:
		return fmt.Sprintf("cannot reach %s: %s; check the network connection and try again", e.Subject, e.cause())
	case ReasonTimeout:
		return fmt.Sprintf("%s did not answer within %s; try again later", e.Subject, e.Limit)
	case ReasonRateLimited:
		return fmt.Sprintf("the release service rate limit is used up until %s; try again then, or download from %s", e.Until, releasesURL)
	case ReasonReleaseLookup:
		return fmt.Sprintf("unexpected answer from the release service: %s", e.cause())
	case ReasonUnsupportedPlatform:
		return fmt.Sprintf("release %s has no archive for %s; build from source: %s", e.Target, e.Subject, sourceURL)
	case ReasonUnverified:
		return fmt.Sprintf("release %s could not be verified: %s; nothing was installed", e.Target, e.cause())
	case ReasonDownloadFailed:
		return fmt.Sprintf("could not download %s: %s; nothing was installed", e.Subject, e.cause())
	case ReasonChecksumMismatch:
		return fmt.Sprintf("%s does not match the signed checksum; nothing was installed", e.Subject)
	case ReasonTooLarge:
		return fmt.Sprintf("%s is larger than %s; nothing was installed", e.Subject, e.Limit)
	case ReasonBadArchive:
		return fmt.Sprintf("%s does not contain exactly one rune binary; nothing was installed", e.Subject)
	case ReasonNotWritable:
		return fmt.Sprintf("cannot write to %s: %s; rune does not ask for elevated rights. Rerun with the rights needed for that directory, or download from %s", e.Subject, e.cause(), releasesURL)
	case ReasonInProgress:
		return fmt.Sprintf("another self-upgrade of %s is running; try again when it finishes", e.Subject)
	case ReasonReplaceFailed:
		var rb *rollbackError
		if errors.As(e.Err, &rb) {
			return fmt.Sprintf("could not replace %s: %s; it is now missing. Restore it by renaming %s back to %s", e.Subject, e.cause(), rb.old, e.Subject)
		}
		return fmt.Sprintf("could not replace %s: %s; the installed rune is unchanged", e.Subject, e.cause())
	}
	return fmt.Sprintf("%s: %s", e.Reason, e.cause())
}

// cause is the wrapped error's text, or the HTTP status when there is none.
func (e *Error) cause() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	if e.Status != 0 {
		return fmt.Sprintf("HTTP %d", e.Status)
	}
	return "unknown cause"
}

// formatSize renders a byte limit for a message.
func formatSize(n int64) string {
	if n >= 1<<20 && n%(1<<20) == 0 {
		return fmt.Sprintf("%d MiB", n>>20)
	}
	return fmt.Sprintf("%d bytes", n)
}
