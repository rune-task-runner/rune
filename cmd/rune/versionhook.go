//go:build !runetest

package main

// installedVersion returns the build-stamped version. In production builds the
// installed version is authoritative and cannot be overridden at runtime — the
// RUNE_TEST_VERSION hook exists only in binaries built with `-tags runetest`
// (see versionhook_runetest.go), never in a released binary.
func installedVersion(version string) string { return version }

// installedStamped reports whether the version was stamped at build time, as
// release builds are. Like installedVersion, only a `-tags runetest` binary
// can override it.
func installedStamped(stamped bool) bool { return stamped }
