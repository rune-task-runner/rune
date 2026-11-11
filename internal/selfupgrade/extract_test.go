package selfupgrade

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type tarEntry struct {
	name string
	body string
	typ  byte
}

func writeTarGz(t *testing.T, entries ...tarEntry) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: 0o755, Typeflag: tar.TypeReg, Size: int64(len(e.body))}
		if e.typ != 0 {
			h.Typeflag, h.Size, h.Linkname = e.typ, 0, e.body
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return writeFile(t, "a.tar.gz", buf.Bytes())
}

func writeZip(t *testing.T, names ...string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("body of " + n)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return writeFile(t, "a.zip", buf.Bytes())
}

func writeFile(t *testing.T, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func wantReason(t *testing.T, name string, err error, want Reason) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Reason != want {
		t.Errorf("%s: err = %v, want reason %q", name, err, want)
	}
}

func onlyFile(t *testing.T, archive string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(archive))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("extraction created files next to the archive: %v", entries)
	}
}

func TestExtractBinaryTarGz(t *testing.T) {
	p := writeTarGz(t, tarEntry{name: "LICENSE", body: "l"}, tarEntry{name: "README.md", body: "r"}, tarEntry{name: "rune", body: "the binary"})
	var out bytes.Buffer
	if err := extractPath(t, p, "linux", &out, 1<<20); err != nil {
		t.Fatal(err)
	}
	if out.String() != "the binary" {
		t.Errorf("extracted %q", out.String())
	}
	onlyFile(t, p)
}

func TestExtractBinaryZip(t *testing.T) {
	p := writeZip(t, "LICENSE", "README.md", "rune.exe")
	var out bytes.Buffer
	if err := extractPath(t, p, "windows", &out, 1<<20); err != nil {
		t.Fatal(err)
	}
	if out.String() != "body of rune.exe" {
		t.Errorf("extracted %q", out.String())
	}
	onlyFile(t, p)
}

func TestExtractBinaryHostileEntriesAreIgnored(t *testing.T) {
	p := writeTarGz(t,
		tarEntry{name: "../../etc/passwd", body: "evil"},
		tarEntry{name: "link", body: "/etc/passwd", typ: tar.TypeSymlink},
		tarEntry{name: "rune", body: "the binary"},
		tarEntry{name: "/abs", body: "evil"})
	var out bytes.Buffer
	if err := extractPath(t, p, "linux", &out, 1<<20); err != nil {
		t.Fatal(err)
	}
	if out.String() != "the binary" {
		t.Errorf("extracted %q", out.String())
	}
	onlyFile(t, p)
}

func TestExtractBinaryRejects(t *testing.T) {
	tests := map[string]struct {
		path string
		goos string
		want Reason
	}{
		"no binary":         {writeTarGz(t, tarEntry{name: "LICENSE", body: "l"}), "linux", ReasonBadArchive},
		"two binaries":      {writeTarGz(t, tarEntry{name: "rune", body: "a"}, tarEntry{name: "rune", body: "b"}), "linux", ReasonBadArchive},
		"binary is symlink": {writeTarGz(t, tarEntry{name: "rune", body: "/bin/sh", typ: tar.TypeSymlink}), "linux", ReasonBadArchive},
		"binary is dir":     {writeTarGz(t, tarEntry{name: "rune", typ: tar.TypeDir}), "linux", ReasonBadArchive},
		"lookalike names": {writeTarGz(t, tarEntry{name: "../rune", body: "x"}, tarEntry{name: "/rune", body: "x"},
			tarEntry{name: "./rune", body: "x"}, tarEntry{name: "dir/rune", body: "x"}, tarEntry{name: "rune.exe", body: "x"}), "linux", ReasonBadArchive},
		"too large":      {writeTarGz(t, tarEntry{name: "rune", body: "0123456789ABCDEF"}), "linux", ReasonTooLarge},
		"truncated gzip": {writeFile(t, "a.tar.gz", []byte{0x1f, 0x8b, 0x08, 0x00, 0x00}), "linux", ReasonBadArchive},
		"not gzip":       {writeFile(t, "a.tar.gz", []byte("plain text")), "linux", ReasonBadArchive},
		"zip no binary":  {writeZip(t, "LICENSE", "rune"), "windows", ReasonBadArchive},
		"zip two":        {writeZip(t, "rune.exe", "rune.exe"), "windows", ReasonBadArchive},
		"zip lookalike":  {writeZip(t, "dir/rune.exe", "../rune.exe"), "windows", ReasonBadArchive},
		"not a zip":      {writeFile(t, "a.zip", []byte("plain text")), "windows", ReasonBadArchive},
	}
	for name, tt := range tests {
		var out bytes.Buffer
		err := extractPath(t, tt.path, tt.goos, &out, 10)
		wantReason(t, name, err, tt.want)
		if out.Len() > 10 {
			t.Errorf("%s: wrote %d bytes past the 10 byte limit", name, out.Len())
		}
	}

	big := writeZip(t, "rune.exe")
	var out bytes.Buffer
	wantReason(t, "zip too large", extractPath(t, big, "windows", &out, 4), ReasonTooLarge)
}

// extractPath opens the archive the way install holds it: as an open file
// whose offset is wherever the download left it.
func extractPath(t *testing.T, path, goos string, dst io.Writer, limit int64) error {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		t.Fatal(err)
	}
	return extractBinary(f, goos, dst, limit)
}

// A fault on this machine must not be reported as a malformed release.
func TestExtractBinaryLocalFaults(t *testing.T) {
	// A staged archive that cannot be read back is not a malformed release.
	closed, err := os.Open(writeTarGz(t, tarEntry{name: "rune", body: "binary"}))
	if err != nil {
		t.Fatal(err)
	}
	_ = closed.Close()
	for _, goos := range []string{"linux", "windows"} {
		var out bytes.Buffer
		err := extractBinary(closed, goos, &out, 10)
		var e *Error
		if err == nil || errors.As(err, &e) || !strings.Contains(err.Error(), "cannot read back the downloaded archive") {
			t.Errorf("%s, unreadable archive: err = %#v, want a plain read-back error", goos, err)
		}
	}

	full := errors.New("no space left on device")
	archives := map[string]string{
		"linux":   writeTarGz(t, tarEntry{name: "rune", body: "binary"}),
		"windows": writeZip(t, "rune.exe"),
	}
	for goos, p := range archives {
		err := extractPath(t, p, goos, failWriter{full}, 1<<20)
		var e *Error
		if !errors.As(err, &e) || e.Reason != ReasonNotWritable || !errors.Is(err, full) {
			t.Errorf("%s, full disk: err = %#v, want not-writable carrying the cause", goos, err)
		}
	}
}

type failWriter struct{ err error }

func (f failWriter) Write([]byte) (int, error) { return 0, f.err }

// errorsAs is errors.As for *Error, for tests that already hold the pointer.
func errorsAs(err error, target **Error) bool { return errors.As(err, target) }
