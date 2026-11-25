package mask

import (
	"strings"
	"testing"
)

func TestNewSet_BuiltinPatterns(t *testing.T) {
	// Every built-in pattern, exercised with mixed-case names (matching is
	// case-insensitive over the NAME, contract §2.1).
	cases := []struct {
		name string
		env  string
	}{
		{"TOKEN", "API_TOKEN=value-one"},
		{"TOKEN lowercase", "api_token=value-one"},
		{"TOKEN camel", "GithubToken=value-one"},
		{"SECRET", "MY_SECRET=value-one"},
		{"PASSWORD", "DB_PASSWORD=value-one"},
		{"PASSWD", "PGPASSWD=value-one"},
		{"APIKEY", "APIKEY=value-one"},
		{"API_KEY", "STRIPE_API_KEY=value-one"},
		{"PRIVATE_KEY", "SSH_PRIVATE_KEY=value-one"},
		{"ACCESS_KEY", "AWS_ACCESS_KEY=value-one"},
		{"CREDENTIAL", "GOOGLE_CREDENTIALS=value-one"},
		{"AUTH", "OAUTH_METHOD=value-one"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSet([]string{tc.env}, nil, nil)
			if s.Empty() {
				t.Fatalf("NewSet(%q) is empty; want value tracked", tc.env)
			}
			if got := s.MaskString("x value-one y"); got != "x "+Placeholder+" y" {
				t.Errorf("MaskString = %q, want value masked", got)
			}
		})
	}
}

func TestNewSet_InnocentNamesIgnored(t *testing.T) {
	s := NewSet([]string{"GREETING=hello-there", "PATH=/usr/bin"}, nil, nil)
	if !s.Empty() {
		t.Fatalf("innocent names produced a non-empty set")
	}
	if got := s.MaskString("hello-there"); got != "hello-there" {
		t.Errorf("MaskString altered text with an empty set: %q", got)
	}
}

func TestNewSet_DeclaredNames(t *testing.T) {
	s := NewSet([]string{"DEPLOY_CFG=s3-abc-def"}, []string{"DEPLOY_CFG"}, nil)
	if got := s.MaskString("cfg is s3-abc-def"); got != "cfg is "+Placeholder {
		t.Errorf("declared name not masked: %q", got)
	}
}

func TestNewSet_ExemptBeatsPattern(t *testing.T) {
	s := NewSet([]string{"OAUTH_METHOD=oauth2-pkce"}, nil, []string{"OAUTH_METHOD"})
	if !s.Empty() {
		t.Fatalf("exempted pattern-matched name still tracked")
	}
}

func TestNewSet_ExemptDoesNotBeatDeclaration(t *testing.T) {
	// Contradictory config is rejected upstream (config.ResolveSettings); the
	// Set itself fails safe: an explicit declaration always wins.
	s := NewSet([]string{"MY_VALUE=abcd-efgh"}, []string{"MY_VALUE"}, []string{"MY_VALUE"})
	if got := s.MaskString("abcd-efgh"); got != Placeholder {
		t.Errorf("declaration did not win over exemption: %q", got)
	}
}

func TestNewSet_DeclaredAndExemptNamesAreCaseInsensitive(t *testing.T) {
	// The pattern rule is case-insensitive; declared/exempt lookups match it.
	s := NewSet([]string{"DEPLOY_CFG=abcd-999"}, []string{"deploy_cfg"}, nil)
	if got := s.MaskString("abcd-999"); got != Placeholder {
		t.Errorf("lowercase declaration did not mask DEPLOY_CFG: %q", got)
	}
	s = NewSet([]string{"GithubToken=gh-value-123"}, nil, []string{"GITHUBTOKEN"})
	if !s.Empty() {
		t.Errorf("differently-cased exemption did not exempt GithubToken")
	}
}

func TestNewSet_BuiltinUnmaskedNames(t *testing.T) {
	// Ubiquitous non-secret names the AUTH pattern would otherwise capture.
	s := NewSet([]string{
		"SSH_AUTH_SOCK=/tmp/agent.1234",
		"GIT_AUTHOR_NAME=Ada Lovelace",
		"GIT_AUTHOR_EMAIL=ada@example.test",
	}, nil, nil)
	if !s.Empty() {
		t.Fatalf("built-in unmasked names were tracked")
	}
	// An explicit declaration still re-includes one of them.
	s = NewSet([]string{"GIT_AUTHOR_NAME=Ada Lovelace"}, []string{"GIT_AUTHOR_NAME"}, nil)
	if got := s.MaskString("Ada Lovelace"); got != Placeholder {
		t.Errorf("declaration did not override the built-in exemption: %q", got)
	}
}

func TestNewSet_MinLen(t *testing.T) {
	s := NewSet([]string{"SHORT_TOKEN=abc", "LONG_TOKEN=abcd"}, nil, nil)
	if got := s.MaskString("abc"); got != "abc" {
		t.Errorf("value below MinLen was masked: %q", got)
	}
	if got := s.MaskString("abcd"); got != Placeholder {
		t.Errorf("value at MinLen not masked: %q", got)
	}
}

func TestNewSet_MultiLinePerLineOnly(t *testing.T) {
	// Each qualifying line is an independent entry; the whole value is NOT one
	// entry (data-model derivation step 3) — so text between masked lines
	// survives, and short lines (< MinLen) are not tracked.
	s := NewSet([]string{"PEM_PRIVATE_KEY=line-one\nzz\nline-three"}, nil, nil)
	if got := s.MaskString("line-one\nzz\nline-three"); got != Placeholder+"\nzz\n"+Placeholder {
		t.Errorf("multi-line masking = %q, want per-line entries only", got)
	}
	if got := s.MaskString("has line-three inside"); got != "has "+Placeholder+" inside" {
		t.Errorf("individual line not masked on its own: %q", got)
	}
}

func TestNewSet_CRLFLines(t *testing.T) {
	s := NewSet([]string{"WIN_TOKEN=first-line\r\nsecond-line"}, nil, nil)
	if got := s.MaskString("a first-line b second-line c"); got != "a "+Placeholder+" b "+Placeholder+" c" {
		t.Errorf("CRLF-separated lines not masked: %q", got)
	}
}

func TestNewSet_EmptyAndMalformedPairs(t *testing.T) {
	s := NewSet([]string{"EMPTY_TOKEN=", "no-equals-sign", "=weird"}, nil, nil)
	if !s.Empty() {
		t.Fatalf("empty/malformed pairs produced entries")
	}
}

func TestNewSet_DeclaredNameAbsentFromEnvIsInert(t *testing.T) {
	s := NewSet([]string{"GREETING=hello-there"}, []string{"NOT_PRESENT"}, nil)
	if !s.Empty() {
		t.Fatalf("declaration for an absent variable produced entries")
	}
}

func TestNewSet_DuplicateValues(t *testing.T) {
	s := NewSet([]string{"A_TOKEN=same-value", "B_SECRET=same-value"}, nil, nil)
	if got := s.MaskString("same-value and same-value"); got != Placeholder+" and "+Placeholder {
		t.Errorf("deduped value not masked everywhere: %q", got)
	}
}

func TestMaskString_LeftmostLongestOverlap(t *testing.T) {
	// One secret is a prefix of another: no fragment of either may survive.
	s := NewSet([]string{"A_TOKEN=hunter2-xyz", "B_TOKEN=hunter2-xyz-extended"}, nil, nil)
	if got := s.MaskString("x hunter2-xyz-extended y"); got != "x "+Placeholder+" y" {
		t.Errorf("longest match not preferred: %q", got)
	}
	if got := s.MaskString("x hunter2-xyz y"); got != "x "+Placeholder+" y" {
		t.Errorf("shorter secret not masked alone: %q", got)
	}
}

func TestMaskString_ManyOccurrences(t *testing.T) {
	s := NewSet([]string{"API_TOKEN=sekrit-value"}, nil, nil)
	in := strings.Repeat("sekrit-value ", 3) + "end"
	want := strings.Repeat(Placeholder+" ", 3) + "end"
	if got := s.MaskString(in); got != want {
		t.Errorf("MaskString = %q, want %q", got, want)
	}
}

func TestSensitiveName(t *testing.T) {
	for _, name := range []string{
		"token", "API_TOKEN", "password", "passwd", "apiKey", "api_key",
		"private_key", "access_key", "credential", "authorization", "author",
	} {
		if !SensitiveName(name) {
			t.Errorf("SensitiveName(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"env", "target", "name", ""} {
		if SensitiveName(name) {
			t.Errorf("SensitiveName(%q) = true, want false", name)
		}
	}
}

// SecretName is the name rule of NewSet on its own, for names that are read
// from text rather than from the environment (spec 058).
func TestSecretName(t *testing.T) {
	declared, exempt := []string{"db_url"}, []string{"PUBLIC_TOKEN"}
	tests := []struct {
		name string
		want bool
	}{
		{"API_TOKEN", true},
		{"api_token", true},
		{"DB_URL", true},
		{"public_token", false},
		{"GIT_AUTHOR_NAME", false},
		{"SSH_AUTH_SOCK", false},
		{"HOME", false},
	}
	for _, tt := range tests {
		if got := SecretName(tt.name, declared, exempt); got != tt.want {
			t.Errorf("SecretName(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
	// NewSet keeps the value of exactly the names SecretName accepts.
	for _, tt := range tests {
		s := NewSet([]string{tt.name + "=value-of-" + tt.name}, declared, exempt)
		if got := !s.Empty(); got != tt.want {
			t.Errorf("NewSet tracks %s: %v, SecretName says %v", tt.name, got, tt.want)
		}
	}
}
