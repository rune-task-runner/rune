package selfupgrade

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/rune-task-runner/rune/internal/semver"
)

// Outcome is what a successful run did.
type Outcome string

const (
	OutcomeUpgraded Outcome = "upgraded"
	OutcomeCurrent  Outcome = "current"
)

// Result describes a run. Installed and Path are set as soon as they are
// known, so they are meaningful next to an error too.
type Result struct {
	Outcome   Outcome
	Installed string
	Target    string
	Path      string
}

// Config is everything an upgrade depends on. All of it is injected so tests
// run without the network and nothing lives in package globals.
type Config struct {
	// Version is the version this binary reports; Stamped says whether it was
	// set at build time (a release build) or read from module build info.
	Version string
	Stamped bool
	ExePath string
	GOOS    string
	GOARCH  string
	Getenv  func(string) string
	Now     func() time.Time

	HTTP         *http.Client
	APIBase      string
	DownloadBase string

	// Trust returns the Sigstore trust root; Issuer and Subject are the only
	// signer identity accepted.
	Trust   func(context.Context) (root.TrustedMaterial, error)
	Issuer  string
	Subject string

	// Logf receives progress lines, without a prefix or newline.
	Logf func(format string, args ...any)
}

const (
	archiveLimit = 100 << 20
	binaryLimit  = 200 << 20
)

// Upgrader runs one self-upgrade.
type Upgrader struct {
	cfg Config
	dl  *downloader

	archiveLimit int64
	binaryLimit  int64
	runLimit     time.Duration
	// replace is replaceBinary; tests substitute it to fail the final step.
	replace func(staged, target string) error
}

// New validates cfg and returns an Upgrader.
func New(cfg Config) (*Upgrader, error) {
	switch {
	case cfg.ExePath == "":
		return nil, errors.New("selfupgrade: ExePath is empty")
	case cfg.GOOS == "" || cfg.GOARCH == "":
		return nil, errors.New("selfupgrade: GOOS and GOARCH are required")
	case cfg.APIBase == "" || cfg.DownloadBase == "":
		return nil, errors.New("selfupgrade: APIBase and DownloadBase are required")
	case cfg.Issuer == "" || cfg.Subject == "":
		return nil, errors.New("selfupgrade: Issuer and Subject are required")
	case cfg.HTTP == nil:
		return nil, errors.New("selfupgrade: HTTP client is nil")
	case cfg.Trust == nil:
		return nil, errors.New("selfupgrade: Trust is nil")
	case cfg.Getenv == nil || cfg.Now == nil || cfg.Logf == nil:
		return nil, errors.New("selfupgrade: Getenv, Now and Logf are required")
	}
	dl := newDownloader(cfg.HTTP)
	dl.userAgent = "rune/" + cfg.Version
	return &Upgrader{
		cfg: cfg, dl: dl,
		archiveLimit: archiveLimit, binaryLimit: binaryLimit, runLimit: runLimit,
		replace: replaceBinary,
	}, nil
}

// Run performs the upgrade. The installed binary changes only in the last
// step, after the release's signature and the archive's checksum have passed.
func (u *Upgrader) Run(ctx context.Context) (Result, error) {
	cfg := u.cfg
	res := Result{Installed: cfg.Version}
	cfg.Logf("installed %s", cfg.Version)

	target, err := filepath.EvalSymlinks(cfg.ExePath)
	if err != nil {
		return res, fmt.Errorf("cannot locate the running executable: %w", err)
	}
	res.Path = target
	info, err := os.Stat(target)
	if err != nil {
		return res, fmt.Errorf("cannot locate the running executable: %w", err)
	}
	// Decided before the lock and before any network use: an install owned by
	// something else is left exactly as it is.
	if origin := detectOrigin(cfg.Version, cfg.Stamped, target, cfg.GOOS, cfg.Getenv); origin != OriginStandalone {
		return res, &Refused{Origin: origin, Path: target, Version: cfg.Version}
	}
	installed, _ := semver.Parse(cfg.Version) // a version that does not parse is a development origin

	ctx, cancel := context.WithTimeout(ctx, u.runLimit)
	defer cancel()

	// The lock is taken before the first network call and held to the end, so
	// a second upgrade of the same binary stops here.
	dir := filepath.Dir(target)
	release, err := acquireLock(dir, cfg.Now())
	if err != nil {
		var e *Error
		if errors.As(err, &e) && e.Reason == ReasonInProgress {
			e.Subject = target
		}
		return res, u.annotate(err, res)
	}
	defer release()
	sweepStaged(dir)
	removeOld(target)

	tag, latest, err := u.latest(ctx)
	if err != nil {
		return res, u.annotate(err, res)
	}
	version := strings.TrimPrefix(tag, "v")
	res.Target = version
	cfg.Logf("latest %s", version)
	if installed.Compare(latest) >= 0 {
		res.Outcome = OutcomeCurrent
		return res, nil
	}

	if err := u.install(ctx, tag, version, target, info.Mode().Perm()); err != nil {
		return res, u.annotate(err, res)
	}
	res.Outcome = OutcomeUpgraded
	return res, nil
}

// install downloads, proves and swaps in the release binary.
func (u *Upgrader) install(ctx context.Context, tag, version, target string, mode os.FileMode) error {
	cfg := u.cfg
	checksums, err := u.fetchSigned(ctx, tag, "checksums.txt")
	if err != nil {
		return err
	}
	bundleJSON, err := u.fetchSigned(ctx, tag, "checksums.txt.sigstore.json")
	if err != nil {
		return err
	}
	trust, err := cfg.Trust(ctx)
	if err != nil {
		var e *Error
		if errors.As(err, &e) || errors.Is(err, context.Canceled) {
			return err
		}
		return &Error{Reason: ReasonUnverified, Err: fmt.Errorf("cannot load the Sigstore trust root: %w", err)}
	}
	if err := verifyChecksums(trust, cfg.Issuer, cfg.Subject, checksums, bundleJSON); err != nil {
		return err
	}
	sums, err := parseChecksums(checksums)
	if err != nil {
		return err
	}
	archive := archiveName(version, cfg.GOOS, cfg.GOARCH)
	want, ok := sums[archive]
	if !ok {
		return &Error{Reason: ReasonUnsupportedPlatform, Subject: cfg.GOOS + "/" + cfg.GOARCH}
	}

	dir := filepath.Dir(target)
	notWritable := func(err error) error { return &Error{Reason: ReasonNotWritable, Subject: dir, Err: err} }
	staged, err := stage(dir)
	if err != nil {
		return notWritable(err)
	}
	defer removeStaged(staged)
	cfg.Logf("downloading %s", archive)
	got, err := u.dl.fetchFile(ctx, u.assetURL(tag, archive), staged, u.archiveLimit)
	if err != nil {
		return err
	}
	if got != want {
		return &Error{Reason: ReasonChecksumMismatch, Subject: archive}
	}

	binary, err := stage(dir)
	if err != nil {
		return notWritable(err)
	}
	defer removeStaged(binary)
	if err := extractBinary(staged, cfg.GOOS, binary, u.binaryLimit); err != nil {
		var e *Error
		if errors.As(err, &e) && e.Subject == "" {
			e.Subject = archive
			if e.Reason == ReasonNotWritable {
				e.Subject = dir
			}
		}
		return err
	}
	if err := binary.Chmod(mode); err != nil {
		return notWritable(err)
	}
	if err := binary.Sync(); err != nil {
		return notWritable(err)
	}
	if err := binary.Close(); err != nil {
		return notWritable(err)
	}
	cfg.Logf("verified signature and checksum")
	// A run that lost its lock (it was paused past the stale limit and
	// another took over) must not replace the binary under the new owner.
	if !ownsLock(dir) {
		return &Error{Reason: ReasonInProgress, Subject: target}
	}
	if err := u.replace(binary.Name(), target); err != nil {
		return &Error{Reason: ReasonReplaceFailed, Subject: target, Err: err}
	}
	return nil
}

// fetchSigned downloads the checksum list or its signature. A release that
// does not publish one of them cannot be verified, which is what a 404 means.
func (u *Upgrader) fetchSigned(ctx context.Context, tag, name string) ([]byte, error) {
	b, err := u.dl.fetchBytes(ctx, u.assetURL(tag, name), metadataLimit)
	var e *Error
	if errors.As(err, &e) && e.Reason == ReasonDownloadFailed && e.Status == http.StatusNotFound {
		return nil, &Error{Reason: ReasonUnverified, Err: fmt.Errorf("%s is not published", name)}
	}
	return b, err
}

// annotate records the versions on an expected failure, for the message.
func (u *Upgrader) annotate(err error, res Result) error {
	var e *Error
	if errors.As(err, &e) {
		e.Installed, e.Target = res.Installed, res.Target
	}
	return err
}

// removeStaged closes and deletes a staging file. After a successful rename
// the file is already gone, which is not an error.
func removeStaged(f *os.File) {
	_ = f.Close()
	_ = os.Remove(f.Name())
}
