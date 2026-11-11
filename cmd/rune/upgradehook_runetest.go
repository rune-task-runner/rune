//go:build runetest

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"os"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/rune-task-runner/rune/internal/selfupgrade"
)

// upgradeConfig lets integration tests (which build the binary with
// `-tags runetest`) point a self-upgrade at a local fixture release:
//
//	RUNE_TEST_UPGRADE_API       base URL of the release API
//	RUNE_TEST_UPGRADE_DOWNLOAD  base URL of the release downloads
//	RUNE_TEST_UPGRADE_CA        PEM certificate the HTTP client also trusts
//	RUNE_TEST_UPGRADE_ROOT      Sigstore trusted-root file used in place of the public one
//
// The signer identity is never overridable. This file is NOT compiled into
// production binaries, so a released `rune` honors none of these variables.
func upgradeConfig(cfg selfupgrade.Config) (selfupgrade.Config, error) {
	cfg = productionUpgradeConfig(cfg)
	if caFile := os.Getenv("RUNE_TEST_UPGRADE_CA"); caFile != "" {
		pemBytes, err := os.ReadFile(caFile)
		if err != nil {
			return cfg, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemBytes) {
			return cfg, errors.New("RUNE_TEST_UPGRADE_CA holds no certificate")
		}
		cfg.HTTP = &http.Client{Transport: &http.Transport{
			Proxy:           http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		}}
		cfg.Trust = selfupgrade.PublicTrust(cfg.HTTP)
	}
	if v := os.Getenv("RUNE_TEST_UPGRADE_API"); v != "" {
		cfg.APIBase = v
	}
	if v := os.Getenv("RUNE_TEST_UPGRADE_DOWNLOAD"); v != "" {
		cfg.DownloadBase = v
	}
	if rootFile := os.Getenv("RUNE_TEST_UPGRADE_ROOT"); rootFile != "" {
		trusted, err := root.NewTrustedRootFromPath(rootFile)
		if err != nil {
			return cfg, err
		}
		cfg.Trust = func(context.Context) (root.TrustedMaterial, error) { return trusted, nil }
	}
	return cfg, nil
}
