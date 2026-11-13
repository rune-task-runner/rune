package cli

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rune-task-runner/rune/internal/config"
	"github.com/rune-task-runner/rune/mcpserver"
)

// Spec 042: the adapter captures each stream in a bounded head-and-tail
// buffer. The task bodies use shell builtins only, so they run on every OS.

// fill is a shell snippet leaving $s at 100 bytes doubled 15 times:
// 3,276,800 bytes.
const fill = "s=0123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789; " +
	"i=0; while [ $i -lt 15 ]; do s=$s$s; i=$((i+1)); done"

// floodBytes is what `printf 'BEGIN\n%s\nEND\n' "$s"` writes after fill.
const floodBytes = 6 + 3276800 + 5

var outputMarkerRE = regexp.MustCompile(`\n\[truncated (\d+) bytes\]\n`)

// shownAndDropped returns the bytes of a truncated stream that are output
// and the count its one marker reports.
func shownAndDropped(t *testing.T, stream string) (shown int, dropped int64) {
	t.Helper()
	locs := outputMarkerRE.FindAllStringSubmatchIndex(stream, -1)
	if len(locs) != 1 {
		t.Fatalf("want exactly one marker, got %d in %d bytes", len(locs), len(stream))
	}
	n, err := strconv.ParseInt(stream[locs[0][2]:locs[0][3]], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return len(stream) - (locs[0][1] - locs[0][0]), n
}

func callTask(t *testing.T, a *mcpAdapter, name string) mcpserver.Result {
	t.Helper()
	res, err := a.Call(context.Background(), name, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestAdapterLimit(t *testing.T) {
	cases := []struct {
		flag, setting, want int
	}{
		{0, 0, 65536},
		{0, 2048, 2048},
		{4096, 0, 4096},
		{4096, 2048, 4096},
	}
	for _, c := range cases {
		a := &mcpAdapter{outputLimit: c.flag, settings: config.Settings{MCPOutputLimit: c.setting}}
		if got := a.limit(); got != c.want {
			t.Errorf("flag %d setting %d: limit() = %d, want %d", c.flag, c.setting, got, c.want)
		}
	}
}

func TestMCPCallCapsStdout(t *testing.T) {
	a := adapterFor(t, "flood:\n    @"+fill+`; printf 'BEGIN\n%s\nEND\n' "$s"`+"\n")
	res := callTask(t, a, "flood")
	if res.ExitCode != 0 || res.Stderr != "" {
		t.Fatalf("exit %d, stderr %q", res.ExitCode, res.Stderr)
	}
	if len(res.Stdout) > 65536 {
		t.Errorf("stdout is %d bytes", len(res.Stdout))
	}
	if !strings.HasPrefix(res.Stdout, "BEGIN\n0123456789") || !strings.HasSuffix(res.Stdout, "0123456789\nEND\n") {
		t.Errorf("stdout does not keep the beginning and the end")
	}
	if shown, dropped := shownAndDropped(t, res.Stdout); int64(shown)+dropped != floodBytes {
		t.Errorf("shown %d + dropped %d != %d", shown, dropped, floodBytes)
	}
}

func TestMCPCallCapsStderr(t *testing.T) {
	a := adapterFor(t, "flood:\n    @"+fill+`; printf 'BEGIN\n%s\nEND\n' "$s" >&2`+"\n")
	res := callTask(t, a, "flood")
	if res.Stdout != "" {
		t.Fatalf("stdout %q", res.Stdout)
	}
	if len(res.Stderr) > 65536 {
		t.Errorf("stderr is %d bytes", len(res.Stderr))
	}
	if !strings.HasPrefix(res.Stderr, "BEGIN\n") || !strings.HasSuffix(res.Stderr, "\nEND\n") {
		t.Errorf("stderr does not keep the beginning and the end")
	}
	if shown, dropped := shownAndDropped(t, res.Stderr); int64(shown)+dropped != floodBytes {
		t.Errorf("shown %d + dropped %d != %d", shown, dropped, floodBytes)
	}
}

func TestMCPCallSmallOutputUnchanged(t *testing.T) {
	want := strings.Repeat("0123456789", 6000)
	a := adapterFor(t, "small:\n    @printf '%s' "+want+"\n")
	if res := callTask(t, a, "small"); res.Stdout != want {
		t.Errorf("60,000 bytes came back as %d bytes", len(res.Stdout))
	}
}

// A dependency, the task, and its post-hook write one stream, and the limit
// applies to the whole of it.
func TestMCPCallCapsDependencyOutput(t *testing.T) {
	big := func(first, last string) string {
		return "    @" + fill + `; printf '` + first + `\n%s\n` + last + `\n' "$s"` + "\n"
	}
	src := "main: dep && hook\n" + big("MAIN", "MAINEND") +
		"\ndep:\n" + big("DEPFIRST", "DEPEND") +
		"\nhook:\n" + big("HOOKFIRST", "HOOKLAST")
	a := adapterFor(t, src)
	res := callTask(t, a, "main")
	if len(res.Stdout) > 65536 {
		t.Errorf("stdout is %d bytes", len(res.Stdout))
	}
	if !strings.HasPrefix(res.Stdout, "DEPFIRST\n") || !strings.HasSuffix(res.Stdout, "\nHOOKLAST\n") {
		t.Errorf("stream does not start in the dependency and end in the hook: %q ... %q",
			res.Stdout[:20], res.Stdout[len(res.Stdout)-20:])
	}
	shownAndDropped(t, res.Stdout)
}

// Spec 042 US2: the mask writer sits above the capture, so no cut can let a
// secret, or a part of one, through.
func TestMCPCallMasksAcrossCuts(t *testing.T) {
	const secret = "Zq7Lw9Xk2Vb5Nm8Rt4Yp6Hs3"
	const limit = 1024
	headMax := (limit - 40) / 2
	tailMax := limit - 40 - headMax

	type layout struct {
		name      string
		pad, rest int
		wantMask  bool
	}
	var layouts []layout
	for pad := headMax - 30; pad <= headMax+6; pad++ {
		layouts = append(layouts, layout{"head cut", pad, 20000, false})
	}
	for rest := tailMax - 30; rest <= tailMax+6; rest++ {
		layouts = append(layouts, layout{"tail cut", 20000, rest, false})
	}
	layouts = append(layouts,
		layout{"middle", 10000, 10000, false},
		layout{"inside head", 100, 20000, true},
		layout{"inside tail", 20000, 100, true},
	)

	for _, l := range layouts {
		body := strings.Repeat(".", l.pad) + secret + strings.Repeat(".", l.rest)
		a := adapterFor(t, "leak:\n    @printf '%s' '"+body+"'\n")
		a.baseEnv = []string{"MY_API_TOKEN=" + secret}
		a.maskSet = deriveMaskSet(a.baseEnv, a.tasks, nil, nil)
		a.outputLimit = limit
		res := callTask(t, a, "leak")
		if len(res.Stdout) > limit {
			t.Errorf("%s pad %d rest %d: stdout is %d bytes", l.name, l.pad, l.rest, len(res.Stdout))
		}
		for i := 0; i+4 <= len(secret); i++ {
			if strings.Contains(res.Stdout, secret[i:i+4]) {
				t.Errorf("%s pad %d rest %d: part %q of the secret is in the result", l.name, l.pad, l.rest, secret[i:i+4])
				break
			}
		}
		if l.wantMask && !strings.Contains(res.Stdout, "***") {
			t.Errorf("%s: the kept part does not show the mask", l.name)
		}
	}
}

// Spec 042 US3: several writers at once still give one consistent, capped
// stream. Run under -race.
func TestMCPCallParallelWritersCapped(t *testing.T) {
	dep := func(name string) string {
		return name + ":\n    @s=0123456789; i=0; while [ $i -lt 16 ]; do s=$s$s; i=$((i+1)); done; printf '%s' \"$s\"\n\n"
	}
	src := "[parallel]\nmain: a b c\n    @true\n\n" + dep("a") + dep("b") + dep("c")
	a := adapterFor(t, src)
	a.outputLimit = 4096
	res := callTask(t, a, "main")
	if res.ExitCode != 0 {
		t.Fatalf("exit %d: %s", res.ExitCode, res.Stderr)
	}
	if len(res.Stdout) > 4096 {
		t.Errorf("stdout is %d bytes", len(res.Stdout))
	}
	const total = 3 * 10 * (1 << 16)
	if shown, dropped := shownAndDropped(t, res.Stdout); int64(shown)+dropped != total {
		t.Errorf("shown %d + dropped %d != %d", shown, dropped, total)
	}
}

func TestMCPCallSingleLongLine(t *testing.T) {
	a := adapterFor(t, "line:\n    @"+fill+`; printf '%s' "$s"`+"\n")
	res := callTask(t, a, "line")
	if len(res.Stdout) > 65536 {
		t.Errorf("stdout is %d bytes", len(res.Stdout))
	}
	lines := strings.Split(res.Stdout, "\n")
	if len(lines) != 3 || !regexp.MustCompile(`^\[truncated \d+ bytes\]$`).MatchString(lines[1]) {
		t.Errorf("want head, a marker line, and tail; got %d lines", len(lines))
	}
}

// A long run never held as one string: the adapter keeps counting and stays
// within the limit. The memory bound itself is TestOutputBufferMemoryBound.
func TestMCPCallLongRunStaysCapped(t *testing.T) {
	if testing.Short() {
		t.Skip("prints 64 MiB")
	}
	src := "long:\n    @s=0123456789abcdef; i=0; while [ $i -lt 16 ]; do s=$s$s; i=$((i+1)); done; " +
		"i=0; while [ $i -lt 64 ]; do printf '%s' \"$s\"; i=$((i+1)); done\n"
	a := adapterFor(t, src)
	res := callTask(t, a, "long")
	if res.ExitCode != 0 {
		t.Fatalf("exit %d: %s", res.ExitCode, res.Stderr)
	}
	if len(res.Stdout) > 65536 {
		t.Errorf("stdout is %d bytes", len(res.Stdout))
	}
	if shown, dropped := shownAndDropped(t, res.Stdout); int64(shown)+dropped != 64<<20 {
		t.Errorf("shown %d + dropped %d != %d", shown, dropped, 64<<20)
	}
}

func TestMCPCallCancelledStaysCapped(t *testing.T) {
	a := adapterFor(t, "spin:\n    @while true; do printf '%s' 0123456789012345678901234567890123456789; done\n")
	a.outputLimit = 2048
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	res, err := a.Call(ctx, "spin", map[string]string{})
	if err != nil {
		return
	}
	if res.ExitCode == 0 {
		t.Errorf("a cancelled run exited 0")
	}
	if len(res.Stdout) > 2048 || len(res.Stderr) > 2048 {
		t.Errorf("stdout %d bytes, stderr %d bytes", len(res.Stdout), len(res.Stderr))
	}
}

// The [context] hook keeps its own 8 KiB cap and marker however small the
// output limit is (spec 042 FR-011).
func TestGatherContextKeepsItsOwnCap(t *testing.T) {
	src := "[context]\nhealth:\n    @s=0123456789; i=0; while [ $i -lt 12 ]; do s=$s$s; i=$((i+1)); done; printf '%s' \"$s\"\n"
	for _, limit := range []int{0, 1024, 1 << 20} {
		a := adapterFor(t, src)
		a.outputLimit = limit
		var stderr strings.Builder
		got := a.gatherContext(context.Background(), &stderr)
		want := strings.Repeat("0123456789", 820)[:contextMaxBytes] + "\n[truncated]"
		if got != want {
			t.Errorf("limit %d: context is %d bytes ending %q", limit, len(got), got[max(0, len(got)-30):])
		}
	}
}

func TestAgentEndpointUsesSettingLimit(t *testing.T) {
	for _, c := range []struct {
		src  string
		want int
	}{
		{"set mcp_output_limit := \"2KiB\"\n\nok:\n    @true\n", 2048},
		{"ok:\n    @true\n", 65536},
	} {
		base := adapterFor(t, c.src)
		eng := &engine{file: base.file, tasks: base.tasks, assigns: base.assigns, settings: base.settings}
		adapter := eng.newAgentAdapter()
		if got := adapter.limit(); got != c.want {
			t.Errorf("agent adapter limit() = %d, want %d", got, c.want)
		}
		// The server is given the adapter's own number, so the two cannot
		// drift into truncating one stream twice.
		if got := eng.agentMCPOptions(adapter.limit()).OutputLimit; got != c.want {
			t.Errorf("agentMCPOptions().OutputLimit = %d, want %d", got, c.want)
		}
	}
}

// internal/config repeats the bounds to stay clear of the MCP server.
func TestOutputLimitBoundsAgree(t *testing.T) {
	if config.MinOutputLimit != mcpserver.MinOutputLimit || config.MaxOutputLimit != mcpserver.MaxOutputLimit {
		t.Errorf("config bounds %d..%d differ from mcpserver bounds %d..%d",
			config.MinOutputLimit, config.MaxOutputLimit, mcpserver.MinOutputLimit, mcpserver.MaxOutputLimit)
	}
}

func TestParseOutputLimitFlag(t *testing.T) {
	if got, err := parseOutputLimitFlag(nil); got != 0 || err != nil {
		t.Errorf("nil flag = %d, %v", got, err)
	}
	v := "256KiB"
	if got, err := parseOutputLimitFlag(&v); got != 262144 || err != nil {
		t.Errorf("256KiB = %d, %v", got, err)
	}
	for _, bad := range []string{"lots", "", "-1", "0", "10"} {
		_, err := parseOutputLimitFlag(&bad)
		want := fmt.Sprintf(`--output-limit: invalid size %q: use a whole number of bytes or a KiB or MiB count, such as 65536 or 256KiB, from 1KiB to 1024MiB`, bad)
		if _, ok := err.(*UsageError); !ok || err.Error() != want {
			t.Errorf("%q: error = %v, want usage error %q", bad, err, want)
		}
	}
}
