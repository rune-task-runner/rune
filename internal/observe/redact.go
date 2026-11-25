package observe

import (
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"mvdan.cc/sh/v3/syntax"

	"github.com/rune-task-runner/rune/internal/mask"
)

const (
	// MaxCommandBytes is the longest command recorded. A longer one is
	// almost always a script written inline, not a task.
	MaxCommandBytes = 4096
	// MaxDescriptionBytes bounds the agent's description of a command.
	MaxDescriptionBytes = 200
)

// Mark stands where a secret was removed, as it does in masked task output.
const Mark = mask.Placeholder

// Secrets is what redaction knows about the project's secrets.
type Secrets struct {
	// Names reports whether a variable, flag or header name is a secret
	// name. Nil knows none.
	Names func(string) bool
	// Values holds the secret values to mask wherever they appear. Nil
	// holds none.
	Values *mask.Set
}

func (s Secrets) secretName(name string) bool {
	return s.Names != nil && s.Names(name)
}

// span is a byte range of a command to replace with Mark.
type span struct{ start, end int }

var (
	// header is the name of an HTTP header at the start of a word.
	header = regexp.MustCompile(`^([A-Za-z0-9_-]+):[ \t]*`)
	// urlPassword is the password of user:password@ in a URL.
	urlPassword = regexp.MustCompile(`://[^/@\s:'"]*:([^/@\s'"]+)@`)
	// urlToken is the user of an HTTP URL that has no password: where a
	// token goes in https://TOKEN@host.
	urlToken = regexp.MustCompile(`(?i)\bhttps?://([^/@\s:'"]+)@`)
	// envName is a variable name, as NAME=value passes one to env, docker -e
	// or make.
	envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	// flagName is the name of a flag. A word such as -HAuthorization:x is a
	// flag with its value attached, not a flag named after a secret.
	flagName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
)

// Redact returns command with every secret it can find replaced by Mark,
// or a *DropError when the command cannot be kept safely: one that does
// not parse, holds a here-document, is longer than MaxCommandBytes or holds
// a control character other than tab and line feed. The command is parsed
// as shell syntax and never run or expanded (research R6).
func Redact(command string, s Secrets) (string, error) {
	if len(command) > MaxCommandBytes {
		return "", &DropError{ReasonTooLong}
	}
	if strings.ContainsFunc(command, isControl) {
		return "", &DropError{ReasonControlCharacter}
	}
	f, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return "", &DropError{ReasonUnparsable}
	}
	var spans []span
	hereDoc := false
	syntax.Walk(f, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.Redirect:
			if n.Op == syntax.Hdoc || n.Op == syntax.DashHdoc {
				hereDoc = true
			}
		case *syntax.CallExpr:
			spans = append(spans, s.assignSpans(n.Assigns)...)
			spans = append(spans, s.flagSpans(n.Args)...)
			spans = append(spans, credentialSpans(command, n.Args)...)
		case *syntax.DeclClause:
			spans = append(spans, s.assignSpans(n.Args)...)
		case *syntax.Word:
			spans = append(spans, s.wordSpans(command, n)...)
		}
		return true
	})
	if hereDoc {
		return "", &DropError{ReasonHereDocument}
	}
	out := splice(command, spans)
	if s.Values != nil {
		out = s.Values.MaskString(out)
	}
	return out, nil
}

func isControl(r rune) bool {
	return r != '\t' && r != '\n' && unicode.IsControl(r)
}

func nodeSpan(n syntax.Node) span {
	return span{int(n.Pos().Offset()), int(n.End().Offset())}
}

// assignSpans finds the values of assignments to secret names, as a
// command's prefix or after export, declare, local, readonly or typeset.
func (s Secrets) assignSpans(assigns []*syntax.Assign) []span {
	var out []span
	for _, a := range assigns {
		if a.Naked || a.Name == nil || !s.secretName(a.Name.Value) {
			continue
		}
		if a.Value != nil && len(a.Value.Parts) > 0 {
			out = append(out, nodeSpan(a.Value))
		}
		if a.Array != nil {
			out = append(out, nodeSpan(a.Array))
		}
	}
	return out
}

// flagSpans finds the values of flags with a secret name: the rest of a
// --flag=value word, or the word after a --flag word. It also finds the value
// of a NAME=value argument with a secret name, alone or after --flag=, as
// env, docker -e, docker --build-arg= or make take one.
func (s Secrets) flagSpans(args []*syntax.Word) []span {
	var out []span
	for i, w := range args {
		text, at, end, whole, ok := wordText(w)
		if !ok {
			continue
		}
		if !strings.HasPrefix(text, "-") {
			out = append(out, s.envArgSpan(text, at, end)...)
			continue
		}
		name, rest, hasValue := strings.Cut(text, "=")
		if !hasValue && !whole {
			// A quoted part continues the flag's name: it cannot be read.
			continue
		}
		flag := strings.TrimLeft(name, "-")
		if !flagName.MatchString(flag) || !s.secretName(strings.ReplaceAll(flag, "-", "_")) {
			if hasValue {
				out = append(out, s.envArgSpan(rest, at+len(name)+1, end)...)
			}
			continue
		}
		if hasValue {
			if start := at + len(name) + 1; end > start {
				out = append(out, span{start, end})
			}
			continue
		}
		if i+1 < len(args) {
			out = append(out, nodeSpan(args[i+1]))
		}
	}
	return out
}

// wordText is the literal text a word starts with, when it starts with one:
// a plain part, or the whole word in quotes, as in -e "API_TOKEN=x". at is
// the offset of the text in the command, end where a value in the word
// ends: the end of the word, or its closing quote. whole is false when more
// parts follow a plain part.
func wordText(w *syntax.Word) (text string, at, end int, whole, ok bool) {
	switch p := w.Parts[0].(type) {
	case *syntax.Lit:
		return p.Value, int(p.Pos().Offset()), int(w.End().Offset()), len(w.Parts) == 1, true
	case *syntax.SglQuoted:
		if len(w.Parts) == 1 && !p.Dollar {
			return p.Value, int(p.Left.Offset()) + 1, int(p.Right.Offset()), true, true
		}
	case *syntax.DblQuoted:
		if len(w.Parts) == 1 && !p.Dollar && len(p.Parts) > 0 {
			if lit, isLit := p.Parts[0].(*syntax.Lit); isLit {
				return lit.Value, int(lit.Pos().Offset()), int(p.Right.Offset()), len(p.Parts) == 1, true
			}
		}
	}
	return "", 0, 0, false, false
}

// envArgSpan finds the value of NAME=value at the start of text, which
// starts at offset at of the command, up to end, when NAME is a secret name.
func (s Secrets) envArgSpan(text string, at, end int) []span {
	name, _, ok := strings.Cut(text, "=")
	if !ok || !envName.MatchString(name) || !s.secretName(name) {
		return nil
	}
	if start := at + len(name) + 1; end > start {
		return []span{{start, end}}
	}
	return nil
}

// passwordFlags are the flags of well-known programs that take a password
// under a name the secret-name rule does not catch. The value of a
// user flag is "user:password", and only what follows the colon is a
// secret. An attached flag (-pSECRET) holds its value in the same word.
var passwordFlags = map[string]struct {
	user     []string // flags whose value is user:password
	next     []string // flags whose value is the next word
	attached string   // a flag whose value follows it in one word
}{
	"curl":         {user: []string{"-u", "--user", "-U", "--proxy-user"}},
	"mysql":        {attached: "-p"},
	"mysqldump":    {attached: "-p"},
	"mysqladmin":   {attached: "-p"},
	"mariadb":      {attached: "-p"},
	"mariadb-dump": {attached: "-p"},
	"sshpass":      {next: []string{"-p"}, attached: "-p"},
}

// credentialSpans finds the passwords given to well-known programs through
// the flags of passwordFlags, and to `docker login` or `podman login`
// through -p.
func credentialSpans(command string, args []*syntax.Word) []span {
	if len(args) == 0 {
		return nil
	}
	program := path.Base(args[0].Lit())
	flags := passwordFlags[program]
	if (program == "docker" || program == "podman") && len(args) > 1 && args[1].Lit() == "login" {
		flags.next = []string{"-p"}
	}
	var out []span
	for i, w := range args[1:] {
		lit := w.Lit()
		next := i+2 < len(args)
		at, end := int(w.Pos().Offset()), int(w.End().Offset())
		switch {
		case slices.Contains(flags.user, lit) && next:
			out = append(out, userPassword(command, args[i+2])...)
		case slices.Contains(flags.next, lit) && next:
			out = append(out, nodeSpan(args[i+2]))
		case flags.attached != "" && strings.HasPrefix(command[at:end], flags.attached) && end-at > len(flags.attached):
			out = append(out, span{at + len(flags.attached), end})
		default:
			for _, f := range flags.user {
				switch {
				case strings.HasPrefix(f, "--") && strings.HasPrefix(command[at:end], f+"="):
					out = append(out, passwordAfterColon(command, at+len(f)+1, end)...)
				case !strings.HasPrefix(f, "--") && strings.HasPrefix(command[at:end], f) && end-at > len(f):
					// An attached short flag: -uuser:password.
					out = append(out, passwordAfterColon(command, at+len(f), end)...)
				}
			}
		}
	}
	return out
}

// userPassword is the password of a user:password word.
func userPassword(command string, w *syntax.Word) []span {
	return passwordAfterColon(command, int(w.Pos().Offset()), int(w.End().Offset()))
}

// passwordAfterColon is what follows the first colon of command[start:end],
// less a closing quote. There is none without a colon: the program then asks
// for the password.
func passwordAfterColon(command string, start, end int) []span {
	colon := strings.IndexByte(command[start:end], ':')
	if colon < 0 {
		return nil
	}
	from := start + colon + 1
	if end > from && (command[end-1] == '"' || command[end-1] == '\'') && (command[start] == command[end-1]) {
		end--
	}
	if end <= from {
		return nil
	}
	return []span{{from, end}}
}

// wordSpans finds, in the source text of one word, the value of an
// authorization or secret-named header, the password of a URL, and the user
// of an HTTP URL without a password, which is where a token goes.
func (s Secrets) wordSpans(command string, w *syntax.Word) []span {
	at := int(w.Pos().Offset())
	src := command[at:int(w.End().Offset())]
	var out []span
	for _, c := range headerStarts(src) {
		m := header.FindStringSubmatchIndex(src[c:])
		if m == nil || !s.authHeader(src[c+m[2]:c+m[3]]) {
			continue
		}
		start, end := c+m[1], len(src)
		if end > start && (src[end-1] == '"' || src[end-1] == '\'') && (src[0] == src[end-1] || (c > 0 && src[c-1] == src[end-1])) {
			end--
		}
		if end > start {
			out = append(out, span{at + start, at + end})
		}
		break
	}
	for _, re := range []*regexp.Regexp{urlPassword, urlToken} {
		for _, m := range re.FindAllStringSubmatchIndex(src, -1) {
			out = append(out, span{at + m[2], at + m[3]})
		}
	}
	return out
}

// headerStarts lists where a header name can start in a word: at its start,
// inside a leading quote, after the = of a --header=... flag, or after an
// attached -H, the flag itself quoted or not.
func headerStarts(src string) []int {
	starts := []int{0}
	off := 0
	if src != "" && (src[0] == '"' || src[0] == '\'') {
		off = 1
		starts = append(starts, 1)
	}
	add := func(at int) {
		starts = append(starts, at)
		if at < len(src) && (src[at] == '"' || src[at] == '\'') {
			starts = append(starts, at+1)
		}
	}
	if rest := src[off:]; strings.HasPrefix(rest, "-") {
		if eq := strings.IndexByte(rest, '='); eq >= 0 {
			add(off + eq + 1)
		}
		if strings.HasPrefix(rest, "-H") {
			add(off + 2)
		}
	}
	return starts
}

func (s Secrets) authHeader(name string) bool {
	return strings.EqualFold(name, "Authorization") || strings.EqualFold(name, "Proxy-Authorization") ||
		s.secretName(strings.ReplaceAll(name, "-", "_"))
}

// splice replaces each span of text by Mark. Spans that overlap are joined.
func splice(text string, spans []span) string {
	if len(spans) == 0 {
		return text
	}
	slices.SortFunc(spans, func(a, b span) int { return a.start - b.start })
	var b strings.Builder
	at := 0
	for _, sp := range spans {
		if sp.end <= at {
			continue
		}
		if sp.start < at {
			sp.start = at
		} else {
			b.WriteString(text[at:sp.start])
			b.WriteString(Mark)
		}
		at = sp.end
	}
	b.WriteString(text[at:])
	return b.String()
}

// CleanDescription returns the agent's description of a command as it is
// stored: secret values masked, its first line only, control characters as
// spaces, at most MaxDescriptionBytes and never cut inside a character.
func CleanDescription(desc string, s Secrets) string {
	if s.Values != nil {
		desc = s.Values.MaskString(desc)
	}
	desc, _, _ = strings.Cut(desc, "\n")
	desc = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, desc)
	desc = strings.TrimSpace(desc)
	if len(desc) > MaxDescriptionBytes {
		end := MaxDescriptionBytes
		for end > 0 && !utf8.RuneStart(desc[end]) {
			end--
		}
		desc = strings.TrimSpace(desc[:end])
	}
	return desc
}
