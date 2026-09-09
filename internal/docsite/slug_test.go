package docsite

import "testing"

func TestSlug(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"overview.md", "overview"},
		{"getting-started.md", "getting-started"},
		{"GRAMMAR.md", "grammar"},
		{"README.md", ""},
		{"how-to/README.md", "how-to"},
		{"how-to/caching.md", "how-to/caching"},
		{"use-cases/python-project.md", "use-cases/python-project"},
		{"examples/go-service/README.md", "examples/go-service"},
		{"./overview.md", "overview"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := Slug(tt.in); got != tt.want {
				t.Errorf("Slug(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
