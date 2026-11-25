package receipt

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitRun runs git in dir for a test and returns its trimmed output.
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newRepo makes a repository with one commit: a.txt and a .gitignore that
// ignores *.log.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	gitRun(t, dir, "config", "user.name", "t")
	gitRun(t, dir, "config", "user.email", "t@example.com")
	gitRun(t, dir, "config", "commit.gpgsign", "false")
	writeFile(t, filepath.Join(dir, "a.txt"), "a\n")
	writeFile(t, filepath.Join(dir, ".gitignore"), "*.log\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-qm", "init")
	return dir
}

func realDir(t *testing.T, dir string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func TestOpen(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, filepath.Join(dir, "sub", "x"), "x")
	repo, err := OpenRepo(context.Background(), filepath.Join(dir, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	if repo.Top != realDir(t, dir) || repo.GitDir != filepath.Join(realDir(t, dir), ".git") {
		t.Fatalf("repo = %+v", repo)
	}
	wt := filepath.Join(t.TempDir(), "wt")
	gitRun(t, dir, "worktree", "add", "-q", wt)
	linked, err := OpenRepo(context.Background(), wt)
	if err != nil {
		t.Fatal(err)
	}
	if linked.Top != realDir(t, wt) || !strings.Contains(linked.GitDir, "worktrees") {
		t.Fatalf("linked worktree = %+v", linked)
	}
	if _, err := OpenRepo(context.Background(), t.TempDir()); !errors.Is(err, ErrNotRepository) {
		t.Fatalf("outside a repository: err = %v", err)
	}
}

// expectedTree stages a copy of dir with git add -A and returns its tree.
func expectedTree(t *testing.T, dir string, exclude string) string {
	t.Helper()
	cp := t.TempDir()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		target := filepath.Join(cp, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	gitRun(t, cp, "add", "-A", "--", ":/", ":(top,exclude)"+exclude)
	return gitRun(t, cp, "write-tree")
}

func snapshotState(t *testing.T, dir string) string {
	t.Helper()
	index, _ := os.ReadFile(filepath.Join(dir, ".git", "index"))
	head, _ := os.ReadFile(filepath.Join(dir, ".git", "HEAD"))
	return string(index) + "\x00" + string(head) + "\x00" + gitRun(t, dir, "for-each-ref") + "\x00" + gitRun(t, dir, "status", "--porcelain")
}

func TestSnapshot(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	repo, err := OpenRepo(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	clean, err := repo.Snapshot(ctx, ".rune")
	if err != nil {
		t.Fatal(err)
	}
	if clean != gitRun(t, dir, "rev-parse", "HEAD^{tree}") {
		t.Fatal("a clean tree's snapshot must be HEAD's tree")
	}

	writeFile(t, filepath.Join(dir, "ignored.log"), "x")
	writeFile(t, filepath.Join(dir, ".rune", "cache", "r.json"), "x")
	same, err := repo.Snapshot(ctx, ".rune")
	if err != nil || same != clean {
		t.Fatalf("ignored files and .rune must not change the snapshot: %v", err)
	}

	writeFile(t, filepath.Join(dir, "a.txt"), "changed\n")
	writeFile(t, filepath.Join(dir, "new.txt"), "untracked\n")
	before := snapshotState(t, dir)
	got, err := repo.Snapshot(ctx, ".rune")
	if err != nil {
		t.Fatal(err)
	}
	if after := snapshotState(t, dir); after != before {
		t.Fatal("the snapshot changed the index, HEAD, refs or working tree")
	}
	if want := expectedTree(t, dir, ".rune"); got != want {
		t.Fatalf("snapshot %s, git add -A gives %s", got, want)
	}
	if got == clean {
		t.Fatal("changes must change the snapshot")
	}
}

// An index entry marked assume-unchanged or skip-worktree must not hide an
// edit from the snapshot.
func TestSnapshotSeesHiddenEdits(t *testing.T) {
	ctx := context.Background()
	for _, flag := range []string{"--assume-unchanged", "--skip-worktree"} {
		t.Run(flag, func(t *testing.T) {
			dir := newRepo(t)
			repo, err := OpenRepo(ctx, dir)
			if err != nil {
				t.Fatal(err)
			}
			gitRun(t, dir, "update-index", flag, "a.txt")
			writeFile(t, filepath.Join(dir, "a.txt"), "changed\n")
			got, err := repo.Snapshot(ctx, ".rune")
			if err != nil {
				t.Fatal(err)
			}
			if got == gitRun(t, dir, "rev-parse", "HEAD^{tree}") {
				t.Fatal("the edit of a hidden entry is missing from the snapshot")
			}
			gitRun(t, dir, "update-index", "--no"+flag[1:], "a.txt")
			if want, err := repo.Snapshot(ctx, ".rune"); err != nil || got != want {
				t.Fatalf("snapshot %s, without the mark %s (%v)", got, want, err)
			}
		})
	}
}

// A tracked file that .gitignore matches is still in a snapshot taken past an
// entry that hides changes.
func TestSnapshotHiddenKeepsTrackedIgnored(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	writeFile(t, filepath.Join(dir, "kept.log"), "tracked\n")
	gitRun(t, dir, "add", "-f", "kept.log")
	gitRun(t, dir, "commit", "-qm", "kept")
	gitRun(t, dir, "update-index", "--assume-unchanged", "a.txt")
	repo, err := OpenRepo(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := repo.Snapshot(ctx, ".rune")
	if err != nil {
		t.Fatal(err)
	}
	if want := gitRun(t, dir, "rev-parse", "HEAD^{tree}"); got != want {
		t.Fatalf("snapshot %s of an unchanged tree, want HEAD's %s", got, want)
	}
}

// An entry as new as the index file is racily clean: git compares its
// content, so a same-size edit in that moment is in the snapshot.
func TestSnapshotRacyEntry(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	gitRun(t, dir, "config", "core.trustctime", "false")
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	a := filepath.Join(dir, "a.txt")
	if err := os.Chtimes(a, old, old); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "update-index", "--refresh")
	writeFile(t, a, "b\n")
	if err := os.Chtimes(a, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, ".git", "index"), old, old); err != nil {
		t.Fatal(err)
	}
	repo, err := OpenRepo(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := repo.Snapshot(ctx, ".rune")
	if err != nil {
		t.Fatal(err)
	}
	if got == gitRun(t, dir, "rev-parse", "HEAD^{tree}") {
		t.Fatal("a same-size edit as new as the index is missing from the snapshot")
	}
}

func TestSnapshotNoCommit(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	writeFile(t, filepath.Join(dir, "f"), "f")
	repo, err := OpenRepo(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := repo.Snapshot(ctx, ".rune")
	if err != nil || tree == "" {
		t.Fatalf("tree %q err %v", tree, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "index")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("the snapshot created the repository's index")
	}
	commit, headTree, err := repo.Head(ctx)
	if err != nil || commit != "" || headTree != "" {
		t.Fatalf("Head with no commit: %q %q %v", commit, headTree, err)
	}
}

func TestHeadAndResolve(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	repo, _ := OpenRepo(ctx, dir)
	commit, tree, err := repo.Head(ctx)
	if err != nil || commit != gitRun(t, dir, "rev-parse", "HEAD") || tree != gitRun(t, dir, "rev-parse", "HEAD^{tree}") {
		t.Fatalf("Head = %q %q %v", commit, tree, err)
	}
	c2, t2, err := repo.Resolve(ctx, "HEAD")
	if err != nil || c2 != commit || t2 != tree {
		t.Fatalf("Resolve = %q %q %v", c2, t2, err)
	}
	for _, bad := range []string{"nope", "--help", ""} {
		if _, _, err := repo.Resolve(ctx, bad); !errors.Is(err, ErrUnknownRevision) {
			t.Errorf("Resolve(%q): err = %v", bad, err)
		}
	}
}

func TestWorktreeMatches(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct {
		change func(dir string)
		want   bool
	}{
		"clean":    {func(string) {}, true},
		"ignored":  {func(dir string) { writeFile(t, filepath.Join(dir, "x.log"), "x") }, true},
		"own dir":  {func(dir string) { writeFile(t, filepath.Join(dir, ".rune", "x"), "x") }, true},
		"modified": {func(dir string) { writeFile(t, filepath.Join(dir, "a.txt"), "b") }, false},
		"deleted":  {func(dir string) { _ = os.Remove(filepath.Join(dir, "a.txt")) }, false},
		"staged": {func(dir string) {
			writeFile(t, filepath.Join(dir, "s.txt"), "s")
			gitRun(t, dir, "add", "s.txt")
		}, false},
		"untracked": {func(dir string) { writeFile(t, filepath.Join(dir, "u.txt"), "u") }, false},
		"assume-unchanged": {func(dir string) {
			gitRun(t, dir, "update-index", "--assume-unchanged", "a.txt")
			writeFile(t, filepath.Join(dir, "a.txt"), "b")
		}, false},
		"skip-worktree": {func(dir string) {
			gitRun(t, dir, "update-index", "--skip-worktree", "a.txt")
			writeFile(t, filepath.Join(dir, "a.txt"), "b")
		}, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dir := newRepo(t)
			c.change(dir)
			repo, _ := OpenRepo(ctx, dir)
			got, err := repo.WorktreeMatches(ctx, gitRun(t, dir, "rev-parse", "HEAD"), ".rune")
			if err != nil || got != c.want {
				t.Fatalf("WorktreeMatches = %v %v, want %v", got, err, c.want)
			}
		})
	}
}

func TestCheckIgnore(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	writeFile(t, filepath.Join(dir, "tracked.log"), "t")
	gitRun(t, dir, "add", "-f", "tracked.log")
	writeFile(t, filepath.Join(dir, "x.log"), "x")
	repo, _ := OpenRepo(ctx, dir)
	got, err := repo.CheckIgnore(ctx, []string{"a.txt", "x.log", "tracked.log"})
	if err != nil || len(got) != 1 || got[0] != "x.log" {
		t.Fatalf("CheckIgnore = %v %v", got, err)
	}
	got, err = repo.CheckIgnore(ctx, []string{"a.txt"})
	if err != nil || len(got) != 0 {
		t.Fatalf("CheckIgnore none = %v %v", got, err)
	}
	got, err = repo.CheckIgnore(ctx, nil)
	if err != nil || got != nil {
		t.Fatalf("CheckIgnore empty = %v %v", got, err)
	}
}

func TestGitCancelled(t *testing.T) {
	dir := newRepo(t)
	repo, err := OpenRepo(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := repo.Snapshot(ctx, ".rune"); err == nil {
		t.Fatal("a cancelled snapshot must fail")
	}
	if _, _, err := repo.Head(ctx); err == nil {
		t.Fatal("a cancelled Head must fail")
	}
	if _, err := OpenRepo(ctx, dir); err == nil {
		t.Fatal("a cancelled Open must fail")
	}
}

func TestGitErrorCarriesStderr(t *testing.T) {
	_, _, err := runGit(context.Background(), t.TempDir(), nil, nil, "rev-parse", "--verify", "nope")
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("git rev-parse")) {
		t.Fatalf("err = %v", err)
	}
}
