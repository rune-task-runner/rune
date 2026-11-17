package planstore

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenRegular(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	regular := filepath.Join(dir, "regular")
	put(t, regular, "content")

	f, info, err := openRegular(regular)
	if err != nil {
		t.Fatalf("openRegular of a regular file: %v", err)
	}
	data, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil || string(data) != "content" || info.Size() != 7 {
		t.Errorf("read %q (size %d), err %v", data, info.Size(), err)
	}

	tests := []struct {
		name  string
		posix bool
		path  func(t *testing.T) string
		want  fileProblem
	}{
		{name: "missing", path: func(*testing.T) string { return filepath.Join(dir, "absent") }, want: problemMissing},
		{name: "directory", path: func(*testing.T) string { return dir }, want: problemNotRegular},
		{
			name:  "symbolic link to a regular file",
			posix: true,
			path: func(t *testing.T) string {
				link := filepath.Join(t.TempDir(), "link")
				if err := os.Symlink(regular, link); err != nil {
					t.Fatal(err)
				}
				return link
			},
			want: problemSymlink,
		},
		{
			name:  "dangling symbolic link",
			posix: true,
			path: func(t *testing.T) string {
				link := filepath.Join(t.TempDir(), "link")
				if err := os.Symlink(filepath.Join(dir, "absent"), link); err != nil {
					t.Fatal(err)
				}
				return link
			},
			want: problemSymlink,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.posix {
				posixOnly(t)
			}
			f, _, err := openRegular(tt.path(t))
			if f != nil {
				_ = f.Close()
				t.Error("openRegular returned a handle")
			}
			got, ok := errors.AsType[fileProblem](err)
			if !ok || got != tt.want {
				t.Errorf("openRegular error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestOpenRegularBelowAFile(t *testing.T) {
	t.Parallel()
	posixOnly(t)
	file := filepath.Join(t.TempDir(), "file")
	put(t, file, "x")
	_, _, err := openRegular(filepath.Join(file, "child"))
	if err == nil {
		t.Fatal("openRegular below a file returned no error")
	}
	if _, ok := errors.AsType[fileProblem](err); ok {
		t.Errorf("openRegular error = %v, want the system's error", err)
	}
}

func TestFileProblemText(t *testing.T) {
	t.Parallel()
	want := map[fileProblem]string{
		problemMissing:    "is missing",
		problemNotRegular: "is not a regular file",
		problemSymlink:    "is a symbolic link",
	}
	for problem, text := range want {
		if got := problem.Error(); got != text {
			t.Errorf("Error() = %q, want %q", got, text)
		}
	}
}

func TestEnsureDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir, err := ensureDir(root)
	if err != nil {
		t.Fatalf("ensureDir: %v", err)
	}
	if want := filepath.Join(root, ".rune", "plans"); dir != want {
		t.Errorf("ensureDir = %q, want %q", dir, want)
	}
	if got := mustRead(t, filepath.Join(dir, ".gitignore")); got != "*\n" {
		t.Errorf(".gitignore = %q", got)
	}

	put(t, filepath.Join(dir, ".gitignore"), "kept\n")
	put(t, filepath.Join(dir, "other"), "kept\n")
	again, err := ensureDir(root)
	if err != nil || again != dir {
		t.Fatalf("second ensureDir = %q, %v", again, err)
	}
	if got := mustRead(t, filepath.Join(dir, ".gitignore")); got != "kept\n" {
		t.Errorf("ensureDir rewrote .gitignore: %q", got)
	}
	if !exists(filepath.Join(dir, "other")) {
		t.Error("ensureDir removed a file")
	}
}

func TestEnsureDirModes(t *testing.T) {
	t.Parallel()
	posixOnly(t)
	root := t.TempDir()
	runeDir := filepath.Join(root, ".rune")
	plans := filepath.Join(runeDir, "plans")
	if err := os.MkdirAll(plans, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(plans, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureDir(root); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(plans)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("plans has mode %o, want 700", info.Mode().Perm())
	}
	info, err = os.Lstat(filepath.Join(plans, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Errorf(".gitignore has mode %o", info.Mode().Perm())
	}

	fresh := t.TempDir()
	if _, err := ensureDir(fresh); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(fresh, ".rune"), filepath.Join(fresh, ".rune", "plans")} {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Errorf("%s has mode %o, want 700", path, info.Mode().Perm())
		}
	}
}

func TestEnsureDirRefuses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		posix  bool
		setup  func(t *testing.T, root string) string
		reason string
	}{
		{
			name:  ".rune is a symbolic link",
			posix: true,
			setup: func(t *testing.T, root string) string {
				path := filepath.Join(root, ".rune")
				if err := os.Symlink(t.TempDir(), path); err != nil {
					t.Fatal(err)
				}
				return path
			},
			reason: "is a symbolic link",
		},
		{
			name:  ".rune is a dangling symbolic link",
			posix: true,
			setup: func(t *testing.T, root string) string {
				path := filepath.Join(root, ".rune")
				if err := os.Symlink(filepath.Join(root, "absent"), path); err != nil {
					t.Fatal(err)
				}
				return path
			},
			reason: "is a symbolic link",
		},
		{
			name:  "plans is a symbolic link",
			posix: true,
			setup: func(t *testing.T, root string) string {
				if err := os.Mkdir(filepath.Join(root, ".rune"), 0o700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(root, ".rune", "plans")
				if err := os.Symlink(t.TempDir(), path); err != nil {
					t.Fatal(err)
				}
				return path
			},
			reason: "is a symbolic link",
		},
		{
			name: ".rune is a file",
			setup: func(t *testing.T, root string) string {
				path := filepath.Join(root, ".rune")
				put(t, path, "x")
				return path
			},
			reason: "is not a directory",
		},
		{
			name: "plans is a file",
			setup: func(t *testing.T, root string) string {
				if err := os.Mkdir(filepath.Join(root, ".rune"), 0o700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(root, ".rune", "plans")
				put(t, path, "x")
				return path
			},
			reason: "is not a directory",
		},
		{
			name:  "plans belongs to another user",
			posix: true,
			setup: func(t *testing.T, root string) string {
				if os.Getuid() != 0 {
					t.Skip("only root can give a directory away")
				}
				path := filepath.Join(root, ".rune", "plans")
				if err := os.MkdirAll(path, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Chown(path, 12345, 12345); err != nil {
					t.Fatal(err)
				}
				return path
			},
			reason: "belongs to another user",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.posix {
				posixOnly(t)
			}
			root := t.TempDir()
			path := tt.setup(t, root)
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}

			for name, call := range map[string]func() error{
				"ensureDir": func() error { _, err := ensureDir(root); return err },
				"checkDir":  func() error { _, err := checkDir(root); return err },
			} {
				var ue *UnusableError
				if err := call(); !errors.As(err, &ue) {
					t.Errorf("%s error = %v, want an *UnusableError", name, err)
				} else if ue.Path != path || ue.Reason != tt.reason {
					t.Errorf("%s: UnusableError = %+v, want %q %q", name, ue, path, tt.reason)
				}
			}
			after, err := os.Lstat(path)
			if err != nil || after.Mode() != before.Mode() {
				t.Errorf("the refused path changed: %v, %v", after, err)
			}
			if exists(filepath.Join(path, ".gitignore")) {
				t.Error("ensureDir wrote into a directory it refused")
			}
		})
	}
}

func TestEnsureDirWithoutARoot(t *testing.T) {
	t.Parallel()
	_, err := ensureDir(filepath.Join(t.TempDir(), "absent"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ensureDir error = %v, want one wrapping fs.ErrNotExist", err)
	}
}

func TestCheckDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if ok, err := checkDir(filepath.Join(root, "absent")); ok || err != nil {
		t.Errorf("checkDir without a root = %v, %v", ok, err)
	}
	if ok, err := checkDir(root); ok || err != nil {
		t.Errorf("checkDir without .rune = %v, %v", ok, err)
	}
	if err := os.Mkdir(filepath.Join(root, ".rune"), 0o700); err != nil {
		t.Fatal(err)
	}
	if ok, err := checkDir(root); ok || err != nil {
		t.Errorf("checkDir without plans = %v, %v", ok, err)
	}
	if entries, _ := os.ReadDir(filepath.Join(root, ".rune")); len(entries) != 0 {
		t.Errorf("checkDir created %v", entries)
	}
	if _, err := ensureDir(root); err != nil {
		t.Fatal(err)
	}
	if ok, err := checkDir(root); !ok || err != nil {
		t.Errorf("checkDir of a store = %v, %v", ok, err)
	}
}

func TestCheckDirBelowAFile(t *testing.T) {
	t.Parallel()
	posixOnly(t)
	file := filepath.Join(t.TempDir(), "file")
	put(t, file, "x")
	ok, err := checkDir(file)
	if ok || err == nil {
		t.Errorf("checkDir below a file = %v, %v; want an error", ok, err)
	}
	var ue *UnusableError
	if errors.As(err, &ue) {
		t.Errorf("checkDir error = %v, want the system's error", err)
	}
}

func TestRealDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	info, err := realDir(dir)
	if err != nil || !info.IsDir() {
		t.Errorf("realDir of a directory = %v, %v", info, err)
	}
	if _, err := realDir(filepath.Join(dir, "absent")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("realDir of a missing path = %v, want fs.ErrNotExist", err)
	}
	file := filepath.Join(dir, "file")
	put(t, file, "x")
	var ue *UnusableError
	if _, err := realDir(file); !errors.As(err, &ue) || ue.Reason != "is not a directory" || ue.Path != file {
		t.Errorf("realDir of a file = %v", err)
	}
}

func TestOwnedByCurrentUser(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !ownedByCurrentUser(info) {
		t.Error("a directory this process made is not its own")
	}
	if os.Getuid() != 0 {
		return
	}
	if err := os.Chown(dir, 12345, 12345); err != nil {
		t.Fatal(err)
	}
	if info, err = os.Lstat(dir); err != nil {
		t.Fatal(err)
	}
	if ownedByCurrentUser(info) {
		t.Error("a directory given away still counts as owned")
	}
}

// The path flips between a regular file and a link to another file while it
// is opened. A handle that comes back must never be the link's target.
func TestOpenRegularDuringASwap(t *testing.T) {
	t.Parallel()
	posixOnly(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact")
	secret := filepath.Join(dir, "secret")
	put(t, path, "real")
	put(t, secret, "secret")

	stop := make(chan struct{})
	swapped := make(chan error, 1)
	go func() {
		staged := filepath.Join(dir, "staged")
		for {
			select {
			case <-stop:
				swapped <- nil
				return
			default:
			}
			if err := os.Symlink(secret, staged); err != nil {
				swapped <- err
				return
			}
			if err := os.Rename(staged, path); err != nil {
				swapped <- err
				return
			}
			if err := os.WriteFile(staged, []byte("real"), 0o600); err != nil {
				swapped <- err
				return
			}
			if err := os.Rename(staged, path); err != nil {
				swapped <- err
				return
			}
		}
	}()

	for range 3000 {
		f, _, err := openRegular(path)
		if err != nil {
			if _, ok := errors.AsType[fileProblem](err); !ok {
				t.Errorf("openRegular error = %v, want a file problem", err)
			}
			continue
		}
		data, err := io.ReadAll(f)
		_ = f.Close()
		if err != nil || string(data) != "real" {
			t.Fatalf("openRegular followed the link: read %q, %v", data, err)
		}
	}
	close(stop)
	if err := <-swapped; err != nil {
		t.Fatal(err)
	}
}
