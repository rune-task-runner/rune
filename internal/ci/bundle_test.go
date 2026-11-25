package ci

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPackUnpackRoundTrip(t *testing.T) {
	src := t.TempDir()
	writeTree(t, src, map[string]string{"dist/app": "app", "dist/sub/b.txt": "b", ".rune/cache/build.json": "{}"})
	if runtime.GOOS != "windows" {
		if err := os.Chmod(filepath.Join(src, "dist", "app"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(src, "dist", "app"), old, old); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Pack(src, []string{"dist", "dist/app", "dist/sub", "dist/sub/b.txt", ".rune/cache/build.json"}, &buf); err != nil {
		t.Fatal(err)
	}

	dst := t.TempDir()
	writeTree(t, dst, map[string]string{"dist/app": "stale"})
	if err := Unpack(dst, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"dist/app": "app", "dist/sub/b.txt": "b", ".rune/cache/build.json": "{}"} {
		if got := readFile(t, filepath.Join(dst, filepath.FromSlash(name))); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	info, err := os.Stat(filepath.Join(dst, "dist", "app"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(old) {
		t.Errorf("mtime = %v, want %v", info.ModTime(), old)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want the exec bit kept", info.Mode().Perm())
	}
}

func TestPackRefuses(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"dist/app": "x"})
	for _, paths := range [][]string{{"../x"}, {"missing"}} {
		if err := Pack(root, paths, &bytes.Buffer{}); err == nil {
			t.Errorf("Pack(%q) accepted it", paths)
		}
	}
	if runtime.GOOS == "windows" {
		t.Skip("links need privileges on Windows")
	}
	if err := os.Symlink("app", filepath.Join(root, "dist", "link")); err != nil {
		t.Fatal(err)
	}
	err := Pack(root, []string{"dist/link"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "dist/link is a link") {
		t.Errorf("Pack of a link: %v", err)
	}
}

// bundleOf builds a bundle with raw headers, for entries Pack never writes.
func bundleOf(t *testing.T, headers ...*tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, h := range headers {
		if h.Typeflag == tar.TypeReg {
			h.Size = 1
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte("x")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestUnpackRefusesUnsafeEntries(t *testing.T) {
	for name, h := range map[string]*tar.Header{
		"absolute":  {Name: "/etc/x", Typeflag: tar.TypeReg, Mode: 0o644},
		"dotdot":    {Name: "../x", Typeflag: tar.TypeReg, Mode: 0o644},
		"unclean":   {Name: "a/../../x", Typeflag: tar.TypeReg, Mode: 0o644},
		"backslash": {Name: `a\b`, Typeflag: tar.TypeReg, Mode: 0o644},
		"symlink":   {Name: "l", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		"hardlink":  {Name: "h", Typeflag: tar.TypeLink, Linkname: "x"},
		"device":    {Name: "d", Typeflag: tar.TypeChar},
	} {
		root := t.TempDir()
		err := Unpack(root, bytes.NewReader(bundleOf(t, h)))
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
		if entries, _ := os.ReadDir(root); len(entries) != 0 {
			t.Errorf("%s: wrote %v", name, entries)
		}
	}
	if err := Unpack(t.TempDir(), strings.NewReader("not gzip")); err == nil || !strings.Contains(err.Error(), "not a bundle") {
		t.Errorf("garbage: %v", err)
	}
	var broken bytes.Buffer
	zw := gzip.NewWriter(&broken)
	if _, err := zw.Write([]byte("not a tar archive at all, but long enough to be read as a header block")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Unpack(t.TempDir(), &broken); err == nil {
		t.Error("a gzip stream without a tar archive was accepted")
	}
}

func TestUnpackDoesNotFollowLinksOutOfTheProject(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("links need privileges on Windows")
	}
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "dist")); err != nil {
		t.Fatal(err)
	}
	err := Unpack(root, bytes.NewReader(bundleOf(t, &tar.Header{Name: "dist/app", Typeflag: tar.TypeReg, Mode: 0o644})))
	if err == nil {
		t.Error("an entry was written through a link that leaves the project")
	}
	if _, statErr := os.Stat(filepath.Join(outside, "app")); statErr == nil {
		t.Error("a file was written outside the project")
	}
}

// The key step and the run step of a job unpack the same bundles, so a
// read-only output must unpack twice.
func TestUnpackTwiceOverAReadOnlyFile(t *testing.T) {
	src := t.TempDir()
	writeTree(t, src, map[string]string{"out": "data"})
	if err := os.Chmod(filepath.Join(src, "out"), 0o444); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Pack(src, []string{"out"}, &buf); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	for i := range 2 {
		if err := Unpack(dst, bytes.NewReader(buf.Bytes())); err != nil {
			t.Fatalf("unpack %d: %v", i+1, err)
		}
	}
	if got := readFile(t, filepath.Join(dst, "out")); got != "data" {
		t.Errorf("out = %q", got)
	}
}

func TestUnpackDirWithGlobCharactersInItsPath(t *testing.T) {
	src, root := t.TempDir(), t.TempDir()
	dir := filepath.Join(t.TempDir(), "state[1]")
	writeTree(t, src, map[string]string{"out": "from a"})
	var buf bytes.Buffer
	if err := Pack(src, []string{"out"}, &buf); err != nil {
		t.Fatal(err)
	}
	writeTree(t, dir, map[string]string{"in/0001-a/bundle.tar": buf.String()})
	if err := UnpackDir(root, dir, false); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(root, "out")); got != "from a" {
		t.Errorf("out = %q, want the bundle's", got)
	}
}

func TestUnpackDirOrder(t *testing.T) {
	root, dir := t.TempDir(), t.TempDir()
	write := func(path, content string) {
		src := t.TempDir()
		writeTree(t, src, map[string]string{"out": content})
		var buf bytes.Buffer
		if err := Pack(src, []string{"out"}, &buf); err != nil {
			t.Fatal(err)
		}
		writeTree(t, dir, map[string]string{path: buf.String()})
	}
	write("in/0002-b/bundle.tar", "from b")
	write("in/0001-a/bundle.tar", "from a")
	write(BundleFile, "restored")
	writeTree(t, dir, map[string]string{"in/stray-file": "ignored"})

	if err := UnpackDir(root, dir, false); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(root, "out")); got != "from b" {
		t.Errorf("without the restored bundle: out = %q, want the last job's", got)
	}
	if err := UnpackDir(root, dir, true); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(root, "out")); got != "restored" {
		t.Errorf("with the restored bundle: out = %q, want it last", got)
	}
	if err := UnpackDir(root, t.TempDir(), true); err != nil {
		t.Errorf("an empty state directory: %v", err)
	}
	writeTree(t, dir, map[string]string{"in/0003-c/bundle.tar": "garbage"})
	if err := UnpackDir(root, dir, false); err == nil || !strings.Contains(err.Error(), "0003-c") {
		t.Errorf("a broken bundle: %v", err)
	}
}
