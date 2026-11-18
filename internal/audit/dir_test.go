package audit

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDefaultBase(t *testing.T) {
	env := func(vars map[string]string) func(string) string {
		return func(name string) string { return vars[name] }
	}
	home := func() (string, error) { return filepath.FromSlash("/home/op"), nil }
	noHome := func() (string, error) { return "", errors.New("no home") }
	// An absolute path on this host: on Windows that takes a volume.
	state := filepath.Join(filepath.VolumeName(t.TempDir())+string(filepath.Separator), "state")
	inState := filepath.Join(state, "rune", "audit")

	tests := []struct {
		name string
		env  map[string]string
		goos string
		home func() (string, error)
		want string
	}{
		{"xdg", map[string]string{"XDG_STATE_HOME": state}, "linux", home, inState},
		{"xdg wins on windows", map[string]string{"XDG_STATE_HOME": state, "LocalAppData": "/local"}, "windows", home, inState},
		{"a relative xdg is ignored", map[string]string{"XDG_STATE_HOME": "state"}, "linux", home, "/home/op/.local/state/rune/audit"},
		{"a relative xdg is ignored on windows", map[string]string{"XDG_STATE_HOME": "state", "LocalAppData": "/local"}, "windows", home, "/local/rune/audit"},
		{"windows", map[string]string{"LocalAppData": "/local"}, "windows", home, "/local/rune/audit"},
		{"windows without the variable", nil, "windows", home, "/home/op/.local/state/rune/audit"},
		{"local app data is windows only", map[string]string{"LocalAppData": "/local"}, "linux", home, "/home/op/.local/state/rune/audit"},
		{"home", nil, "darwin", home, "/home/op/.local/state/rune/audit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DefaultBase(env(tt.env), tt.goos, tt.home)
			if err != nil {
				t.Fatal(err)
			}
			if got != filepath.FromSlash(tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
	if _, err := DefaultBase(env(nil), "linux", noHome); err == nil {
		t.Error("no error without a home directory")
	}
	if _, err := DefaultBase(env(nil), "linux", func() (string, error) { return "", nil }); err == nil {
		t.Error("no error for an empty home directory")
	}
}

func TestProjectID(t *testing.T) {
	a, b := ProjectID("/repo/Runefile"), ProjectID("/other/Runefile")
	if len(a) != 16 {
		t.Fatalf("id %q is not 16 digits", a)
	}
	if a == b {
		t.Error("two paths share an id")
	}
	if a != ProjectID("/repo/Runefile") {
		t.Error("the id of one path changed")
	}
	if got := ProjectDir("/base", "/repo/Runefile"); got != filepath.Join("/base", a) {
		t.Errorf("ProjectDir = %q", got)
	}
}

func TestWithin(t *testing.T) {
	sep := string(filepath.Separator)
	tests := []struct {
		dir, parent string
		want        bool
	}{
		{"/repo", "/repo", true},
		{"/repo/x/y", "/repo", true},
		{"/repo2", "/repo", false},
		{"/", "/repo", false},
		{"/repo/..x", "/repo", true},
		{"/other", "/repo", false},
	}
	for _, tt := range tests {
		dir := filepath.FromSlash(tt.dir)
		parent := filepath.FromSlash(tt.parent)
		if got := within(dir, parent); got != tt.want {
			t.Errorf("within(%q, %q) = %v, want %v", dir+sep, parent, got, tt.want)
		}
	}
}

func TestResolve(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := resolve(dir); got != dir {
		t.Errorf("an existing directory: %q, want %q", got, dir)
	}
	missing := filepath.Join(dir, "a", "b")
	if got := resolve(missing); got != missing {
		t.Errorf("a missing path: %q, want %q", got, missing)
	}
	if runtime.GOOS == "windows" {
		return
	}
	link := filepath.Join(dir, "link")
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if got, want := resolve(filepath.Join(link, "x", "y")), filepath.Join(target, "x", "y"); got != want {
		t.Errorf("through a link: %q, want %q", got, want)
	}
}

func TestRealDir(t *testing.T) {
	dir := t.TempDir()
	if _, err := realDir(dir); err != nil {
		t.Errorf("a directory: %v", err)
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var unusable *UnusableError
	if _, err := realDir(file); !errors.As(err, &unusable) || unusable.Reason != "is not a directory" {
		t.Errorf("a file: %v", err)
	}
	if _, err := realDir(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a missing path: %v", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if _, err := realDir(link); !errors.As(err, &unusable) || unusable.Error() != link+" is a symbolic link" {
		t.Errorf("a link: %v", err)
	}
}

func TestLocationErrorText(t *testing.T) {
	err := &LocationError{Dir: "/repo/.audit/ab", Project: "/repo"}
	want := "/repo/.audit/ab is inside the project (/repo); set RUNE_AUDIT_DIR to a directory outside it"
	if err.Error() != want {
		t.Errorf("got %q", err.Error())
	}
}
