package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/rune-task-runner/rune/internal/receipt"
	"github.com/rune-task-runner/rune/mcpserver"
)

const channelRunefile = "set receipts\n\nbuild:\n    @echo built\n\ntest: build\n    @echo ok\n"

// US5: a tool call's receipts share the call's audit ID and carry what the
// client reported as claims.
func TestMCPCallWritesReceipts(t *testing.T) {
	dir := receiptProject(t, channelRunefile, map[string]string{}, true)
	a, _, err := loadMCPAdapter(context.Background(), Options{Stderr: &bytes.Buffer{}, Version: "1.0.0"}, filepath.Join(dir, "Runefile"))
	if err != nil {
		t.Fatal(err)
	}
	if a.receipts == nil {
		t.Fatal("the load has no signer")
	}
	cs, logDir, _ := auditedServer(t, a, mcpserver.Options{}, nil)
	callWith(t, cs, "test", nil)
	call := lastCall(t, logDir)
	list := readReceipts(t, dir)
	if len(list) != 2 {
		t.Fatalf("%d receipts", len(list))
	}
	for _, rc := range list {
		if rc.Channel != receipt.ChannelMCP || rc.Invocation != call.ID || rc.Status != receipt.StatusPassed {
			t.Errorf("%s: channel %s invocation %s (call %s) status %s", rc.Task, rc.Channel, rc.Invocation, call.ID, rc.Status)
		}
		if rc.Claims == nil || rc.Claims.Client == nil || rc.Claims.Client.Name != "test" || rc.Claims.Client.Version != "v1" || rc.Claims.Auth != "process" {
			t.Errorf("%s: claims %+v", rc.Task, rc.Claims)
		}
	}
	test := receiptOf(t, list, "test")
	if test.Related == nil || len(test.Related.Dependencies) != 1 {
		t.Errorf("test's relations %+v", test.Related)
	}
}

func TestCallRecorderWithoutAudit(t *testing.T) {
	a := &mcpAdapter{}
	if a.callRecorder(nil) != nil {
		t.Fatal("an adapter without a signer records")
	}
	a.receipts = &receiptSigner{}
	clearClaimVars(t)
	rec := a.callRecorder(nil)
	if rec == nil || len(rec.invocation) != 16 || rec.channel != receipt.ChannelMCP || rec.claims != nil {
		t.Fatalf("recorder %+v", rec)
	}
}

func TestReceiptChannelsOnTheCommandLine(t *testing.T) {
	dir := receiptProject(t, channelRunefile, map[string]string{}, true)
	t.Setenv("CI", "true")
	t.Setenv("GITHUB_RUN_ID", "42")
	if _, err := runReceipts(t, dir, Options{}, "build"); err != nil {
		t.Fatal(err)
	}
	rc := readReceipts(t, dir)[0]
	if rc.Channel != receipt.ChannelCLI || rc.Claims == nil || rc.Claims.Env["CI"] != "true" || rc.Claims.Env["GITHUB_RUN_ID"] != "42" || rc.Claims.Client != nil {
		t.Fatalf("cli: %s %+v", rc.Channel, rc.Claims)
	}
}

func TestReceiptChannelOfAnAgentRun(t *testing.T) {
	dir := receiptProject(t, channelRunefile, map[string]string{}, true)
	t.Setenv(agentRunVar, "1")
	t.Setenv(invocationVar, "0123456789abcdef")
	if _, err := runReceipts(t, dir, Options{}, "build"); err != nil {
		t.Fatal(err)
	}
	rc := readReceipts(t, dir)[0]
	if rc.Channel != receipt.ChannelAgent || rc.ParentInvocation != "0123456789abcdef" || rc.Invocation == rc.ParentInvocation {
		t.Fatalf("agent run: %s parent %q invocation %q", rc.Channel, rc.ParentInvocation, rc.Invocation)
	}
}

func TestReceiptChannelOfCIRun(t *testing.T) {
	dir := receiptProject(t, channelRunefile, map[string]string{}, true)
	if _, _, err := runCIRun(t, dir, CIRunFlags{}, "test"); err != nil {
		t.Fatal(err)
	}
	list := readReceipts(t, dir)
	if len(list) != 1 {
		t.Fatalf("%d receipts: the job runs one body", len(list))
	}
	if rc := list[0]; rc.Task != "test" || rc.Channel != receipt.ChannelCIRun || rc.Related != nil {
		t.Fatalf("ci run: %+v", rc)
	}
}

// An agent's call has no command-line overrides to record.
func TestMCPCallRecordsNoOverrides(t *testing.T) {
	dir := receiptProject(t, channelRunefile, map[string]string{}, true)
	a, _, err := loadMCPAdapter(context.Background(), Options{Stderr: &bytes.Buffer{}, Version: "1.0.0"}, filepath.Join(dir, "Runefile"))
	if err != nil {
		t.Fatal(err)
	}
	cs, _, _ := auditedServer(t, a, mcpserver.Options{}, nil)
	callWith(t, cs, "build", nil)
	for _, rc := range readReceipts(t, dir) {
		if rc.Overrides != nil {
			t.Errorf("%s: overrides %v", rc.Task, rc.Overrides)
		}
	}
}

// Spec 061 FR-017: the receipts of an agent task's calls name the overrides
// of the command line that started it.
func TestCallRecorderNamesOverrides(t *testing.T) {
	clearClaimVars(t)
	a := &mcpAdapter{receipts: &receiptSigner{}, overrides: map[string]string{"B": "1", "A": "2"}}
	if rec := a.callRecorder(nil); !slices.Equal(rec.overrides, []string{"A", "B"}) {
		t.Fatalf("overrides %q", rec.overrides)
	}
	a.overrides = map[string]string{}
	if rec := a.callRecorder(nil); rec.overrides != nil {
		t.Fatalf("no overrides: %q", rec.overrides)
	}
}
