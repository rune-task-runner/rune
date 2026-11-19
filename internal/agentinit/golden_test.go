package agentinit

import (
	"bytes"
	"flag"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "regenerate golden files")

// goldenCase is one directory under testdata/<client>/.
type goldenCase struct {
	dir    string
	client Client
	in     Inputs
	force  bool
	crlf   bool
	hasIn  bool
}

// testQuote writes a path as the command line quotes it for the report.
func testQuote(s string) string {
	if strings.ContainsAny(s, " \"\\'$") {
		return "'" + s + "'"
	}
	return s
}

// readTree maps every file under dir to its bytes, by its path with forward
// slashes. A directory that does not exist is an empty tree.
func readTree(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		files[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return files
}

// loadCase reads a case directory as the conventions of the task list say:
// a flags file with one option per line, and the files that exist before
// the run under in/.
func loadCase(t *testing.T, dir string) goldenCase {
	t.Helper()
	c := goldenCase{dir: dir, client: Client(filepath.Base(filepath.Dir(dir)))}
	a, ok := adapterOf(c.client)
	if !ok {
		t.Fatalf("%s: unknown client directory", dir)
	}
	runefile := "/work/demo/Runefile"
	version := ""
	if len(a.checked) > 0 {
		version = a.checked[0]
	}
	flags, err := os.ReadFile(filepath.Join(dir, "flags"))
	if err != nil {
		t.Fatalf("every case has a flags file: %v", err)
	}
	for line := range strings.Lines(string(flags)) {
		switch key, value, _ := strings.Cut(strings.TrimSpace(line), "="); key {
		case "":
		case "force":
			c.force = true
		case "unpinned":
			c.in.Unpinned = true
		case "crlf":
			c.crlf = true
		case "runefile":
			runefile = value
		case "version":
			version = value
		default:
			t.Fatalf("%s: unknown flag %q", dir, key)
		}
	}
	_, err = os.Stat(filepath.Join(dir, "in"))
	c.hasIn = err == nil
	c.in.Files = readTree(t, filepath.Join(dir, "in"))
	if c.crlf {
		// The repository stores every text file with \n, so a fixture
		// cannot hold \r\n itself.
		for name, data := range c.in.Files {
			c.in.Files[name] = bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n"))
		}
	}
	c.in.Runefile = runefile
	c.in.ShownRunefile = testQuote(runefile)
	c.in.ShownDir = testQuote(path.Dir(runefile))
	c.in.Force = c.force
	c.in.Version = version
	return c
}

func compareGolden(t *testing.T, file string, got []byte) {
	t.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs.\n--- got ---\n%s\n--- want ---\n%s", file, got, want)
	}
}

// subsequence reports whether sub can be made from all by deleting bytes.
func subsequence(sub, all []byte) bool {
	i := 0
	for _, b := range all {
		if i < len(sub) && sub[i] == b {
			i++
		}
	}
	return i == len(sub)
}

// contains reports whether the decoded value all holds everything part
// holds: every key with a value it contains, every list as an ordered
// subsequence, every other value equal.
func contains(all, part any) bool {
	switch part := part.(type) {
	case map[string]any:
		m, ok := all.(map[string]any)
		if !ok {
			return false
		}
		for k, v := range part {
			if _, ok := m[k]; !ok || !contains(m[k], v) {
				return false
			}
		}
		return true
	case []any:
		list, ok := all.([]any)
		if !ok {
			return false
		}
		i := 0
		for _, v := range list {
			if i < len(part) && contains(v, part[i]) {
				i++
			}
		}
		return i == len(part)
	}
	return reflect.DeepEqual(all, part)
}

// decodeFile reads a client file as plain values, by its format.
func decodeFile(t *testing.T, name string, data []byte) any {
	t.Helper()
	switch path.Ext(name) {
	case ".json":
		root, err := parseJSON(name, data)
		if err != nil {
			t.Fatal(err)
		}
		return decodeJSON(root)
	case ".toml":
		m, err := decodeTOML(name, data)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	return string(data)
}

// TestGolden plans every case and compares the files and the report with
// the committed ones. It also holds every case to the rules that do not
// depend on its content: a second run changes nothing, nothing is written
// that would approve or expose more than was asked, and an existing file
// only gains text.
func TestGolden(t *testing.T) {
	// One directory per client. A corpus the fuzz target leaves under
	// testdata/fuzz is not a case.
	var dirs []string
	for _, client := range Clients() {
		found, err := filepath.Glob(filepath.Join("testdata", string(client), "*"))
		if err != nil || len(found) == 0 {
			t.Fatalf("no golden cases found for %s: %v", client, err)
		}
		dirs = append(dirs, found...)
	}
	for _, dir := range dirs {
		t.Run(filepath.ToSlash(strings.TrimPrefix(dir, "testdata"+string(filepath.Separator))), func(t *testing.T) {
			c := loadCase(t, dir)
			res, err := Plan(c.client, c.in)
			wantDir := filepath.Join(dir, "want")
			if *update {
				if err := os.RemoveAll(wantDir); err != nil {
					t.Fatal(err)
				}
			}
			switch {
			case err != nil:
				compareGolden(t, filepath.Join(dir, "want.report"), []byte("error: "+err.Error()+"\n"))
				return
			case len(res.Conflicts) > 0:
				var b strings.Builder
				for _, k := range res.Conflicts {
					fmt.Fprintf(&b, "conflict: %s:%d:%d: %s\n", k.File, k.Line, k.Col, k.Msg)
				}
				compareGolden(t, filepath.Join(dir, "want.report"), []byte(b.String()))
				for _, f := range res.Files {
					if !bytes.Equal(f.New, f.Old) {
						t.Errorf("%s changes although the plan has a conflict", f.Path)
					}
				}
				return
			}
			compareGolden(t, filepath.Join(dir, "want.report"), []byte(Report(res, false)))

			after := map[string][]byte{}
			for _, f := range res.Files {
				after[f.Path] = f.New
				got := f.New
				old, existed := c.in.Files[f.Path]
				if c.crlf && existed {
					if bytes.Contains(bytes.ReplaceAll(got, []byte("\r\n"), nil), []byte("\n")) {
						t.Errorf("%s has a line that ends in \\n alone in a \\r\\n file:\n%q", f.Path, got)
					}
					got = bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n"))
				}
				compareGolden(t, filepath.Join(wantDir, filepath.FromSlash(f.Path)), got)

				for _, word := range []string{"allow-destructive", "sandbox", "approve", "bypassPermissions", "defaultMode", "env", "token", "Authorization"} {
					if bytes.Contains(f.New, []byte(word)) && !bytes.Contains(old, []byte(word)) {
						t.Errorf("%s gained %q, which a setup never writes", f.Path, word)
					}
				}
				if !existed || c.force {
					continue
				}
				if !subsequence(old, f.New) {
					t.Errorf("%s lost bytes it had before the run", f.Path)
				}
				if !contains(decodeFile(t, f.Path, f.New), decodeFile(t, f.Path, old)) {
					t.Errorf("%s no longer holds everything it held before the run", f.Path)
				}
			}
			if !*update {
				if want := slices.Sorted(maps.Keys(readTree(t, wantDir))); !slices.Equal(want, slices.Sorted(maps.Keys(after))) {
					t.Errorf("want/ holds %v, the plan has %v", want, slices.Sorted(maps.Keys(after)))
				}
			}

			again := c.in
			again.Files = after
			second, err := Plan(c.client, again)
			if err != nil || len(second.Conflicts) > 0 {
				t.Fatalf("a second run fails: %v %v", err, second)
			}
			for _, f := range second.Files {
				if f.Status() != "unchanged" {
					t.Errorf("a second run leaves %s %s, want unchanged:\n%s", f.Path, f.Status(), f.New)
				}
				for _, e := range f.Entries {
					if e.State != StatePresent {
						t.Errorf("after a run, %s: %s is %s", f.Path, e.Name, e.State)
					}
				}
			}
		})
	}
}
