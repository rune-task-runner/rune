package upgradetest

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

func get(t *testing.T, r *Release, path string) (int, []byte) {
	t.Helper()
	resp, err := r.Client.Get(r.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return resp.StatusCode, b
}

func tarNames(t *testing.T, b []byte) []string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return names
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
}

func TestReleaseLayout(t *testing.T) {
	r := New(t, Options{})
	code, latest := get(t, r, latestPath)
	if code != 200 || string(latest) != `{"tag_name":"v9.9.9","draft":false,"prerelease":false}` {
		t.Fatalf("latest = %d %s", code, latest)
	}
	_, sums := get(t, r, downloadBase+"v9.9.9/checksums.txt")
	lines := strings.Split(strings.TrimSpace(string(sums)), "\n")
	if len(lines) != 6 {
		t.Fatalf("checksums.txt has %d lines, want 6", len(lines))
	}
	for _, p := range AllPlatforms() {
		name := ArchiveName("9.9.9", p[0], p[1])
		want := fmt.Sprintf("%x  %s", sha256.Sum256(r.Archive(p[0], p[1])), name)
		if !slices.Contains(lines, want) {
			t.Errorf("checksums.txt lacks %q", want)
		}
		_, served := get(t, r, downloadBase+"v9.9.9/"+name)
		if !bytes.Equal(served, r.Archive(p[0], p[1])) {
			t.Errorf("%s: served bytes differ from Archive()", name)
		}
	}
	if got := tarNames(t, r.Archive("linux", "amd64")); !slices.Equal(got, []string{"LICENSE", "README.md", "rune"}) {
		t.Errorf("tar entries = %v", got)
	}
	zr, err := zip.NewReader(bytes.NewReader(r.Archive("windows", "amd64")), int64(len(r.Archive("windows", "amd64"))))
	if err != nil {
		t.Fatal(err)
	}
	var zn []string
	for _, f := range zr.File {
		zn = append(zn, f.Name)
	}
	if !slices.Equal(zn, []string{"LICENSE", "README.md", "rune.exe"}) {
		t.Errorf("zip entries = %v", zn)
	}
	if r.Requests() != 8 || r.ArchiveRequests() != 6 {
		t.Errorf("Requests = %d, ArchiveRequests = %d, want 8 and 6", r.Requests(), r.ArchiveRequests())
	}
	if !bytes.Contains(r.CACertPEM(), []byte("BEGIN CERTIFICATE")) {
		t.Error("CACertPEM is not a PEM certificate")
	}
}

func TestReleaseSignatureVerifies(t *testing.T) {
	r := New(t, Options{})
	var b bundle.Bundle
	if err := b.UnmarshalJSON(r.Bundle()); err != nil {
		t.Fatalf("bundle: %v", err)
	}
	tr, err := root.NewTrustedRootFromJSON(r.TrustRootJSON(t))
	if err != nil {
		t.Fatalf("TrustRootJSON does not parse: %v", err)
	}
	for name, trust := range map[string]root.TrustedMaterial{"in-process": r.Trust, "from JSON": tr} {
		v, err := verify.NewVerifier(trust,
			verify.WithSignedCertificateTimestamps(1), verify.WithTransparencyLog(1), verify.WithObserverTimestamps(1))
		if err != nil {
			t.Fatal(err)
		}
		id, err := verify.NewShortCertificateIdentity(Issuer, "", Subject, "")
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(r.Checksums())
		if _, err := v.Verify(&b, verify.NewPolicy(verify.WithArtifactDigest("sha256", sum[:]), verify.WithCertificateIdentity(id))); err != nil {
			t.Errorf("%s: fixture signature does not verify: %v", name, err)
		}
	}
}

func TestReleaseOptions(t *testing.T) {
	flip := func(b []byte) []byte { c := append([]byte(nil), b...); c[len(c)-1] ^= 0xff; return c }

	clean := New(t, Options{Platforms: [][2]string{{"linux", "amd64"}}})
	mut := New(t, Options{Platforms: [][2]string{{"linux", "amd64"}}, MutateArchive: flip})
	listed := strings.Fields(string(mut.Checksums()))[0]
	if got := fmt.Sprintf("%x", sha256.Sum256(mut.Archive("linux", "amd64"))); got == listed {
		t.Error("MutateArchive: served archive still matches the list")
	}
	if len(strings.Split(strings.TrimSpace(string(clean.Checksums())), "\n")) != 1 {
		t.Error("Platforms: expected one checksum line")
	}

	omit := New(t, Options{OmitSignature: true, OmitChecksums: true})
	if code, _ := get(t, omit, downloadBase+"v9.9.9/checksums.txt"); code != 404 {
		t.Errorf("OmitChecksums: status %d, want 404", code)
	}
	if code, _ := get(t, omit, downloadBase+"v9.9.9/checksums.txt.sigstore.json"); code != 404 {
		t.Errorf("OmitSignature: status %d, want 404", code)
	}

	st := New(t, Options{Status: 403, Header: map[string][]string{"X-Ratelimit-Remaining": {"0"}}})
	resp, err := st.Client.Get(st.URL + latestPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 403 || resp.Header.Get("X-Ratelimit-Remaining") != "0" {
		t.Errorf("Status/Header override: got %d %q", resp.StatusCode, resp.Header.Get("X-Ratelimit-Remaining"))
	}

	pre := New(t, Options{Version: "1.2.3", Prerelease: true, Draft: true})
	if _, b := get(t, pre, latestPath); string(b) != `{"tag_name":"v1.2.3","draft":true,"prerelease":true}` {
		t.Errorf("latest = %s", b)
	}

	extra := New(t, Options{ExtraEntries: []Entry{{Name: "../../evil", Body: []byte("x")}, {Name: "link", Body: []byte("/etc/passwd"), Typeflag: tar.TypeSymlink}}})
	if got := tarNames(t, extra.Archive("linux", "amd64")); !slices.Equal(got, []string{"LICENSE", "README.md", "rune", "../../evil", "link"}) {
		t.Errorf("ExtraEntries: tar entries = %v", got)
	}

	cut := New(t, Options{CutAfter: 10, Binary: bytes.Repeat([]byte("x"), 200000)})
	resp, err = cut.Client.Get(cut.URL + downloadBase + "v9.9.9/" + ArchiveName("9.9.9", "linux", "amd64"))
	if err == nil {
		_, err = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Error("CutAfter: transfer completed without an error")
	}
}
