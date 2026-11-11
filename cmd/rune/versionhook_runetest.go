//go:build runetest

package main

import "os"

// installedVersion lets integration tests (which build the binary with
// `-tags runetest`) inject the reported installed version via RUNE_TEST_VERSION,
// so the minimum_version gate can be exercised end-to-end without release-time
// ldflags. This file is NOT compiled into production binaries, so a released
// `rune` never honors the env var and the installed version stays authoritative.
func installedVersion(version string) string {
	if tv := os.Getenv("RUNE_TEST_VERSION"); tv != "" {
		return tv
	}
	return version
}

// installedStamped makes an injected RUNE_TEST_VERSION count as a build-time
// stamp, so a test binary reads as a release build to `--self-upgrade`
// (which declines `go install` builds, recognized by a version that was not
// stamped). RUNE_TEST_UNSTAMPED=1 turns that off for the test that wants a
// `go install` build. Not compiled into production binaries.
func installedStamped(stamped bool) bool {
	if os.Getenv("RUNE_TEST_VERSION") != "" && os.Getenv("RUNE_TEST_UNSTAMPED") != "1" {
		return true
	}
	return stamped
}
