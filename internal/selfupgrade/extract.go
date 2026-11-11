package selfupgrade

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
)

// extractBinary copies the one rune binary in the archive to dst. Entry names
// are only ever compared, never used to build a path, so an archive cannot
// place a file anywhere. Other entries (licence, readme) are skipped.
//
// archive is the open file the checksum was computed over, so what is
// extracted is what was verified, not a second look-up by name.
func extractBinary(archive *os.File, goos string, dst io.Writer, limit int64) error {
	info, err := archive.Stat()
	if err != nil {
		return fmt.Errorf("cannot read back the downloaded archive: %w", err)
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("cannot read back the downloaded archive: %w", err)
	}
	if goos == "windows" {
		return extractZip(archive, info.Size(), "rune.exe", dst, limit)
	}
	return extractTarGz(archive, "rune", dst, limit)
}

func badArchive(err error) error { return &Error{Reason: ReasonBadArchive, Err: err} }

func extractTarGz(archive io.Reader, want string, dst io.Writer, limit int64) error {
	gz, err := gzip.NewReader(archive)
	if err != nil {
		return badArchive(err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	found := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return badArchive(err)
		}
		if hdr.Name != want {
			continue
		}
		if found || hdr.Typeflag != tar.TypeReg {
			return badArchive(errors.New("unexpected rune entry"))
		}
		found = true
		if err := copyLimited(dst, tr, limit); err != nil {
			return err
		}
	}
	if !found {
		return badArchive(errors.New("no rune entry"))
	}
	return nil
}

func extractZip(archive io.ReaderAt, size int64, want string, dst io.Writer, limit int64) error {
	zr, err := zip.NewReader(archive, size)
	if err != nil {
		return badArchive(err)
	}

	var entry *zip.File
	for _, f := range zr.File {
		if f.Name != want {
			continue
		}
		if entry != nil || !f.Mode().IsRegular() {
			return badArchive(errors.New("unexpected rune entry"))
		}
		entry = f
	}
	if entry == nil {
		return badArchive(errors.New("no rune entry"))
	}
	rc, err := entry.Open()
	if err != nil {
		return badArchive(err)
	}
	defer func() { _ = rc.Close() }()
	return copyLimited(dst, rc, limit)
}

// copyLimited copies at most limit bytes. The declared size of an entry is
// not trusted, so the limit is enforced on what is actually read.
func copyLimited(dst io.Writer, src io.Reader, limit int64) error {
	w := &failingWriter{w: dst}
	n, err := io.Copy(w, io.LimitReader(src, limit))
	if w.err != nil {
		// The destination failed (a full disk, say); the archive is fine.
		return &Error{Reason: ReasonNotWritable, Err: w.err}
	}
	if err != nil {
		return badArchive(err)
	}
	if n < limit {
		return nil
	}
	var probe [1]byte
	if m, _ := src.Read(probe[:]); m > 0 {
		return &Error{Reason: ReasonTooLarge, Limit: formatSize(limit)}
	}
	return nil
}

// failingWriter remembers the first error of the writer it wraps, so a copy
// can tell a bad source from a bad destination.
type failingWriter struct {
	w   io.Writer
	err error
}

func (f *failingWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if err != nil && f.err == nil {
		f.err = err
	}
	return n, err
}
