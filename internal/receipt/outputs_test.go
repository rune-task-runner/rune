package receipt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestOutputFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "dist", "b"), "bb")
	writeFile(t, filepath.Join(root, "dist", "a"), "a")
	if err := os.Symlink("a", filepath.Join(root, "dist", "link")); err != nil {
		t.Skip("no symbolic links here")
	}
	files, err := OutputFiles(root, []string{"dist/b", "dist", "dist/a", "dist/link", "missing"})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("a"))
	if len(files) != 2 || files[0].Path != "dist/a" || files[0].Size != 1 || files[0].SHA256 != hex.EncodeToString(sum[:]) || files[1].Path != "dist/b" {
		t.Fatalf("files = %+v", files)
	}
}

func manyFiles(n int) []OutputFile {
	files := make([]OutputFile, n)
	for i := range files {
		files[i] = OutputFile{Path: fmt.Sprintf("f%03d", i), Size: 1, SHA256: "x"}
	}
	return files
}

func TestBuildOutputs(t *testing.T) {
	if out, manifest, err := BuildOutputs(nil); out != nil || manifest != nil || err != nil {
		t.Fatalf("no files: %v %v %v", out, manifest, err)
	}
	files := manyFiles(MaxInlineOutputs)
	out, manifest, err := BuildOutputs(files)
	if err != nil || manifest != nil {
		t.Fatalf("inline: %v %v", manifest, err)
	}
	canonical, _ := json.Marshal(files)
	sum := sha256.Sum256(canonical)
	if out.Count != MaxInlineOutputs || len(out.Files) != MaxInlineOutputs || out.Digest != hex.EncodeToString(sum[:]) || out.Manifest != "" {
		t.Fatalf("inline outputs %+v", out)
	}

	files = manyFiles(MaxInlineOutputs + 1)
	out, manifest, err = BuildOutputs(files)
	if err != nil || manifest == nil {
		t.Fatalf("manifest: %v", err)
	}
	sum = sha256.Sum256(manifest)
	if out.Count != MaxInlineOutputs+1 || out.Files != nil || out.Digest != hex.EncodeToString(sum[:]) {
		t.Fatalf("outputs with a manifest %+v", out)
	}
}

func TestCompareOutputs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "same"), "s")
	writeFile(t, filepath.Join(root, "changed"), "new")
	files, err := OutputFiles(root, []string{"same", "changed"})
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, OutputFile{Path: "gone", Size: 1, SHA256: "x"})
	for i := range files {
		if files[i].Path == "changed" {
			files[i].SHA256 = "old"
		}
	}
	out, _, _ := BuildOutputs(files)
	got := CompareOutputs(root, out, nil)
	want := map[string]string{"same": OutputMatch, "changed": OutputChanged, "gone": OutputMissing}
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	for _, s := range got {
		if want[s.Path] != s.State {
			t.Errorf("%s = %s, want %s", s.Path, s.State, want[s.Path])
		}
	}

	big := manyFiles(MaxInlineOutputs + 1)
	out, manifest, _ := BuildOutputs(big)
	out.Manifest = "x.outputs.json"
	if got := CompareOutputs(root, out, nil); len(got) != 1 || got[0].State != OutputUnknown || got[0].Path != "x.outputs.json" {
		t.Fatalf("missing manifest: %+v", got)
	}
	if got := CompareOutputs(root, out, []byte("[]")); len(got) != 1 || got[0].State != OutputUnknown {
		t.Fatalf("manifest with another digest: %+v", got)
	}
	if got := CompareOutputs(root, out, manifest); len(got) != MaxInlineOutputs+1 || got[0].State != OutputMissing {
		t.Fatalf("manifest: %d entries, first %+v", len(got), got[0])
	}
	if got := CompareOutputs(root, nil, nil); got != nil {
		t.Fatalf("no outputs: %+v", got)
	}
}
