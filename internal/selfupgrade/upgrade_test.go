package selfupgrade

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/rune-task-runner/rune/internal/selfupgrade/upgradetest"
)

func validConfig() Config {
	return Config{
		Version: "0.7.0", Stamped: true, ExePath: "/x/rune", GOOS: "linux", GOARCH: "amd64",
		Getenv: func(string) string { return "" }, Now: time.Now,
		HTTP: &http.Client{}, APIBase: "https://api.example", DownloadBase: "https://dl.example",
		Trust:  func(context.Context) (root.TrustedMaterial, error) { return nil, nil },
		Issuer: "issuer", Subject: "subject",
		Logf: func(string, ...any) {},
	}
}

func TestNew(t *testing.T) {
	if _, err := New(validConfig()); err != nil {
		t.Fatalf("New(valid) = %v", err)
	}
	broken := map[string]func(*Config){
		"ExePath":      func(c *Config) { c.ExePath = "" },
		"GOOS":         func(c *Config) { c.GOOS = "" },
		"GOARCH":       func(c *Config) { c.GOARCH = "" },
		"APIBase":      func(c *Config) { c.APIBase = "" },
		"DownloadBase": func(c *Config) { c.DownloadBase = "" },
		"Issuer":       func(c *Config) { c.Issuer = "" },
		"Subject":      func(c *Config) { c.Subject = "" },
		"HTTP":         func(c *Config) { c.HTTP = nil },
		"Trust":        func(c *Config) { c.Trust = nil },
		"Getenv":       func(c *Config) { c.Getenv = nil },
		"Now":          func(c *Config) { c.Now = nil },
		"Logf":         func(c *Config) { c.Logf = nil },
	}
	for name, mutate := range broken {
		cfg := validConfig()
		mutate(&cfg)
		if _, err := New(cfg); err == nil {
			t.Errorf("New with missing %s succeeded", name)
		}
	}
}

// install is a fake installed binary plus the log lines a run produced.
type install struct {
	dir  string
	path string
	body []byte
	logs []string
}

const installMode = 0o750

// newInstall places a fake binary at <tmp>/bin/<name> and returns a Config
// wired to rel for it.
func newInstall(t *testing.T, rel *upgradetest.Release, version string) (Config, *install) {
	t.Helper()
	// Resolved, because Run reports resolved paths: the temp directory is a
	// link on macOS and a short name on Windows.
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "bin")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	in := &install{dir: dir, path: filepath.Join(dir, "rune"), body: []byte("installed rune " + version)}
	if err := os.WriteFile(in.path, in.body, installMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(in.path, installMode); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		Version: version, Stamped: true, ExePath: in.path, GOOS: "linux", GOARCH: "amd64",
		Getenv: func(string) string { return "" }, Now: time.Now,
		HTTP: rel.Client, APIBase: rel.URL, DownloadBase: rel.URL,
		Trust:  func(context.Context) (root.TrustedMaterial, error) { return rel.Trust, nil },
		Issuer: upgradetest.Issuer, Subject: upgradetest.Subject,
		Logf: func(format string, args ...any) { in.logs = append(in.logs, fmt.Sprintf(format, args...)) },
	}
	return cfg, in
}

func mustNew(t *testing.T, cfg Config) *Upgrader {
	t.Helper()
	u, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// unchanged asserts the install is exactly as newInstall left it.
func (in *install) unchanged(t *testing.T, name string) {
	t.Helper()
	got, err := os.ReadFile(in.path)
	if err != nil || !bytes.Equal(got, in.body) {
		t.Errorf("%s: installed binary changed (err %v)", name, err)
	}
	in.modeIs(t, name)
	in.onlyBinary(t, name)
}

func (in *install) modeIs(t *testing.T, name string) {
	t.Helper()
	info, err := os.Stat(in.path)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != installMode {
		t.Errorf("%s: mode = %v, want %v", name, info.Mode().Perm(), os.FileMode(installMode))
	}
}

func (in *install) onlyBinary(t *testing.T, name string) {
	t.Helper()
	entries, err := os.ReadDir(in.dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		// Windows keeps the replaced binary until the next upgrade.
		if runtime.GOOS == "windows" && e.Name() == filepath.Base(in.path)+oldSuffix {
			continue
		}
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{filepath.Base(in.path)}) {
		t.Errorf("%s: install directory holds %v, want only the binary", name, names)
	}
}

func fixtureBinary(version string) []byte {
	return []byte("#!/bin/sh\necho fixture rune " + version + "\n")
}

func TestRunUpgrades(t *testing.T) {
	rel := upgradetest.New(t, upgradetest.Options{})
	cfg, in := newInstall(t, rel, "0.7.0")
	res, err := mustNew(t, cfg).Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := Result{Outcome: OutcomeUpgraded, Installed: "0.7.0", Target: "9.9.9", Path: in.path}
	if res != want {
		t.Errorf("Result = %+v, want %+v", res, want)
	}
	if got, _ := os.ReadFile(in.path); !bytes.Equal(got, fixtureBinary("9.9.9")) {
		t.Errorf("installed binary is not the fixture binary: %q", got)
	}
	in.modeIs(t, "upgrade")
	in.onlyBinary(t, "upgrade")
	wantLogs := []string{
		"installed 0.7.0",
		"latest 9.9.9",
		"downloading rune_9.9.9_linux_amd64.tar.gz",
		"verified signature and checksum",
	}
	if !slices.Equal(in.logs, wantLogs) {
		t.Errorf("log lines = %q, want %q", in.logs, wantLogs)
	}
}

func TestRunEveryPlatform(t *testing.T) {
	for _, p := range upgradetest.AllPlatforms() {
		t.Run(p[0]+"/"+p[1], func(t *testing.T) {
			rel := upgradetest.New(t, upgradetest.Options{})
			cfg, in := newInstall(t, rel, "0.7.0")
			cfg.GOOS, cfg.GOARCH = p[0], p[1]
			if _, err := mustNew(t, cfg).Run(context.Background()); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got, _ := os.ReadFile(in.path); !bytes.Equal(got, fixtureBinary("9.9.9")) {
				t.Errorf("installed binary is not the fixture binary")
			}
			if want := "downloading " + upgradetest.ArchiveName("9.9.9", p[0], p[1]); !slices.Contains(in.logs, want) {
				t.Errorf("logs %q lack %q", in.logs, want)
			}
		})
	}
}

func TestRunAlreadyCurrent(t *testing.T) {
	for _, version := range []string{"9.9.9", "10.0.0", "10.0.0-rc.1"} {
		rel := upgradetest.New(t, upgradetest.Options{})
		cfg, in := newInstall(t, rel, version)
		res, err := mustNew(t, cfg).Run(context.Background())
		if err != nil {
			t.Fatalf("%s: %v", version, err)
		}
		if res.Outcome != OutcomeCurrent || res.Installed != version || res.Target != "9.9.9" {
			t.Errorf("%s: Result = %+v", version, res)
		}
		in.unchanged(t, version)
		if rel.Requests() != 1 {
			t.Errorf("%s: %d requests, want only the latest-release lookup", version, rel.Requests())
		}
	}
}

func TestRunThroughSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need extra rights on Windows")
	}
	rel := upgradetest.New(t, upgradetest.Options{})
	cfg, in := newInstall(t, rel, "0.7.0")
	link := filepath.Join(filepath.Dir(in.dir), "link")
	if err := os.Symlink(in.path, link); err != nil {
		t.Fatal(err)
	}
	cfg.ExePath = link
	res, err := mustNew(t, cfg).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(in.path)
	if err != nil {
		t.Fatal(err)
	}
	if res.Path != real {
		t.Errorf("Path = %q, want the resolved file %q", res.Path, real)
	}
	if got, _ := os.ReadFile(in.path); !bytes.Equal(got, fixtureBinary("9.9.9")) {
		t.Error("the file behind the link was not replaced")
	}
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("link is no longer a symbolic link (err %v)", err)
	}
	if dest, _ := os.Readlink(link); dest != in.path {
		t.Errorf("link points at %q, want %q", dest, in.path)
	}
}

func TestRunUnsupportedPlatform(t *testing.T) {
	rel := upgradetest.New(t, upgradetest.Options{Platforms: [][2]string{{"linux", "amd64"}, {"linux", "arm64"}}})
	cfg, in := newInstall(t, rel, "0.7.0")
	cfg.GOOS = "windows"
	_, err := mustNew(t, cfg).Run(context.Background())
	var e *Error
	if !errors.As(err, &e) || e.Reason != ReasonUnsupportedPlatform || e.Subject != "windows/amd64" || e.Target != "9.9.9" || e.Installed != "0.7.0" {
		t.Errorf("err = %#v, want unsupported-platform for windows/amd64", err)
	}
	in.unchanged(t, "unsupported platform")
}

func TestRunMissingExecutable(t *testing.T) {
	rel := upgradetest.New(t, upgradetest.Options{})
	cfg, _ := newInstall(t, rel, "0.7.0")
	cfg.ExePath = filepath.Join(t.TempDir(), "absent")
	if _, err := mustNew(t, cfg).Run(context.Background()); err == nil {
		t.Error("Run succeeded for an executable that does not exist")
	}
	if rel.Requests() != 0 {
		t.Errorf("%d requests made for a missing executable", rel.Requests())
	}
}

// US2 end to end: a release that is wrong in exactly one way never replaces
// the binary and leaves nothing behind.
func TestRunTampered(t *testing.T) {
	flip := func(b []byte) []byte { c := append([]byte(nil), b...); c[len(c)/2] ^= 0xff; return c }
	extraLine := func(b []byte) []byte { return append(b, []byte(strings.Repeat("ab", 32)+"  evil.tar.gz\n")...) }
	tests := map[string]struct {
		opts upgradetest.Options
		want Reason
	}{
		"altered archive":          {upgradetest.Options{MutateArchive: flip}, ReasonChecksumMismatch},
		"altered checksum list":    {upgradetest.Options{MutateChecksums: extraLine}, ReasonUnverified},
		"missing signature":        {upgradetest.Options{OmitSignature: true}, ReasonUnverified},
		"missing checksum list":    {upgradetest.Options{OmitChecksums: true}, ReasonUnverified},
		"signed by other workflow": {upgradetest.Options{Subject: ciSubject}, ReasonUnverified},
		"second binary entry":      {upgradetest.Options{ExtraEntries: []upgradetest.Entry{{Name: "rune", Body: []byte("evil")}}}, ReasonBadArchive},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rel := upgradetest.New(t, tt.opts)
			cfg, in := newInstall(t, rel, "0.7.0")
			_, err := mustNew(t, cfg).Run(context.Background())
			var e *Error
			if !errors.As(err, &e) || e.Reason != tt.want {
				t.Fatalf("err = %v, want reason %q", err, tt.want)
			}
			if e.Installed != "0.7.0" || e.Target != "9.9.9" {
				t.Errorf("error versions = %q, %q", e.Installed, e.Target)
			}
			in.unchanged(t, name)
		})
	}
}

func TestRunIgnoresHostileEntries(t *testing.T) {
	rel := upgradetest.New(t, upgradetest.Options{ExtraEntries: []upgradetest.Entry{
		{Name: "../../evil", Body: []byte("evil")},
		{Name: "link", Body: []byte("/etc/passwd"), Typeflag: tar.TypeSymlink},
	}})
	cfg, in := newInstall(t, rel, "0.7.0")
	if _, err := mustNew(t, cfg).Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	in.onlyBinary(t, "hostile entries")
	parent := filepath.Dir(in.dir)
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("files appeared outside the install directory: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(parent), "evil")); !os.IsNotExist(err) {
		t.Error("an archive entry escaped the install directory")
	}
}

// bigBinary is large and incompressible, so its archive spans many chunks.
func bigBinary() []byte {
	b := make([]byte, 400<<10)
	r := rand.New(rand.NewPCG(1, 2))
	for i := range b {
		b[i] = byte(r.UintN(256))
	}
	return b
}

func TestRunFailuresLeaveInstallIntact(t *testing.T) {
	t.Run("download cut off", func(t *testing.T) {
		rel := upgradetest.New(t, upgradetest.Options{CutAfter: 1000, Binary: bigBinary()})
		cfg, in := newInstall(t, rel, "0.7.0")
		_, err := mustNew(t, cfg).Run(context.Background())
		var e *Error
		if !errors.As(err, &e) || e.Reason != ReasonDownloadFailed || e.Subject != "rune_9.9.9_linux_amd64.tar.gz" {
			t.Errorf("err = %#v, want download-failed naming the archive", err)
		}
		in.unchanged(t, "cut off")
	})

	t.Run("cancelled during download", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		rel := upgradetest.New(t, upgradetest.Options{OnArchive: cancel, LatencyPerChunk: 50 * time.Millisecond, Binary: bigBinary()})
		cfg, in := newInstall(t, rel, "0.7.0")
		_, err := mustNew(t, cfg).Run(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
		in.unchanged(t, "cancelled")
	})

	t.Run("deadline during download", func(t *testing.T) {
		rel := upgradetest.New(t, upgradetest.Options{LatencyPerChunk: 200 * time.Millisecond, Binary: bigBinary()})
		cfg, in := newInstall(t, rel, "0.7.0")
		u := mustNew(t, cfg)
		u.runLimit = 300 * time.Millisecond
		_, err := u.Run(context.Background())
		wantReason(t, "deadline", err, ReasonTimeout)
		in.unchanged(t, "deadline")
	})

	t.Run("live lock", func(t *testing.T) {
		rel := upgradetest.New(t, upgradetest.Options{})
		cfg, in := newInstall(t, rel, "0.7.0")
		lock := filepath.Join(in.dir, lockName)
		if err := os.WriteFile(lock, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		age(t, lock, 14*time.Minute)
		_, err := mustNew(t, cfg).Run(context.Background())
		var e *Error
		if !errors.As(err, &e) || e.Reason != ReasonInProgress || e.Subject != in.path {
			t.Errorf("err = %#v, want in-progress naming %s", err, in.path)
		}
		if rel.Requests() != 0 {
			t.Errorf("%d requests made while another upgrade holds the lock", rel.Requests())
		}
		if _, err := os.Stat(lock); err != nil {
			t.Errorf("another upgrade's lock was removed: %v", err)
		}
		if got, _ := os.ReadFile(in.path); !bytes.Equal(got, in.body) {
			t.Error("binary changed")
		}
	})

	t.Run("stale leftovers", func(t *testing.T) {
		rel := upgradetest.New(t, upgradetest.Options{})
		cfg, in := newInstall(t, rel, "0.7.0")
		for _, name := range []string{lockName, ".rune-upgrade-123"} {
			p := filepath.Join(in.dir, name)
			if err := os.WriteFile(p, []byte("left by a killed run"), 0o600); err != nil {
				t.Fatal(err)
			}
			age(t, p, 16*time.Minute)
		}
		res, err := mustNew(t, cfg).Run(context.Background())
		if err != nil || res.Outcome != OutcomeUpgraded {
			t.Fatalf("Run = %+v, %v", res, err)
		}
		in.onlyBinary(t, "stale leftovers")
	})

	// A run paused past the stale limit loses its lock to another run. It
	// must not replace the binary, nor remove the new owner's lock.
	t.Run("lock taken over mid-run", func(t *testing.T) {
		var lock string
		rel := upgradetest.New(t, upgradetest.Options{OnArchive: func() {
			_ = os.WriteFile(lock, []byte("1 another-host\n"), 0o600)
		}})
		cfg, in := newInstall(t, rel, "0.7.0")
		lock = filepath.Join(in.dir, lockName)
		_, err := mustNew(t, cfg).Run(context.Background())
		var e *Error
		if !errors.As(err, &e) || e.Reason != ReasonInProgress || e.Subject != in.path {
			t.Errorf("err = %#v, want in-progress naming the binary", err)
		}
		if got, _ := os.ReadFile(in.path); !bytes.Equal(got, in.body) {
			t.Error("the binary was replaced after the lock was lost")
		}
		in.modeIs(t, "lock taken over")
		if b, _ := os.ReadFile(lock); string(b) != "1 another-host\n" {
			t.Errorf("the new owner's lock was removed or changed: %q", b)
		}
	})

	// A run killed a moment ago leaves a fresh lock and a fresh staged file.
	t.Run("leftovers of a killed run", func(t *testing.T) {
		host, err := os.Hostname()
		if err != nil || host == "" {
			t.Skip("no hostname")
		}
		rel := upgradetest.New(t, upgradetest.Options{})
		cfg, in := newInstall(t, rel, "0.7.0")
		leftovers := map[string]string{
			lockName:            fmt.Sprintf("%d %s\n", deadPid(t), host),
			".rune-upgrade-123": "half an archive",
		}
		for name, content := range leftovers {
			if err := os.WriteFile(filepath.Join(in.dir, name), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		res, err := mustNew(t, cfg).Run(context.Background())
		if err != nil || res.Outcome != OutcomeUpgraded {
			t.Fatalf("Run = %+v, %v", res, err)
		}
		in.onlyBinary(t, "leftovers of a killed run")
	})

	t.Run("archive over the limit", func(t *testing.T) {
		rel := upgradetest.New(t, upgradetest.Options{})
		cfg, in := newInstall(t, rel, "0.7.0")
		u := mustNew(t, cfg)
		u.archiveLimit = 10
		_, err := u.Run(context.Background())
		wantReason(t, "archive limit", err, ReasonTooLarge)
		in.unchanged(t, "archive limit")
	})

	t.Run("binary over the limit", func(t *testing.T) {
		rel := upgradetest.New(t, upgradetest.Options{})
		cfg, in := newInstall(t, rel, "0.7.0")
		u := mustNew(t, cfg)
		u.binaryLimit = 4
		_, err := u.Run(context.Background())
		var e *Error
		if !errors.As(err, &e) || e.Reason != ReasonTooLarge || e.Subject != "rune_9.9.9_linux_amd64.tar.gz" {
			t.Errorf("err = %#v, want too-large naming the archive", err)
		}
		in.unchanged(t, "binary limit")
	})

	t.Run("replacement fails", func(t *testing.T) {
		rel := upgradetest.New(t, upgradetest.Options{})
		cfg, in := newInstall(t, rel, "0.7.0")
		u := mustNew(t, cfg)
		u.replace = func(string, string) error { return errors.New("disk on fire") }
		_, err := u.Run(context.Background())
		var e *Error
		if !errors.As(err, &e) || e.Reason != ReasonReplaceFailed || e.Subject != in.path {
			t.Errorf("err = %#v, want replace-failed naming %s", err, in.path)
		}
		in.unchanged(t, "replacement fails")
	})

	t.Run("trust root unreachable", func(t *testing.T) {
		rel := upgradetest.New(t, upgradetest.Options{})
		cfg, in := newInstall(t, rel, "0.7.0")
		cfg.Trust = func(context.Context) (root.TrustedMaterial, error) {
			return nil, fmt.Errorf("tuf: %w", &Error{Reason: ReasonOffline, Subject: "tuf-repo-cdn.sigstore.dev", Err: errors.New("no route")})
		}
		_, err := mustNew(t, cfg).Run(context.Background())
		wantReason(t, "trust offline", err, ReasonOffline)
		in.unchanged(t, "trust offline")

		cfg.Trust = func(context.Context) (root.TrustedMaterial, error) { return nil, errors.New("bad metadata") }
		_, err = mustNew(t, cfg).Run(context.Background())
		wantReason(t, "trust broken", err, ReasonUnverified)
		in.unchanged(t, "trust broken")
	})

	t.Run("unwritable directory", func(t *testing.T) {
		if os.Geteuid() == 0 || runtime.GOOS == "windows" {
			t.Skip("needs a non-root user on a system that enforces directory permissions")
		}
		rel := upgradetest.New(t, upgradetest.Options{})
		cfg, in := newInstall(t, rel, "0.7.0")
		if err := os.Chmod(in.dir, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(in.dir, 0o755) })
		_, err := mustNew(t, cfg).Run(context.Background())
		var e *Error
		if !errors.As(err, &e) || e.Reason != ReasonNotWritable || e.Subject != in.dir {
			t.Errorf("err = %#v, want not-writable naming %s", err, in.dir)
		}
		if rel.Requests() != 0 {
			t.Errorf("%d requests made before the write check", rel.Requests())
		}
		in.unchanged(t, "unwritable")
	})
}

// Eight upgrades of one install at once: one replaces the binary, the rest
// see the lock. The archive is held back until the losers have returned, so
// no late starter can take the lock after the winner finishes.
func TestRunConcurrent(t *testing.T) {
	hold := make(chan struct{})
	rel := upgradetest.New(t, upgradetest.Options{HoldArchive: hold})
	base, in := newInstall(t, rel, "0.7.0")

	const n = 8
	type outcome struct {
		res Result
		err error
	}
	results := make(chan outcome, n)
	start := make(chan struct{})
	for range n {
		cfg := base
		cfg.Logf = func(string, ...any) {}
		u := mustNew(t, cfg)
		go func() {
			<-start
			res, err := u.Run(context.Background())
			results <- outcome{res, err}
		}()
	}
	close(start)

	var upgraded, inProgress int
	collect := func() {
		o := <-results
		var e *Error
		switch {
		case o.err == nil && o.res.Outcome == OutcomeUpgraded:
			upgraded++
		case errors.As(o.err, &e) && e.Reason == ReasonInProgress:
			inProgress++
		default:
			t.Errorf("unexpected outcome: %+v, %v", o.res, o.err)
		}
	}
	for range n - 1 {
		collect()
	}
	if rel.ArchiveRequests() > 1 {
		t.Errorf("%d archive requests while the lock was held", rel.ArchiveRequests())
	}
	close(hold)
	collect()

	if upgraded != 1 || inProgress != n-1 {
		t.Errorf("upgraded %d, in progress %d; want 1 and %d", upgraded, inProgress, n-1)
	}
	if got, _ := os.ReadFile(in.path); !bytes.Equal(got, fixtureBinary("9.9.9")) {
		t.Error("the binary is not the complete new version")
	}
	in.onlyBinary(t, "concurrent")
}

func TestRunRefusesOtherOrigins(t *testing.T) {
	tests := map[string]struct {
		mutate func(*Config)
		want   Origin
	}{
		"development build":   {func(c *Config) { c.Version, c.Stamped = "dev", false }, OriginDevelopment},
		"unparseable version": {func(c *Config) { c.Version = "banana" }, OriginDevelopment},
		"go install":          {func(c *Config) { c.Stamped = false }, OriginGoInstall},
		"container": {func(c *Config) {
			c.Getenv = func(k string) string {
				if k == "RUNE_CONTAINER" {
					return "1"
				}
				return ""
			}
		}, OriginContainer},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rel := upgradetest.New(t, upgradetest.Options{})
			cfg, in := newInstall(t, rel, "0.7.0")
			tt.mutate(&cfg)
			res, err := mustNew(t, cfg).Run(context.Background())
			var r *Refused
			if !errors.As(err, &r) || r.Origin != tt.want || r.Path != in.path || r.Version != cfg.Version {
				t.Fatalf("err = %#v, want a refusal for %s at %s", err, tt.want, in.path)
			}
			if res.Installed != cfg.Version || res.Path != in.path {
				t.Errorf("Result = %+v", res)
			}
			if rel.Requests() != 0 {
				t.Errorf("%d requests made before refusing", rel.Requests())
			}
			if len(in.logs) != 1 || in.logs[0] != "installed "+cfg.Version {
				t.Errorf("log lines = %q, want only the installed line", in.logs)
			}
			in.unchanged(t, name)
		})
	}
}

func TestRunRefusesPackageManagerPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need extra rights on Windows")
	}
	rel := upgradetest.New(t, upgradetest.Options{})
	cfg, _ := newInstall(t, rel, "0.7.0")
	base := t.TempDir()
	// Resolve the base first: on macOS the temp directory is itself a link.
	base, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	cask := filepath.Join(base, "opt", "homebrew", "Caskroom", "rune", "0.7.0")
	bin := filepath.Join(base, "home", "bin")
	for _, d := range []string{cask, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	real := filepath.Join(cask, "rune")
	if err := os.WriteFile(real, []byte("brewed rune"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(bin, "rune")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	for name, exe := range map[string]string{"direct": real, "through a link": link} {
		cfg.ExePath = exe
		_, err := mustNew(t, cfg).Run(context.Background())
		var r *Refused
		if !errors.As(err, &r) || r.Origin != OriginHomebrew || r.Path != real {
			t.Errorf("%s: err = %#v, want a Homebrew refusal for %s", name, err, real)
		}
		if got, _ := os.ReadFile(real); string(got) != "brewed rune" {
			t.Errorf("%s: the Homebrew binary changed", name)
		}
	}
	if rel.Requests() != 0 {
		t.Errorf("%d requests made before refusing", rel.Requests())
	}
	entries, _ := os.ReadDir(cask)
	if len(entries) != 1 {
		t.Errorf("a refusal left files in the package manager's directory: %v", entries)
	}
}
