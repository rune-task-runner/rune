package ci

import (
	"regexp"
	"strings"
	"testing"
)

func TestKey(t *testing.T) {
	base := KeyInput{Fingerprint: "f", Node: "build()", Version: "0.11.0", GOOS: "linux", GOARCH: "amd64", Image: "ubuntu24"}
	key := Key("build", base)
	if !regexp.MustCompile(`^rune-build-[0-9a-f]{64}$`).MatchString(key) {
		t.Errorf("key = %q", key)
	}
	if Key("build", base) != key {
		t.Error("the same input gave two keys")
	}
	for name, change := range map[string]func(*KeyInput){
		"fingerprint": func(k *KeyInput) { k.Fingerprint = "g" },
		"node":        func(k *KeyInput) { k.Node = "build(x)" },
		"version":     func(k *KeyInput) { k.Version = "0.12.0" },
		"goos":        func(k *KeyInput) { k.GOOS = "darwin" },
		"goarch":      func(k *KeyInput) { k.GOARCH = "arm64" },
		"image":       func(k *KeyInput) { k.Image = "ubuntu22" },
	} {
		in := base
		change(&in)
		if Key("build", in) == key {
			t.Errorf("changing the %s did not change the key", name)
		}
	}
	long := Key(strings.Repeat("a", 300), base)
	if len(long) > 512 {
		t.Errorf("key of %d characters is over GitHub's 512", len(long))
	}
}
