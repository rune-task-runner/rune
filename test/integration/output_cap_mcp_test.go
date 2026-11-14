package integration

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Spec 042 quickstart: the output limit, with the real binary over stdio.
// This stands in for the issue's TestMCPE2E_Pending_OutputCapped, whose suite
// is not in this repository. Bodies use shell builtins only, so they run the
// same on every OS.

// capFill leaves $s at 100 bytes doubled 15 times: 3,276,800 bytes.
const capFill = "s=0123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789; " +
	"i=0; while [ $i -lt 15 ]; do s=$s$s; i=$((i+1)); done"

// capFloodBytes is one flooded stream: BEGIN, the filler, END, each on a line.
const capFloodBytes = 6 + 3276800 + 5

const capRunefile = `# About 3 MB on stdout.
flood:
    @` + capFill + `; printf 'BEGIN\n%s\nEND\n' "$s"

# About 3 MB on each stream.
both:
    @` + capFill + `; printf 'BEGIN\n%s\nEND\n' "$s"; printf 'EBEGIN\n%s\nEEND\n' "$s" >&2

# About 3 MB, then exit 3.
fail:
    @` + capFill + `; printf 'BEGIN\n%s\nEND\n' "$s"; exit 3

# 60,000 bytes: under the limit.
small:
    @s=0123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789; i=0; while [ $i -lt 600 ]; do printf '%s' "$s"; i=$((i+1)); done

# Project health for agents.
[context]
health:
    @echo context > context.done
`

var capMarkerRE = regexp.MustCompile(`(?m)^\[truncated (\d+) bytes\]$`)

// capResult calls task on a server started with flags and returns the result
// text and its error flag.
func capResult(t *testing.T, dir, task string, flags ...string) (text string, isError bool) {
	t.Helper()
	reply, stderr := mcpServerCall(t, dir, append([]string{"mcp"}, flags...), task, "{}")
	var frame struct {
		Result *struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(reply), &frame); err != nil {
		t.Fatalf("decoding tools/call reply: %v", err)
	}
	if frame.Result == nil || len(frame.Result.Content) != 1 {
		t.Fatalf("tools/call returned no single text result; stderr=%q reply=%.300s", stderr, reply)
	}
	return frame.Result.Content[0].Text, frame.Result.IsError
}

// capDropped sums the counts of the markers in text and checks how many
// there are.
func capDropped(t *testing.T, text string, markers int) int {
	t.Helper()
	found := capMarkerRE.FindAllStringSubmatch(text, -1)
	if len(found) != markers {
		t.Fatalf("marker lines = %d, want %d", len(found), markers)
	}
	total := 0
	for _, m := range found {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatal(err)
		}
		total += n
	}
	return total
}

// Scenario A.
func TestOutputCap_FloodIsCapped(t *testing.T) {
	dir := writeRunefile(t, capRunefile)
	text, isError := capResult(t, dir, "flood")
	if isError {
		t.Error("exit 0 was marked as an error")
	}
	if len(text) > 65568 {
		t.Errorf("result is %d bytes, want at most 65568", len(text))
	}
	if !strings.HasPrefix(text, "BEGIN\n0123456789") {
		t.Errorf("result does not start with the first output: %q", text[:20])
	}
	if !strings.HasSuffix(text, "0123456789\nEND\n[exit 0]") {
		t.Errorf("result does not end with the last output and the exit line: %q", text[len(text)-30:])
	}
	dropped := capDropped(t, text, 1)
	marker := "\n[truncated " + strconv.Itoa(dropped) + " bytes]\n"
	shown := len(text) - len(marker) - len("[exit 0]")
	if shown+dropped != capFloodBytes {
		t.Errorf("shown %d + dropped %d != %d", shown, dropped, capFloodBytes)
	}
}

// Scenario B.
func TestOutputCap_BothStreams(t *testing.T) {
	dir := writeRunefile(t, capRunefile)
	text, _ := capResult(t, dir, "both")
	if len(text) > 131104 {
		t.Errorf("result is %d bytes, want at most 131104", len(text))
	}
	capDropped(t, text, 2)
	if !strings.HasPrefix(text, "BEGIN\n") || !strings.Contains(text, "\nEND\nEBEGIN\n") || !strings.HasSuffix(text, "\nEEND\n[exit 0]") {
		t.Errorf("result does not keep both ends of both streams")
	}
}

// Scenario C.
func TestOutputCap_FailKeepsExitLine(t *testing.T) {
	dir := writeRunefile(t, capRunefile)
	text, isError := capResult(t, dir, "fail")
	if !isError {
		t.Error("exit 3 was not marked as an error")
	}
	if !strings.Contains(text, "\nEND\n") || !strings.Contains(text, "[exit 3]") {
		t.Errorf("result lost the end of stdout or the exit line: %q", text[len(text)-200:])
	}
	// Stderr carries the failure banner, which is short.
	capDropped(t, text, 1)
}

// Scenario D.
func TestOutputCap_SmallUnchanged(t *testing.T) {
	dir := writeRunefile(t, capRunefile)
	text, _ := capResult(t, dir, "small")
	want := strings.Repeat("0123456789", 6000) + "\n[exit 0]"
	if text != want {
		t.Errorf("a result within the limit changed: %d bytes, want %d", len(text), len(want))
	}
}

// Scenario E.
func TestOutputCap_FlagRaisesLimit(t *testing.T) {
	dir := writeRunefile(t, capRunefile)
	text, _ := capResult(t, dir, "flood", "--output-limit", "256KiB")
	if len(text) <= 65536 || len(text) > 262176 {
		t.Errorf("result is %d bytes, want more than 65536 and at most 262176", len(text))
	}
	capDropped(t, text, 1)
}

// Scenario F.
func TestOutputCap_SettingLowersLimit(t *testing.T) {
	dir := writeRunefile(t, "set mcp_output_limit := \"1KiB\"\n\n"+capRunefile)
	text, _ := capResult(t, dir, "flood")
	if len(text) > 1056 {
		t.Errorf("result is %d bytes, want at most 1056", len(text))
	}
	capDropped(t, text, 1)
}

// Scenario G.
func TestOutputCap_FlagBeatsSetting(t *testing.T) {
	dir := writeRunefile(t, "set mcp_output_limit := \"1KiB\"\n\n"+capRunefile)
	text, _ := capResult(t, dir, "flood", "--output-limit", "4KiB")
	if len(text) <= 1056 || len(text) > 4128 {
		t.Errorf("result is %d bytes, want more than 1056 and at most 4128", len(text))
	}
}

// Scenario H.
func TestOutputCap_BadFlagRunsNothing(t *testing.T) {
	for _, v := range []string{"lots", "", "-1", "0", "10"} {
		dir := writeRunefile(t, capRunefile)
		r := runWithStdin(t, dir, "", "mcp", "--output-limit", v)
		if r.code != 2 {
			t.Errorf("%q: exit %d, want 2 (stderr %q)", v, r.code, r.stderr)
		}
		want := `--output-limit: invalid size "` + v + `": use a whole number of bytes or a KiB or MiB count, such as 65536 or 256KiB, from 1KiB to 1024MiB`
		if !strings.Contains(r.stderr, want) {
			t.Errorf("%q: stderr %q lacks %q", v, r.stderr, want)
		}
		if exists(t, dir, "context.done") {
			t.Errorf("%q: the [context] hook ran before the usage error", v)
		}
	}
}

// Scenario I.
func TestOutputCap_BadSettingIsDiagnosed(t *testing.T) {
	dir := writeRunefile(t, "set mcp_output_limit := \"lots\"\n\n"+capRunefile)
	for _, args := range [][]string{{"small"}, {"mcp"}} {
		r := runWithStdin(t, dir, "", args...)
		if r.code == 0 {
			t.Errorf("%v: exit 0, want a failure", args)
		}
		for _, want := range []string{
			`invalid mcp_output_limit: invalid size "lots"`,
			"Runefile:1:25",
			"^",
		} {
			if !strings.Contains(r.stderr, want) {
				t.Errorf("%v: stderr lacks %q:\n%s", args, want, r.stderr)
			}
		}
		if r.stdout != "" {
			t.Errorf("%v: stdout = %.80q, want nothing to have run", args, r.stdout)
		}
		if exists(t, dir, "context.done") {
			t.Errorf("%v: the [context] hook ran", args)
		}
	}
}

// Scenario J.
func TestOutputCap_CommandLineIsNotCapped(t *testing.T) {
	dir := writeRunefile(t, "set mcp_output_limit := \"1KiB\"\n\n"+capRunefile)
	r := run(t, dir, nil, "flood")
	if r.code != 0 || len(r.stdout) != capFloodBytes {
		t.Errorf("exit %d, stdout %d bytes, want %d", r.code, len(r.stdout), capFloodBytes)
	}
}

func TestOutputCap_EquivalentSpellings(t *testing.T) {
	dir := writeRunefile(t, capRunefile)
	plain, _ := capResult(t, dir, "flood")
	for _, v := range []string{"65536", "64KiB", "65536B"} {
		if got, _ := capResult(t, dir, "flood", "--output-limit", v); got != plain {
			t.Errorf("--output-limit %s gives a different result than no flag", v)
		}
	}
}

// Scenario K: a secret on either cut never comes through, whole or in part.
func TestOutputCap_SecretAtTheCuts(t *testing.T) {
	const secret = "Zq7Lw9Xk2Vb5Nm8Rt4Yp6Hs3"
	const headMax = 32748
	dots := func(n int) string { return strings.Repeat(".", n) }
	// The masked secret is 3 bytes, so the tail starts headMax bytes before
	// the end of pad + 3 + rest.
	src := `[env("DEMO_API_TOKEN", "` + secret + `")]
athead:
    @printf '%s%s%s' '` + dots(headMax-10) + `' "$DEMO_API_TOKEN" '` + dots(100000) + `'

[env("DEMO_API_TOKEN", "` + secret + `")]
attail:
    @printf '%s%s%s' '` + dots(100000) + `' "$DEMO_API_TOKEN" '` + dots(headMax-1) + `'
`
	dir := writeRunefile(t, src)
	for _, task := range []string{"athead", "attail"} {
		text, _ := capResult(t, dir, task)
		capDropped(t, text, 1)
		for i := 0; i+4 <= len(secret); i++ {
			if strings.Contains(text, secret[i:i+4]) {
				t.Errorf("%s: part %q of the secret is in the result", task, secret[i:i+4])
				break
			}
		}
	}
}
