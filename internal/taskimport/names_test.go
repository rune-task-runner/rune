package taskimport

import (
	"slices"
	"testing"
)

func TestMapNames(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"valid names are kept", []string{"build", "_helper", "a-b_c9"}, []string{"build", "_helper", "a-b_c9"}},
		{"invalid runs become one dash", []string{"test:unit", "docker::build", "a b.c"}, []string{"test-unit", "docker-build", "a-b-c"}},
		{"reserved words get a prefix", []string{"import", "set", "mod", "if", "else"}, []string{"task-import", "task-set", "task-mod", "task-if", "task-else"}},
		{"a leading digit gets a prefix", []string{"1x"}, []string{"task-1x"}},
		{"a leading dash is removed", []string{"-x-", ":x:"}, []string{"x", "x-2"}},
		{"only invalid characters", []string{":::", "..."}, []string{"task", "task-2"}},
		{"collisions are numbered in source order", []string{"a:b", "a.b", "a/b"}, []string{"a-b", "a-b-2", "a-b-3"}},
		{"a kept name is never taken", []string{"test:unit", "test-unit"}, []string{"test-unit-2", "test-unit"}},
		{"a numbered name skips a kept one", []string{"a:b", "a.b", "a-b-2", "a-b"}, []string{"a-b-3", "a-b-4", "a-b-2", "a-b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mapNames(tt.in); !slices.Equal(got, tt.want) {
				t.Errorf("mapNames(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestValidName(t *testing.T) {
	for name, want := range map[string]bool{
		"build": true, "_x": true, "a-b": true, "A9_": true,
		"": false, "9a": false, "-a": false, "a:b": false, "a b": false, "import": false, "else": false,
	} {
		if got := validName(name); got != want {
			t.Errorf("validName(%q) = %v, want %v", name, got, want)
		}
	}
}
