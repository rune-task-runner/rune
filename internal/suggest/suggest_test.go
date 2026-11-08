package suggest

import "testing"

// Nearest powers every did-you-mean suggestion: the "unknown task" error, the
// --allow-destructive flag, and unknown tool arguments.

func TestNearest(t *testing.T) {
	cands := []string{"build", "test", "serve", "version", "completion"}
	cases := []struct {
		token, want string
		ok          bool
	}{
		{"serv", "serve", true},      // 1 deletion
		{"tset", "test", true},       // transposition (distance 2)
		{"biuld", "build", true},     // transposition (distance 2)
		{"versoin", "version", true}, //nolint:misspell // intentional typo: tests the did-you-mean suggestion
		{"tesxyz", "", false},        // distance 3 from test: just past the limit
		{"zzzzzzzzzz", "", false},    // nothing close
	}
	for _, c := range cases {
		got, ok := Nearest(c.token, cands)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("Nearest(%q) = (%q,%v); want (%q,%v)", c.token, got, ok, c.want, c.ok)
		}
	}
}

func TestNearestTieGoesToFirstCandidate(t *testing.T) {
	for _, c := range []struct {
		cands []string
		want  string
	}{
		{[]string{"tag", "tab"}, "tag"},
		{[]string{"tab", "tag"}, "tab"},
	} {
		if got, ok := Nearest("tad", c.cands); !ok || got != c.want {
			t.Errorf("Nearest(tad, %v) = (%q,%v); want (%q,true)", c.cands, got, ok, c.want)
		}
	}
}

func TestNearestNoCandidates(t *testing.T) {
	if got, ok := Nearest("x", nil); ok || got != "" {
		t.Errorf("Nearest(x, nil) = (%q,%v); want (\"\",false)", got, ok)
	}
}
