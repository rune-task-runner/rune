package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDiscover(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(deep); !errors.Is(err, ErrNotFound) && err != nil {
		// A Runefile above the temporary directory is not this test's.
		t.Logf("a Runefile exists above the temporary directory: %v", err)
	}
	write := func(path string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("build:\n    @true\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	top := filepath.Join(root, "Runefile")
	write(top)
	if got, err := Discover(deep); err != nil || got != top {
		t.Errorf("Discover from below = %q, %v, want %q", got, err, top)
	}
	// A directory is not a Runefile, whatever its name.
	if err := os.Mkdir(filepath.Join(root, "a", ".runefile"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := Discover(deep); err != nil || got != top {
		t.Errorf("a directory named .runefile was taken: %q, %v", got, err)
	}
	nearer := filepath.Join(root, "a", "runefile")
	write(nearer)
	if got, err := Discover(deep); err != nil || got != nearer {
		t.Errorf("the nearest Runefile = %q, %v, want %q", got, err, nearer)
	}
	if got, err := Resolve(top, deep); err != nil || got != top {
		t.Errorf("Resolve with a file = %q, %v", got, err)
	}
}

// A directory with two Runefiles is an error. Taking one of them would let a
// file added beside the Runefile replace it in silence (spec 053).
func TestDiscoverAmbiguous(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Runefile", ".runefile"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("build:\n    @true\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, err = Discover(dir)
	var ambiguous *AmbiguousError
	if !errors.As(err, &ambiguous) || ambiguous.Dir != dir || len(ambiguous.Names) != 2 {
		t.Fatalf("error = %v", err)
	}
	want := dir + " holds more than one Runefile (.runefile, Runefile); remove all but one, or name one with --file"
	if err.Error() != want {
		t.Errorf("text = %q, want %q", err.Error(), want)
	}
	// From a directory below, the search stops at the ambiguity: it does not
	// pass it by for a Runefile further up.
	below := filepath.Join(dir, "sub")
	if err := os.Mkdir(below, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(below); !errors.As(err, &ambiguous) {
		t.Errorf("from below: %v", err)
	}
	// Naming the file is the way out.
	if got, err := Resolve(filepath.Join(dir, "Runefile"), dir); err != nil || got != filepath.Join(dir, "Runefile") {
		t.Errorf("Resolve with --file = %q, %v", got, err)
	}
}
