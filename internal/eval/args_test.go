package eval

import "testing"

func TestArgsKeyIgnoresMapOrder(t *testing.T) {
	a := Args{Values: map[string]string{}, Lists: map[string][]string{}}
	a.Values["x"] = "1"
	a.Values["y"] = "2"
	a.Values["z"] = "a b"
	a.Lists["z"] = []string{"a", "b"}

	b := Args{Values: map[string]string{}, Lists: map[string][]string{}}
	b.Lists["z"] = []string{"a", "b"}
	b.Values["z"] = "a b"
	b.Values["y"] = "2"
	b.Values["x"] = "1"

	if a.Key() != b.Key() {
		t.Errorf("equal Args gave different keys:\n%s\n%s", a.Key(), b.Key())
	}
}

func TestArgsKeyDistinguishes(t *testing.T) {
	tests := []struct {
		name string
		a, b Args
	}{
		{
			name: "element boundaries",
			a:    Args{Values: map[string]string{"p": "a b"}, Lists: map[string][]string{"p": {"a b"}}},
			b:    Args{Values: map[string]string{"p": "a b"}, Lists: map[string][]string{"p": {"a", "b"}}},
		},
		{
			name: "empty list versus no list",
			a:    Args{Values: map[string]string{"p": ""}, Lists: map[string][]string{"p": {}}},
			b:    Args{Values: map[string]string{"p": ""}},
		},
		{
			name: "separator inside a value",
			a:    Args{Values: map[string]string{"a": "1;b=2"}},
			b:    Args{Values: map[string]string{"a": "1", "b": "2"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.a.Key() == tt.b.Key() {
				t.Errorf("different Args share the key %s", tt.a.Key())
			}
		})
	}
}
