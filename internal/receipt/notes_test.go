package receipt

import (
	"context"
	"os"
	"slices"
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

func TestNotesForTree(t *testing.T) {
	ctx := context.Background()
	gitIdentity(t)
	dir := newRepo(t)
	repo, err := OpenRepo(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	first := gitRun(t, dir, "rev-parse", "HEAD")
	tree := gitRun(t, dir, "rev-parse", "HEAD^{tree}")
	if lines, err := repo.NotesForTree(ctx, tree); err != nil || lines != nil {
		t.Fatalf("no notes ref: %q %v", lines, err)
	}

	// A second commit with the same tree, and a third with another.
	gitRun(t, dir, "commit", "-q", "--allow-empty", "-m", "same tree")
	second := gitRun(t, dir, "rev-parse", "HEAD")
	writeFile(t, dir+"/c.txt", "c")
	gitRun(t, dir, "add", "c.txt")
	gitRun(t, dir, "commit", "-qm", "other tree")
	third := gitRun(t, dir, "rev-parse", "HEAD")
	gitRun(t, dir, "notes", "--ref="+NotesRef, "add", "-m", "{\"a\":1}\n\n", first)
	gitRun(t, dir, "notes", "--ref="+NotesRef, "add", "-m", `{"b":1}`, second)
	gitRun(t, dir, "notes", "--ref="+NotesRef, "add", "-m", `{"c":1}`, third)
	gitRun(t, dir, "notes", "add", "-m", `{"other":1}`, first)
	before := snapshotState(t, dir)
	notesBefore := gitRun(t, dir, "rev-parse", NotesRef)

	lines, err := repo.NotesForTree(ctx, tree)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(lines))
	for i, l := range lines {
		got[i] = string(l)
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{`{"a":1}`, `{"b":1}`}) {
		t.Fatalf("lines %q", got)
	}
	if lines, err := repo.NotesForTree(ctx, "0123456789012345678901234567890123456789"); err != nil || lines != nil {
		t.Fatalf("a tree no commit has: %q %v", lines, err)
	}
	if snapshotState(t, dir) != before || gitRun(t, dir, "rev-parse", NotesRef) != notesBefore {
		t.Fatal("reading notes changed the repository")
	}
}

// A clone that fetched the notes ref lacks the commits it annotates, as a CI
// checkout of main lacks a pull request's merge preview: the receipts that
// name the tree still count, and only those.
func TestNotesForTreeCommitNotFetched(t *testing.T) {
	ctx := context.Background()
	key := testKey(t, 1)
	match := signed(t, key, passing("m", t0))
	other := passing("o", t0)
	other.Source.Before, other.Source.After = vOther, vOther
	src := newRepo(t)
	gitRun(t, src, "checkout", "-qb", "pr")
	writeFile(t, src+"/c.txt", "c")
	gitRun(t, src, "add", "c.txt")
	gitRun(t, src, "commit", "-qm", "pr")
	note := string(match) + "\n" + string(signed(t, key, other)) + "\nnot a receipt\n"
	gitRun(t, src, "notes", "--ref="+NotesRef, "add", "-m", note, "HEAD")

	dst := newRepo(t)
	gitRun(t, dst, "fetch", "-q", src, NotesRef+":"+NotesRef)
	repo, err := OpenRepo(ctx, dst)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := repo.NotesForTree(ctx, vTree)
	if err != nil || len(lines) != 1 || string(lines[0]) != strings.TrimSpace(string(match)) {
		t.Fatalf("lines %q err %v", lines, err)
	}
	if lines, err := repo.NotesForTree(ctx, gitRun(t, dst, "rev-parse", "HEAD^{tree}")); err != nil || lines != nil {
		t.Fatalf("this clone's own tree: %q %v", lines, err)
	}
}

func TestSnapshotOf(t *testing.T) {
	key := testKey(t, 1)
	none := passing("n", t0)
	none.Source = nil
	for name, c := range map[string]struct {
		line []byte
		want string
	}{
		"a receipt":        {signed(t, key, passing("a", t0)), vTree},
		"without snapshot": {signed(t, key, none), ""},
		"not an envelope":  {[]byte("x"), ""},
		"not a receipt":    {[]byte(`{"payloadType":"t","payload":"eA==","signatures":[{"keyid":"k","sig":"s"}]}`), ""},
	} {
		if got := snapshotOf(c.line); got != c.want {
			t.Errorf("%s: %q", name, got)
		}
	}
}

// git that does not run to an exit is a failure, not an absence of notes.
func TestNotesForTreeGitFails(t *testing.T) {
	dir := newRepo(t)
	repo, err := OpenRepo(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if lines, err := repo.NotesForTree(ctx, "x"); err == nil || lines != nil {
		t.Fatalf("lines %q err %v", lines, err)
	}
}

// A notes ref git cannot read is a failure, never "no notes".
func TestNotesForTreeBrokenRef(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	repo, err := OpenRepo(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir+"/.git/"+NotesRef, "0123456789012345678901234567890123456789\n")
	if lines, err := repo.NotesForTree(ctx, "x"); err == nil || lines != nil {
		t.Fatalf("lines %q err %v", lines, err)
	}
}
