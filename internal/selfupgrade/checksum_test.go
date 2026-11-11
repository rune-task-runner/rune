package selfupgrade

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestArchiveName(t *testing.T) {
	real, err := os.ReadFile("testdata/v0.7.0/checksums.txt")
	if err != nil {
		t.Fatal(err)
	}
	listed, err := parseChecksums(real)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct{ goos, goarch, want string }{
		{"linux", "amd64", "rune_0.8.0_linux_amd64.tar.gz"},
		{"linux", "arm64", "rune_0.8.0_linux_arm64.tar.gz"},
		{"darwin", "amd64", "rune_0.8.0_darwin_amd64.tar.gz"},
		{"darwin", "arm64", "rune_0.8.0_darwin_arm64.tar.gz"},
		{"windows", "amd64", "rune_0.8.0_windows_amd64.zip"},
		{"windows", "arm64", "rune_0.8.0_windows_arm64.zip"},
	}
	for _, tt := range tests {
		if got := archiveName("0.8.0", tt.goos, tt.goarch); got != tt.want {
			t.Errorf("archiveName(0.8.0, %s, %s) = %q, want %q", tt.goos, tt.goarch, got, tt.want)
		}
		if _, ok := listed[archiveName("0.7.0", tt.goos, tt.goarch)]; !ok {
			t.Errorf("real v0.7.0 list has no entry for %s/%s", tt.goos, tt.goarch)
		}
	}
}

func TestParseChecksums(t *testing.T) {
	real, err := os.ReadFile("testdata/v0.7.0/checksums.txt")
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseChecksums(real)
	if err != nil || len(got) != 12 {
		t.Fatalf("real list: %d entries, err %v; want 12, nil", len(got), err)
	}

	hash := strings.Repeat("ab", 32)
	ok := map[string]string{
		"blank lines":      "\n" + hash + "  a.tar.gz\n\n",
		"no final newline": hash + "  a.tar.gz",
		"CRLF":             hash + "  a.tar.gz\r\n",
	}
	for name, in := range ok {
		got, err := parseChecksums([]byte(in))
		if err != nil || len(got) != 1 || got["a.tar.gz"][0] != 0xab {
			t.Errorf("%s: got %v, err %v", name, got, err)
		}
	}

	bad := map[string]string{
		"short hash":     strings.Repeat("ab", 31) + "  a.tar.gz\n",
		"uppercase hash": strings.Repeat("AB", 32) + "  a.tar.gz\n",
		"non-hex hash":   strings.Repeat("zz", 32) + "  a.tar.gz\n",
		"one space":      hash + " a.tar.gz\n",
		"no name":        hash + "  \n",
		"duplicate name": hash + "  a.tar.gz\n" + hash + "  a.tar.gz\n",
		"hash only":      hash + "\n",
	}
	for name, in := range bad {
		_, err := parseChecksums([]byte(in))
		var e *Error
		if !errors.As(err, &e) || e.Reason != ReasonUnverified {
			t.Errorf("%s: err = %v, want an unverified *Error", name, err)
		}
	}
}
