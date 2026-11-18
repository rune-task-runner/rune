package sandbox

import (
	"context"
	"testing"
)

func TestParseMode(t *testing.T) {
	for _, value := range []string{"enforce", "off"} {
		mode, err := ParseMode(value)
		if err != nil || string(mode) != value {
			t.Errorf("ParseMode(%q) = %q, %v", value, mode, err)
		}
	}
	for _, value := range []string{"", "on", "Enforce", "auto", " off"} {
		_, err := ParseMode(value)
		want := `"` + value + `" is not a mode; use enforce or off`
		if err == nil || err.Error() != want {
			t.Errorf("ParseMode(%q) error = %v, want %q", value, err, want)
		}
	}
}

func TestDetect(t *testing.T) {
	if got := Detect("linux", nil).Name(); got != "bubblewrap" {
		t.Errorf("linux backend = %q", got)
	}
	for goos, want := range map[string]string{
		"darwin":  "this system (macos) has no sandbox backend",
		"windows": "this system (windows) has no sandbox backend",
	} {
		b := Detect(goos, nil)
		if b.Name() != "" || b.Path() != "" {
			t.Errorf("%s: name %q, path %q", goos, b.Name(), b.Path())
		}
		if err := b.Probe(context.Background()); err == nil || err.Error() != want {
			t.Errorf("%s: probe = %v, want %q", goos, err, want)
		}
		if _, err := b.Command(context.Background(), Spec{}, []string{"true"}); err == nil {
			t.Errorf("%s: Command returned no error", goos)
		}
	}
}
