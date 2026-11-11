// Package upgradetest builds a signed fixture release on a local TLS server,
// in the layout a real Rune release has. It exists so self-upgrade tests at
// every layer run without the network. Only tests import it.
package upgradetest

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"
)

// The identity the production updater pins. Fixtures sign with it by default.
const (
	Issuer  = "https://token.actions.githubusercontent.com"
	Subject = "https://github.com/rune-task-runner/rune/.github/workflows/release.yml@refs/heads/main"
)

const (
	latestPath   = "/repos/rune-task-runner/rune/releases/latest"
	downloadBase = "/rune-task-runner/rune/releases/download/"
	chunkSize    = 32 << 10
)

// Entry is an extra archive member. A zero Typeflag means a regular file.
type Entry struct {
	Name     string
	Body     []byte
	Typeflag byte
}

// Options shapes the fixture. The zero value is a valid, correctly signed
// release 9.9.9 for all six published platform pairs.
type Options struct {
	Version   string
	Platforms [][2]string
	Binary    []byte
	Subject   string

	Prerelease, Draft            bool
	OmitSignature, OmitChecksums bool

	// MutateArchive and MutateChecksums change what is served after signing,
	// so the result no longer matches the signature or the list.
	MutateArchive   func([]byte) []byte
	MutateChecksums func([]byte) []byte
	ExtraEntries    []Entry

	// HoldArchive, while open, keeps archive requests unanswered.
	HoldArchive chan struct{}
	// OnArchive runs when an archive request starts being served.
	OnArchive       func()
	LatencyPerChunk time.Duration
	// CutAfter closes the connection after that many archive body bytes.
	CutAfter int64

	// Status and Header override the answer to the latest-release request.
	Status int
	Header http.Header
}

// Release is a running fixture.
type Release struct {
	// URL serves both the release API and the downloads.
	URL    string
	Client *http.Client
	Trust  root.TrustedMaterial

	opts      Options
	auth      *authority
	srv       *httptest.Server
	files     map[string][]byte
	archives  map[string]bool
	requests  atomic.Int64
	archiveRq atomic.Int64
}

// AllPlatforms lists the pairs a real release publishes.
func AllPlatforms() [][2]string {
	return [][2]string{
		{"linux", "amd64"},
		{"linux", "arm64"},
		{"darwin", "amd64"},
		{"darwin", "arm64"},
		{"windows", "amd64"},
		{"windows", "arm64"},
	}
}

// ArchiveName is the published archive name for one platform.
func ArchiveName(version, goos, goarch string) string {
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("rune_%s_%s_%s.%s", version, goos, goarch, ext)
}

// New builds and serves a release, and stops it when the test ends.
func New(t testing.TB, o Options) *Release {
	t.Helper()
	if o.Version == "" {
		o.Version = "9.9.9"
	}
	if o.Platforms == nil {
		o.Platforms = AllPlatforms()
	}
	if o.Binary == nil {
		o.Binary = []byte("#!/bin/sh\necho fixture rune " + o.Version + "\n")
	}
	if o.Subject == "" {
		o.Subject = Subject
	}

	auth, err := newAuthority()
	if err != nil {
		t.Fatalf("upgradetest: authority: %v", err)
	}
	r := &Release{opts: o, auth: auth, Trust: auth.trust, files: map[string][]byte{}, archives: map[string]bool{}}

	var sums bytes.Buffer
	for _, p := range o.Platforms {
		name := ArchiveName(o.Version, p[0], p[1])
		var body []byte
		if p[0] == "windows" {
			body, err = buildZip(o.Binary, o.ExtraEntries)
		} else {
			body, err = buildTarGz(o.Binary, o.ExtraEntries)
		}
		if err != nil {
			t.Fatalf("upgradetest: build %s: %v", name, err)
		}
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256(body), name)
		if o.MutateArchive != nil {
			body = o.MutateArchive(body)
		}
		r.files[name] = body
		r.archives[name] = true
	}

	signed := sums.Bytes()
	sig, err := auth.sign(o.Subject, Issuer, signed)
	if err != nil {
		t.Fatalf("upgradetest: sign: %v", err)
	}
	served := signed
	if o.MutateChecksums != nil {
		served = o.MutateChecksums(append([]byte(nil), signed...))
	}
	if !o.OmitChecksums {
		r.files["checksums.txt"] = served
	}
	if !o.OmitSignature {
		r.files["checksums.txt.sigstore.json"] = sig
	}

	r.srv = httptest.NewTLSServer(http.HandlerFunc(r.serve))
	t.Cleanup(r.srv.Close)
	r.URL = r.srv.URL
	r.Client = r.srv.Client()
	return r
}

// Requests is the number of requests the fixture has received.
func (r *Release) Requests() int { return int(r.requests.Load()) }

// ArchiveRequests is the number of archive requests received.
func (r *Release) ArchiveRequests() int { return int(r.archiveRq.Load()) }

// Archive returns the served bytes of one platform's archive.
func (r *Release) Archive(goos, goarch string) []byte {
	return r.files[ArchiveName(r.opts.Version, goos, goarch)]
}

// Checksums returns the served checksum list.
func (r *Release) Checksums() []byte { return r.files["checksums.txt"] }

// Bundle returns the served signature file.
func (r *Release) Bundle() []byte { return r.files["checksums.txt.sigstore.json"] }

// CACertPEM is the certificate a client must trust to reach the fixture.
func (r *Release) CACertPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: r.srv.Certificate().Raw})
}

// TrustRootJSON is the fixture's trust material as a Sigstore trusted-root
// file, for binaries that load their trust from disk.
func (r *Release) TrustRootJSON(t testing.TB) []byte {
	t.Helper()
	b, err := r.auth.trust.MarshalJSON()
	if err != nil {
		t.Fatalf("upgradetest: marshal trusted root: %v", err)
	}
	return b
}

func (r *Release) serve(w http.ResponseWriter, req *http.Request) {
	r.requests.Add(1)
	if req.URL.Path == latestPath {
		r.serveLatest(w)
		return
	}
	prefix := downloadBase + "v" + r.opts.Version + "/"
	name, ok := strings.CutPrefix(req.URL.Path, prefix)
	body, found := r.files[name]
	if !ok || !found {
		http.NotFound(w, req)
		return
	}
	if !r.archives[name] {
		_, _ = w.Write(body)
		return
	}
	r.serveArchive(w, req, body)
}

func (r *Release) serveLatest(w http.ResponseWriter) {
	for k, vs := range r.opts.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	if r.opts.Status != 0 {
		w.WriteHeader(r.opts.Status)
		_, _ = w.Write([]byte(`{"message":"fixture status"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"tag_name":"v%s","draft":%t,"prerelease":%t}`, r.opts.Version, r.opts.Draft, r.opts.Prerelease)
}

func (r *Release) serveArchive(w http.ResponseWriter, req *http.Request, body []byte) {
	r.archiveRq.Add(1)
	if r.opts.HoldArchive != nil {
		select {
		case <-r.opts.HoldArchive:
		case <-req.Context().Done():
			return
		}
	}
	if r.opts.OnArchive != nil {
		r.opts.OnArchive()
	}
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	flusher, _ := w.(http.Flusher)
	var sent int64
	for len(body) > 0 {
		n := min(len(body), chunkSize)
		if r.opts.CutAfter > 0 && sent+int64(n) > r.opts.CutAfter {
			n = int(r.opts.CutAfter - sent)
			_, _ = w.Write(body[:n])
			if flusher != nil {
				flusher.Flush()
			}
			// Drop the connection mid-body so the client sees a short transfer.
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					_ = conn.Close()
				}
			}
			return
		}
		if _, err := w.Write(body[:n]); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
		sent += int64(n)
		body = body[n:]
		if r.opts.LatencyPerChunk > 0 {
			select {
			case <-time.After(r.opts.LatencyPerChunk):
			case <-req.Context().Done():
				return
			}
		}
	}
}

func buildTarGz(binary []byte, extra []Entry) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	entries := append([]Entry{
		{Name: "LICENSE", Body: []byte("license\n")},
		{Name: "README.md", Body: []byte("readme\n")},
		{Name: "rune", Body: binary},
	}, extra...)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.Name, Mode: 0o644, Typeflag: tar.TypeReg, Size: int64(len(e.Body))}
		if e.Name == "rune" {
			hdr.Mode = 0o755
		}
		if e.Typeflag != 0 {
			hdr.Typeflag = e.Typeflag
		}
		if hdr.Typeflag != tar.TypeReg {
			hdr.Size = 0
			hdr.Linkname = string(e.Body)
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if hdr.Typeflag == tar.TypeReg {
			if _, err := tw.Write(e.Body); err != nil {
				return nil, err
			}
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func buildZip(binary []byte, extra []Entry) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	entries := append([]Entry{
		{Name: "LICENSE", Body: []byte("license\n")},
		{Name: "README.md", Body: []byte("readme\n")},
		{Name: "rune.exe", Body: binary},
	}, extra...)
	for _, e := range entries {
		w, err := zw.Create(e.Name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(e.Body); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
