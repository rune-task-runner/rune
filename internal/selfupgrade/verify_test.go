package selfupgrade

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/rune-task-runner/rune/internal/selfupgrade/upgradetest"
)

const ciSubject = "https://github.com/rune-task-runner/rune/.github/workflows/ci.yml@refs/heads/main"

func wantUnverified(t *testing.T, name string, err error) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Reason != ReasonUnverified {
		t.Errorf("%s: err = %v, want an unverified *Error", name, err)
	}
}

func TestVerifyChecksumsFixture(t *testing.T) {
	rel := upgradetest.New(t, upgradetest.Options{})
	sums, bundleJSON := rel.Checksums(), rel.Bundle()

	if err := verifyChecksums(rel.Trust, upgradetest.Issuer, upgradetest.Subject, sums, bundleJSON); err != nil {
		t.Fatalf("valid fixture: %v", err)
	}
	wantUnverified(t, "altered list",
		verifyChecksums(rel.Trust, upgradetest.Issuer, upgradetest.Subject, append(append([]byte(nil), sums...), 'x'), bundleJSON))
	wantUnverified(t, "wrong issuer",
		verifyChecksums(rel.Trust, "https://example.com", upgradetest.Subject, sums, bundleJSON))
	wantUnverified(t, "wrong subject",
		verifyChecksums(rel.Trust, upgradetest.Issuer, ciSubject, sums, bundleJSON))
	for name, b := range map[string]string{"empty": "", "empty object": "{}", "not JSON": "nope", "array": "[]", "null": "null"} {
		wantUnverified(t, "bundle "+name, verifyChecksums(rel.Trust, upgradetest.Issuer, upgradetest.Subject, sums, []byte(b)))
	}

	other := upgradetest.New(t, upgradetest.Options{})
	wantUnverified(t, "foreign trust root",
		verifyChecksums(other.Trust, upgradetest.Issuer, upgradetest.Subject, sums, bundleJSON))

	ci := upgradetest.New(t, upgradetest.Options{Subject: ciSubject})
	wantUnverified(t, "signed by another workflow",
		verifyChecksums(ci.Trust, upgradetest.Issuer, upgradetest.Subject, ci.Checksums(), ci.Bundle()))
}

// The real v0.7.0 release, in cosign's older bundle form, against a snapshot
// of the public trust root and the identity production pins.
func TestVerifyChecksumsRealRelease(t *testing.T) {
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	trust, err := root.NewTrustedRootFromJSON(read("trusted_root.json"))
	if err != nil {
		t.Fatal(err)
	}
	sums := read("v0.7.0/checksums.txt")
	bundleJSON := read("v0.7.0/checksums.txt.sigstore.json")

	if err := verifyChecksums(trust, upgradetest.Issuer, upgradetest.Subject, sums, bundleJSON); err != nil {
		t.Fatalf("real v0.7.0 release does not verify: %v", err)
	}
	wantUnverified(t, "altered real list",
		verifyChecksums(trust, upgradetest.Issuer, upgradetest.Subject, append(append([]byte(nil), sums...), 'x'), bundleJSON))
	wantUnverified(t, "real list, other workflow",
		verifyChecksums(trust, upgradetest.Issuer, ciSubject, sums, bundleJSON))
	wantUnverified(t, "real list, other issuer",
		verifyChecksums(trust, "https://example.com", upgradetest.Subject, sums, bundleJSON))

	fixture := upgradetest.New(t, upgradetest.Options{})
	wantUnverified(t, "real list, fixture trust root",
		verifyChecksums(fixture.Trust, upgradetest.Issuer, upgradetest.Subject, sums, bundleJSON))
}

func TestLegacyBundleRejectsMalformed(t *testing.T) {
	for name, b := range map[string]string{
		"missing cert":      `{"base64Signature":"AA==","rekorBundle":{}}`,
		"cert not base64":   `{"base64Signature":"AA==","cert":"%%%","rekorBundle":{"SignedEntryTimestamp":"AA==","Payload":{"body":"AA==","logID":"00"}}}`,
		"cert not PEM":      `{"base64Signature":"AA==","cert":"AAAA","rekorBundle":{"SignedEntryTimestamp":"AA==","Payload":{"body":"AA==","logID":"00"}}}`,
		"log id not hex":    `{"base64Signature":"AA==","cert":"LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCkFBQUEKLS0tLS1FTkQgQ0VSVElGSUNBVEUtLS0tLQo=","rekorBundle":{"SignedEntryTimestamp":"AA==","Payload":{"body":"AA==","logID":"zz"}}}`,
		"signature missing": `{"cert":"AAAA","rekorBundle":{}}`,
	} {
		if _, err := loadBundle([]byte(b), []byte("x")); err == nil {
			t.Errorf("%s: loadBundle accepted a malformed legacy bundle", name)
		}
	}
}
