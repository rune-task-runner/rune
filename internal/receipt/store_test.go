package receipt

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDir(t *testing.T) {
	if got := Dir("/p"); got != filepath.Join("/p", ".rune", "receipts") {
		t.Fatalf("Dir = %q", got)
	}
}

func TestWrite(t *testing.T) {
	dir := Dir(t.TempDir())
	line := []byte("{\"x\":1}\n")
	if err := Write(dir, "id1", line); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "id1.json"))
	if err != nil || string(got) != string(line) {
		t.Fatalf("content %q err %v", got, err)
	}
	ignore, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil || string(ignore) != "*\n" {
		t.Fatalf(".gitignore %q err %v", ignore, err)
	}
	if err := Write(dir, "id1", []byte("other\n")); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("second write: err = %v, want ErrExist", err)
	}
	got, _ = os.ReadFile(filepath.Join(dir, "id1.json"))
	if string(got) != string(line) {
		t.Fatal("an existing receipt was replaced")
	}
	assertNoTemp(t, dir)
}

func TestWriteKeepsUserGitignore(t *testing.T) {
	dir := Dir(t.TempDir())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, "id", []byte("x\n")); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(got) != "mine\n" {
		t.Fatalf(".gitignore replaced: %q", got)
	}
}

func TestWriteUnwritable(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".rune"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(Dir(root), "id", []byte("x\n")); err == nil {
		t.Fatal("writing under a file must fail")
	}
}

func assertNoTemp(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatalf("temporary file left: %s", e.Name())
		}
	}
}

func TestWriteManifestAndRead(t *testing.T) {
	dir := Dir(t.TempDir())
	name, err := WriteManifest(dir, "id2", []byte("[]"))
	if err != nil || name != "id2.outputs.json" {
		t.Fatalf("name %q err %v", name, err)
	}
	data, err := ReadManifest(dir, name)
	if err != nil || string(data) != "[]" {
		t.Fatalf("read %q err %v", data, err)
	}
	for _, bad := range []string{"../x.outputs.json", "x.json", "a/b.outputs.json", ""} {
		if _, err := ReadManifest(dir, bad); err == nil {
			t.Errorf("ReadManifest(%q) should be refused", bad)
		}
	}
	assertNoTemp(t, dir)
}

func TestList(t *testing.T) {
	dir := Dir(t.TempDir())
	stored, err := List(dir)
	if err != nil || len(stored) != 0 {
		t.Fatalf("missing dir: %v %v", stored, err)
	}
	for _, id := range []string{"b", "a"} {
		if err := Write(dir, id, []byte(id+"\n")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := WriteManifest(dir, "a", []byte("[]")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	stored, err = List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 || stored[0].Name != "a.json" || string(stored[0].Data) != "a\n" || stored[1].Name != "b.json" {
		t.Fatalf("List = %+v", stored)
	}
}
