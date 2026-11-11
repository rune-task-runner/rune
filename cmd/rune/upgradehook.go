//go:build !runetest

package main

import "github.com/rune-task-runner/rune/internal/selfupgrade"

// upgradeConfig returns the production endpoints, trust source and signer
// identity. A released binary has no way to redirect a self-upgrade: the
// RUNE_TEST_UPGRADE_* overrides exist only in binaries built with
// `-tags runetest` (see upgradehook_runetest.go).
func upgradeConfig(cfg selfupgrade.Config) (selfupgrade.Config, error) {
	return productionUpgradeConfig(cfg), nil
}
