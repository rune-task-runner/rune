package observe

import (
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"

	"github.com/rune-task-runner/rune/internal/mask"
)

// testSecrets declares DB_URL a secret and exempts PUBLIC_TOKEN, as a
// Runefile would with set secrets and set unmasked, and knows the value of
// DEPLOY_SECRET from the environment.
func testSecrets() Secrets {
	declared, exempt := []string{"DB_URL"}, []string{"PUBLIC_TOKEN"}
	return Secrets{
		Names:  func(name string) bool { return mask.SecretName(name, declared, exempt) },
		Values: mask.NewSet([]string{"DEPLOY_SECRET=s3cr3tvalue"}, declared, exempt),
	}
}

func TestRedact(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"prefix assignment", "API_TOKEN=abc123xyz deploy", "API_TOKEN=*** deploy"},
		{"quoted prefix assignment", `API_TOKEN="a b c" deploy`, "API_TOKEN=*** deploy"},
		{"two assignments", "A=1 API_TOKEN=abc123xyz deploy", "A=1 API_TOKEN=*** deploy"},
		{"export", "export API_TOKEN=abc123xyz", "export API_TOKEN=***"},
		{"declare", "declare PASSWORD=hunter22", "declare PASSWORD=***"},
		{"local", "local SECRET_KEY=k9k9k9", "local SECRET_KEY=***"},
		{"readonly", "readonly PASSWORD=hunter22", "readonly PASSWORD=***"},
		{"typeset", "typeset API_TOKEN=abc123xyz", "typeset API_TOKEN=***"},
		{"array", "API_TOKEN=(a b)", "API_TOKEN=***"},
		{"declared name", "DB_URL=x1y2z3 migrate", "DB_URL=*** migrate"},
		{"exempt name", "PUBLIC_TOKEN=abcd run", "PUBLIC_TOKEN=abcd run"},
		{"built-in exemption", "GIT_AUTHOR_NAME=me git commit", "GIT_AUTHOR_NAME=me git commit"},
		{"empty value", "API_TOKEN= deploy", "API_TOKEN= deploy"},
		{"flag and next word", "deploy --token tok_live_9", "deploy --token ***"},
		{"flag with equals", "deploy --password=hunter22", "deploy --password=***"},
		{"flag with quoted value", `deploy --token="a b"`, "deploy --token=***"},
		{"single-dash flag", "deploy -password hunter22", "deploy -password ***"},
		{"dashed flag name", "deploy --api-key=k1k1", "deploy --api-key=***"},
		{"author is redacted too", "git commit --author me", "git commit --author ***"},
		{"secret flag at the end", "deploy --token", "deploy --token"},
		{"short flag", "mysql -p hunter22", "mysql -p hunter22"},
		{"end of options", "rm -- x", "rm -- x"},
		{"flag name continued by a quote", `deploy --to"ken" x`, `deploy --to"ken" x`},
		{"authorization header", `curl -H "Authorization: Bearer zzz111" https://x`, `curl -H "Authorization: ***" https://x`},
		{"proxy authorization in single quotes", `curl -H 'Proxy-Authorization: Basic abc' x`, `curl -H 'Proxy-Authorization: ***' x`},
		{"secret-named header", `curl -H "X-Api-Token: abc" x`, `curl -H "X-Api-Token: ***" x`},
		{"header flag with equals", "curl --header=Authorization:abc x", "curl --header=Authorization:*** x"},
		{"header flag with quoted value", `curl --header="Authorization: abc" x`, `curl --header="Authorization: ***" x`},
		{"attached header", `curl -H"Authorization: Bearer zzz111" x`, `curl -H"Authorization: ***" x`},
		{"attached header unquoted", `curl -HAuthorization:zzz111 x`, `curl -HAuthorization:*** x`},
		{"quoted header flag with equals", `curl "--header=Authorization: Bearer zzz111" x`, `curl "--header=Authorization: ***" x`},
		{"quoted attached header", `curl '-HAuthorization: Bearer zzz111' x`, `curl '-HAuthorization: ***' x`},
		{"plain header", `curl -H "Accept: text/plain" x`, `curl -H "Accept: text/plain" x`},
		{"url password", "psql postgres://app:pw0rd99@db/app", "psql postgres://app:***@db/app"},
		{"url without password", "curl https://example.com/path", "curl https://example.com/path"},
		{"url token as user", "git clone https://ghp_abc123@github.com/o/r", "git clone https://***@github.com/o/r"},
		{"ssh url user kept", "git clone ssh://git@github.com/o/r", "git clone ssh://git@github.com/o/r"},
		{"env argument", "env API_TOKEN=abc123xyz ./deploy", "env API_TOKEN=*** ./deploy"},
		{"docker -e argument", `docker run -e API_TOKEN="a b" img`, "docker run -e API_TOKEN=*** img"},
		{"argument after flag equals", "docker build --build-arg=API_TOKEN=abc123xyz .", "docker build --build-arg=API_TOKEN=*** ."},
		{"quoted docker -e argument", `docker run -e "API_TOKEN=abc123xyz" img`, `docker run -e "API_TOKEN=***" img`},
		{"single-quoted env argument", `env 'API_TOKEN=abc123xyz' ./deploy`, `env 'API_TOKEN=***' ./deploy`},
		{"quoted argument with an expansion", `env "API_TOKEN=abc$X" ./deploy`, `env "API_TOKEN=***" ./deploy`},
		{"quoted argument data", `curl -d "password=hunter22" x`, `curl -d "password=***" x`},
		{"quoted flag with equals", `deploy "--token=abc123xyz"`, `deploy "--token=***"`},
		{"quoted flag and next word", `deploy '--token' abc123xyz`, `deploy '--token' ***`},
		{"quoted plain argument kept", `make "GOOS=linux" build`, `make "GOOS=linux" build`},
		{"plain argument kept", "make GOOS=linux build", "make GOOS=linux build"},
		{"exempt argument kept", "env PUBLIC_TOKEN=abcd run", "env PUBLIC_TOKEN=abcd run"},
		{"known value anywhere", `echo "x s3cr3tvalue y"`, `echo "x *** y"`},
		{"spacing kept", "echo  \"a  b\"   'c'", "echo  \"a  b\"   'c'"},
		{"nothing to remove", "go test ./...", "go test ./..."},
		{"in a substitution", "echo $(API_TOKEN=abc123xyz x)", "echo $(API_TOKEN=*** x)"},
		{"multi-line", "go test \\\n  ./...", "go test \\\n  ./..."},
		{"tab", "echo\ta", "echo\ta"},
		{"curl user", "curl -u admin:hunter22 https://x", "curl -u admin:*** https://x"},
		{"curl quoted user", `curl --user "admin:hunter22" https://x`, `curl --user "admin:***" https://x`},
		{"curl user with equals", "curl --user=admin:hunter22 https://x", "curl --user=admin:*** https://x"},
		{"curl proxy user", "curl -U p:hunter22 https://x", "curl -U p:*** https://x"},
		{"curl user without a password", "curl -u admin https://x", "curl -u admin https://x"},
		{"curl user with an empty password", "curl -u admin: https://x", "curl -u admin: https://x"},
		{"an empty secret argument", "env API_TOKEN= ./deploy", "env API_TOKEN= ./deploy"},
		{"curl attached user", "curl -uadmin:hunter22 https://x", "curl -uadmin:*** https://x"},
		{"curl attached quoted user", "curl -u'admin:hunter22' https://x", "curl -u'admin:***' https://x"},
		{"curl attached user without a password", "curl -uadmin https://x", "curl -uadmin https://x"},
		{"mysql attached password", "mysql -uroot -phunter22 db", "mysql -uroot -p*** db"},
		{"mysql through a path", "/usr/bin/mysqldump -phunter22 db", "/usr/bin/mysqldump -p*** db"},
		{"mysql asks", "mysql -p db", "mysql -p db"},
		{"sshpass", "sshpass -p hunter22 ssh host", "sshpass -p *** ssh host"},
		{"sshpass attached", "sshpass -phunter22 ssh host", "sshpass -p*** ssh host"},
		{"docker login", "docker login -u me -p hunter22 reg", "docker login -u me -p *** reg"},
		{"docker run port", "docker run -p 8080:80 img", "docker run -p 8080:80 img"},
		{"mkdir parents", "mkdir -p a/b", "mkdir -p a/b"},
		{"go test parallel", "go test -p 4 ./...", "go test -p 4 ./..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Redact(tt.in, testSecrets())
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("Redact(%q)\n got  %q\n want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestRedactOmits(t *testing.T) {
	tests := []struct{ name, in, reason string }{
		{"here-document", "cat <<EOF > .env\nTOKEN=x\nEOF", ReasonHereDocument},
		{"dash here-document", "cat <<-EOF\n\tx\n\tEOF", ReasonHereDocument},
		{"unparsable", "echo 'unterminated", ReasonUnparsable},
		{"too long", "echo " + strings.Repeat("a", MaxCommandBytes-4), ReasonTooLong},
		{"NUL", "echo a\x00b", ReasonControlCharacter},
		{"escape", "echo a\x1bb", ReasonControlCharacter},
		{"carriage return", "echo a\r", ReasonControlCharacter},
		{"C1 control", "echo a\u0085b", ReasonControlCharacter},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := Redact(tt.in, testSecrets()); dropReason(err) != tt.reason {
				t.Errorf("Redact = %q, %v; want the reason %s", got, err, tt.reason)
			}
		})
	}
	// The limit itself is accepted, and a here-string is not a here-document.
	for _, in := range []string{"echo " + strings.Repeat("a", MaxCommandBytes-5), "cat <<< word"} {
		if _, err := Redact(in, testSecrets()); err != nil {
			t.Errorf("Redact(%.20q...): %v", in, err)
		}
	}
}

func TestRedactWithoutSecrets(t *testing.T) {
	if got, err := Redact("API_TOKEN=abc123xyz x", Secrets{}); err != nil || got != "API_TOKEN=abc123xyz x" {
		t.Errorf("no secret names known: %q %v", got, err)
	}
}

func TestWordText(t *testing.T) {
	tests := []struct {
		word, text, value string
		whole, ok         bool
	}{
		{`A=x`, "A=x", "A=x", true, true},
		{`A="x y"`, "A=", `A="x y"`, false, true},
		{`'A=x'`, "A=x", "A=x", true, true},
		{`"A=x"`, "A=x", "A=x", true, true},
		{`"A=x$Y"`, "A=x", "A=x$Y", false, true},
		{`"A=x"y`, "", "", false, false},
		{`$'A=x'`, "", "", false, false},
		{`$A`, "", "", false, false},
	}
	for _, tt := range tests {
		cmd := "cmd " + tt.word
		f, err := syntax.NewParser().Parse(strings.NewReader(cmd), "")
		if err != nil {
			t.Fatal(err)
		}
		w := f.Stmts[0].Cmd.(*syntax.CallExpr).Args[1]
		text, at, end, whole, ok := wordText(w)
		if ok != tt.ok || text != tt.text || whole != tt.whole || (ok && cmd[at:end] != tt.value) {
			t.Errorf("wordText(%s) = %q %q %v %v, want %q %q %v %v", tt.word, text, cmd[at:end], whole, ok, tt.text, tt.value, tt.whole, tt.ok)
		}
	}
}

func TestSplice(t *testing.T) {
	tests := []struct {
		text  string
		spans []span
		want  string
	}{
		{"abcdef", nil, "abcdef"},
		{"abcdef", []span{{1, 3}}, "a***def"},
		{"abcdef", []span{{4, 6}, {0, 1}}, "***bcd***"},
		{"abcdef", []span{{1, 4}, {2, 5}}, "a***f"},
		{"abcdef", []span{{1, 5}, {2, 3}}, "a***f"},
	}
	for _, tt := range tests {
		if got := splice(tt.text, tt.spans); got != tt.want {
			t.Errorf("splice(%q, %v) = %q, want %q", tt.text, tt.spans, got, tt.want)
		}
	}
}

func TestCleanDescription(t *testing.T) {
	s := testSecrets()
	tests := []struct{ name, in, want string }{
		{"plain", "Run test suite", "Run test suite"},
		{"secret value", "Deploy with s3cr3tvalue", "Deploy with ***"},
		{"first line", "Run tests\nthen more", "Run tests"},
		{"control characters", "Run\ttests\x01now", "Run tests now"},
		{"spaces", "  Run  ", "Run"},
		{"cut", strings.Repeat("a", 250), strings.Repeat("a", MaxDescriptionBytes)},
		{"cut at a character", strings.Repeat("a", MaxDescriptionBytes-1) + "é", strings.Repeat("a", MaxDescriptionBytes-1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CleanDescription(tt.in, s); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
	if got := CleanDescription("x s3cr3tvalue", Secrets{}); got != "x s3cr3tvalue" {
		t.Errorf("no values known: %q", got)
	}
}
