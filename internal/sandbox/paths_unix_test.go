//go:build unix

package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// realTemp is a temporary directory with its own links followed, so that a
// test compares resolved paths on a host whose temporary directory is a link.
func realTemp(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestResolve(t *testing.T) {
	dir := realTemp(t)
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if got := Resolve(link); got != real {
		t.Errorf("Resolve(link) = %q, want %q", got, real)
	}
	if got, want := Resolve(filepath.Join(link, "a", "b")), filepath.Join(real, "a", "b"); got != want {
		t.Errorf("Resolve of a missing tail = %q, want %q", got, want)
	}
}

func TestWithin(t *testing.T) {
	cases := []struct {
		parent, path string
		want         bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b", "/a/b/c", true},
		{"/a/b", "/a/bc", false},
		{"/a/b", "/a", false},
		{"/a/b", "/a/b/../c", false},
		{"/", "/x", true},
	}
	for _, c := range cases {
		if got := Within(c.parent, c.path); got != c.want {
			t.Errorf("Within(%q, %q) = %v, want %v", c.parent, c.path, got, c.want)
		}
	}
}

func TestAncestors(t *testing.T) {
	if got, want := Ancestors("/ws", "/ws/a/b/f"), []string{"/ws/a", "/ws/a/b"}; !slices.Equal(got, want) {
		t.Errorf("Ancestors = %v, want %v", got, want)
	}
	if got := Ancestors("/ws", "/ws/f"); len(got) != 0 {
		t.Errorf("a direct child has ancestors %v", got)
	}
	if got := Ancestors("/ws", "/other/a/f"); len(got) != 0 {
		t.Errorf("a path outside has ancestors %v", got)
	}
}

func TestCheckProtected(t *testing.T) {
	ws := realTemp(t)
	write := func(rel string) string {
		p := filepath.Join(ws, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	plain := write("Runefile")
	if err := CheckProtected(ws, plain, true); err != nil {
		t.Errorf("a plain file: %v", err)
	}
	if err := CheckProtected(ws, filepath.Join(ws, "missing", "f"), true); err != nil {
		t.Errorf("a missing path: %v", err)
	}

	linked := write("linked")
	if err := os.Link(linked, filepath.Join(ws, "other-name")); err != nil {
		t.Fatal(err)
	}
	var le *LinkedError
	if err := CheckProtected(ws, linked, true); !errors.As(err, &le) || le.Path != linked || le.Count != 2 {
		t.Errorf("a linked file: %v", err)
	}
	if err := CheckProtected(ws, linked, false); err != nil {
		t.Errorf("a linked file with links off: %v", err)
	}

	write("real/t.rune")
	if err := os.Symlink(filepath.Join(ws, "real"), filepath.Join(ws, "sub")); err != nil {
		t.Fatal(err)
	}
	through := filepath.Join(ws, "sub", "t.rune")
	var se *SymlinkError
	if err := CheckProtected(ws, through, true); !errors.As(err, &se) || se.Path != through || se.Link != filepath.Join(ws, "sub") {
		t.Errorf("a path through a link: %v", err)
	}
	self := filepath.Join(ws, "self.rune")
	if err := os.Symlink(plain, self); err != nil {
		t.Fatal(err)
	}
	if err := CheckProtected(ws, self, true); !errors.As(err, &se) || se.Link != self {
		t.Errorf("a path that is a link: %v", err)
	}

	// A directory always has more than one link.
	if err := os.MkdirAll(filepath.Join(ws, ".git", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CheckProtected(ws, filepath.Join(ws, ".git"), true); err != nil {
		t.Errorf("a directory: %v", err)
	}
}

func TestRepoRoot(t *testing.T) {
	dir := realTemp(t)
	deep := filepath.Join(dir, "repo", "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := RepoRoot(deep); got != deep {
		t.Errorf("no repository: %q, want the start %q", got, deep)
	}
	if err := os.Mkdir(filepath.Join(dir, "repo", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := RepoRoot(deep), filepath.Join(dir, "repo"); got != want {
		t.Errorf("RepoRoot = %q, want %q", got, want)
	}
	// A worktree has a .git file, and the nearest entry wins.
	if err := os.WriteFile(filepath.Join(deep, ".git"), []byte("gitdir: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := RepoRoot(deep); got != deep {
		t.Errorf("RepoRoot with a .git file = %q, want %q", got, deep)
	}
}
