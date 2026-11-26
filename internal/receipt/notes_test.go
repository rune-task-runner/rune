package receipt

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestReadNote(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	repo, err := OpenRepo(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	commit := gitRun(t, dir, "rev-parse", "HEAD")
	lines, err := repo.ReadNote(ctx, commit)
	if err != nil || lines != nil {
		t.Fatalf("no notes ref: %q %v", lines, err)
	}
	gitRun(t, dir, "notes", "--ref=other", "add", "-m", "x", commit)
	writeFile(t, dir+"/b.txt", "b")
	gitRun(t, dir, "add", "b.txt")
	gitRun(t, dir, "commit", "-qm", "second")
	second := gitRun(t, dir, "rev-parse", "HEAD")
	gitRun(t, dir, "notes", "--ref="+NotesRef, "add", "-m", "{\"a\":1}\n\n{\"b\":2}", second)
	if lines, err := repo.ReadNote(ctx, commit); err != nil || lines != nil {
		t.Fatalf("no note for this commit: %q %v", lines, err)
	}
	lines, err = repo.ReadNote(ctx, second)
	if err != nil || len(lines) != 2 || string(lines[0]) != `{"a":1}` || string(lines[1]) != `{"b":2}` {
		t.Fatalf("lines %q err %v", lines, err)
	}
}

func gitIdentity(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.com")
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.com")
}

func TestAttachNote(t *testing.T) {
	ctx := context.Background()
	gitIdentity(t)
	dir := newRepo(t)
	repo, _ := OpenRepo(ctx, dir)
	commit := gitRun(t, dir, "rev-parse", "HEAD")
	gitRun(t, dir, "notes", "add", "-m", "unrelated", commit)
	unrelated := gitRun(t, dir, "notes", "show", commit)
	notesCommit := gitRun(t, dir, "rev-parse", "refs/notes/commits")

	added, present, err := repo.AttachNote(ctx, commit, [][]byte{[]byte(`{"b":1}`), []byte(`{"a":1}` + "\n")})
	if err != nil || added != 2 || present != 0 {
		t.Fatalf("first attach: %d %d %v", added, present, err)
	}
	added, present, err = repo.AttachNote(ctx, commit, [][]byte{[]byte(`{"a":1}`), []byte(`{"c":1}`)})
	if err != nil || added != 1 || present != 1 {
		t.Fatalf("second attach: %d %d %v", added, present, err)
	}
	if got := gitRun(t, dir, "notes", "--ref="+NotesRef, "show", commit); got != "{\"a\":1}\n{\"b\":1}\n{\"c\":1}" {
		t.Fatalf("note %q", got)
	}
	added, present, err = repo.AttachNote(ctx, commit, [][]byte{[]byte(`{"c":1}`)})
	if err != nil || added != 0 || present != 1 {
		t.Fatalf("nothing new: %d %d %v", added, present, err)
	}
	if gitRun(t, dir, "rev-parse", "HEAD") != commit {
		t.Fatal("the commit changed")
	}
	if gitRun(t, dir, "notes", "show", commit) != unrelated || gitRun(t, dir, "rev-parse", "refs/notes/commits") != notesCommit {
		t.Fatal("the unrelated note changed")
	}
}

func TestAttachNoteMergesConcurrentWriter(t *testing.T) {
	ctx := context.Background()
	gitIdentity(t)
	dir := newRepo(t)
	repo, _ := OpenRepo(ctx, dir)
	commit := gitRun(t, dir, "rev-parse", "HEAD")
	// Another writer's note replaces ours right after our first write.
	replaced := false
	repo.afterWrite = func() {
		if !replaced {
			replaced = true
			gitRun(t, dir, "notes", "--ref="+NotesRef, "add", "-f", "-m", `{"other":1}`, commit)
		}
	}
	if _, _, err := repo.AttachNote(ctx, commit, [][]byte{[]byte(`{"mine":1}`)}); err != nil {
		t.Fatal(err)
	}
	if got := gitRun(t, dir, "notes", "--ref="+NotesRef, "show", commit); got != "{\"mine\":1}\n{\"other\":1}" {
		t.Fatalf("note %q", got)
	}
	repo.afterWrite = func() {
		gitRun(t, dir, "notes", "--ref="+NotesRef, "add", "-f", "-m", `{"x":1}`, commit)
	}
	if _, _, err := repo.AttachNote(ctx, commit, [][]byte{[]byte(`{"y":1}`)}); err == nil {
		t.Fatal("a note that keeps changing must fail after three tries")
	}
	if got := gitRun(t, dir, "notes", "--ref="+NotesRef, "show", commit); got != `{"x":1}` {
		t.Fatalf("the other writer's note was replaced: %q", got)
	}
	if refs := gitRun(t, dir, "for-each-ref", "--format=%(refname)", "refs/notes/"); refs != NotesRef {
		t.Fatalf("scratch refs left: %q", refs)
	}
}

func TestAttachNoteNeedsIdentity(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	repo, _ := OpenRepo(ctx, dir)
	commit := gitRun(t, dir, "rev-parse", "HEAD")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "user.useConfigOnly")
	t.Setenv("GIT_CONFIG_VALUE_0", "true")
	for _, name := range []string{"GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "EMAIL"} {
		t.Setenv(name, "")
		_ = os.Unsetenv(name)
	}
	// The repository's own identity would be enough; this one has none.
	gitRun(t, dir, "config", "--unset", "user.email")
	_, _, err := repo.AttachNote(ctx, commit, [][]byte{[]byte(`{"a":1}`)})
	if err == nil || !strings.Contains(err.Error(), "set user.name and user.email") {
		t.Fatalf("err = %v", err)
	}
}
