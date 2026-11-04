package eval

import (
	"strings"
	"testing"
)

func listScope(elems []string) *Scope {
	s := newTestScope()
	return s.WithArgs(Args{
		Values: map[string]string{"names": strings.Join(elems, " "), "name": "a b"},
		Lists:  map[string][]string{"names": elems},
	})
}

func TestQuoteEachList(t *testing.T) {
	tests := []struct {
		name  string
		elems []string
		want  string
	}{
		{"injection attempt", []string{"yq", "x; echo pwned", "tofu"}, `'yq' 'x; echo pwned' 'tofu'`},
		{"spaces, empty, quote", []string{"a b", "", "it's"}, `'a b' '' 'it'\''s'`},
		{"empty list", []string{}, ``},
		{"newline", []string{"a\nb"}, "'a\nb'"},
		{"command substitution", []string{"$(id)"}, `'$(id)'`},
		{"backtick", []string{"`id`"}, "'`id`'"},
		{"semicolon", []string{";"}, `';'`},
		{"leading dash", []string{"-rf", "/"}, `'-rf' '/'`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := evalFragment(t, `quote_each(names)`, listScope(tt.elems))
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("quote_each(names) = %q, want %q", got, tt.want)
			}
		})
	}
}

// Anything but a bare reference to a variadic is quote() of its value.
func TestQuoteEachFallsBackToQuote(t *testing.T) {
	scope := listScope([]string{"yq", "x; echo pwned", "tofu"})
	tests := []struct {
		src  string
		want string
	}{
		{`quote_each(name)`, `'a b'`},
		{`quote_each("a b")`, `'a b'`},
		{`quote_each(names + "")`, `'yq x; echo pwned tofu'`},
	}
	for _, tt := range tests {
		got, err := evalFragment(t, tt.src, scope)
		if err != nil {
			t.Fatalf("%s: %v", tt.src, err)
		}
		if got != tt.want {
			t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
		}
		quoted, _ := evalFragment(t, strings.Replace(tt.src, "quote_each", "quote", 1), scope)
		if got != quoted {
			t.Errorf("%s = %q, but quote() gives %q", tt.src, got, quoted)
		}
	}

	noLists := newTestScope().WithArgs(Args{Values: map[string]string{"names": "a b"}})
	got, err := evalFragment(t, `quote_each(names)`, noLists)
	if err != nil || got != `'a b'` {
		t.Errorf("quote_each of a name absent from Lists = %q, %v; want quote()'s 'a b'", got, err)
	}
}

func TestQuoteEachArity(t *testing.T) {
	scope := listScope([]string{"a"})
	for _, tt := range []struct{ src, want string }{
		{`quote_each()`, "quote_each() expects 1 argument, got 0"},
		{`quote_each(names, names)`, "quote_each() expects 1 argument, got 2"},
	} {
		_, err := evalFragment(t, tt.src, scope)
		if err == nil || err.Msg != tt.want {
			t.Errorf("%s: error = %v, want %q", tt.src, err, tt.want)
		}
		_, qerr := evalFragment(t, strings.Replace(tt.src, "quote_each", "quote", 1), scope)
		if qerr == nil || strings.Replace(qerr.Msg, "quote()", "quote_each()", 1) != tt.want {
			t.Errorf("%s: quote() error %v does not match", tt.src, qerr)
		}
	}
}
