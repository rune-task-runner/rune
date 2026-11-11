package selfupgrade

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// archiveName is the published archive for one platform, as the release
// pipeline names it.
func archiveName(version, goos, goarch string) string {
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("rune_%s_%s_%s.%s", version, goos, goarch, ext)
}

// parseChecksums reads a checksum list: one "<64 lowercase hex>  <name>" per
// line. A list that does not have exactly that shape is not trusted at all.
func parseChecksums(b []byte) (map[string][32]byte, error) {
	sums := map[string][32]byte{}
	for line := range strings.Lines(string(b)) {
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}
		digest, name, ok := strings.Cut(line, "  ")
		if !ok || name == "" || len(digest) != 64 || digest != strings.ToLower(digest) {
			return nil, &Error{Reason: ReasonUnverified, Err: fmt.Errorf("malformed checksum line %q", line)}
		}
		raw, err := hex.DecodeString(digest)
		if err != nil {
			return nil, &Error{Reason: ReasonUnverified, Err: fmt.Errorf("malformed checksum line %q", line)}
		}
		if _, dup := sums[name]; dup {
			return nil, &Error{Reason: ReasonUnverified, Err: fmt.Errorf("checksum list names %s twice", name)}
		}
		sums[name] = [32]byte(raw)
	}
	return sums, nil
}
