package planexec

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wbern/adrian/go/internal/adr"
	"github.com/wbern/adrian/go/internal/reviewpacket"
)

func onePacket() reviewpacket.Plan {
	return reviewpacket.Build([]reviewpacket.Input{{SourceID: "scope", ADR: adr.ADR{ID: "0040", Decision: "Rule", DiffContext: true}, Files: []string{"a.ts"}, Diff: "diff --git a/a.ts b/a.ts\n@@ -1 +1 @@\n+yes\n"}}, reviewpacket.Options{MaxTokensPerChunk: 1024})
}
func adapterFixture(t *testing.T, body string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	plan := filepath.Join(dir, "plan.json")
	raw, _ := json.Marshal(onePacket())
	if err := os.WriteFile(plan, raw, 0600); err != nil {
		t.Fatal(err)
	}
	adapter := filepath.Join(dir, "adapter.sh")
	if err := os.WriteFile(adapter, []byte("#!/bin/sh\ncat >/dev/null\n"+body+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return plan, adapter
}
func TestRunCLIEmitsIncompleteEvidenceOnAdapterFailure(t *testing.T) {
	plan, adapter := adapterFixture(t, "exit 7")
	var out bytes.Buffer
	if err := RunCLI(context.Background(), []string{"--plan", plan, "--adapter", adapter}, &out); err == nil {
		t.Fatal("failure hidden")
	}
	var run struct {
		Incomplete   bool   `json:"incomplete"`
		FailedPacket int    `json:"failedPacketIndex"`
		Failure      string `json:"failure"`
	}
	if err := json.Unmarshal(out.Bytes(), &run); err != nil || !run.Incomplete || run.FailedPacket != 1 || run.Failure == "" {
		t.Fatalf("partial attempt lost: %s %v", out.String(), err)
	}
}
func TestExecuteDoesNotInvokeAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	_, err := Execute(ctx, onePacket(), func(context.Context, reviewpacket.Packet) ([]byte, error) { called = true; return []byte(`{}`), nil })
	if err == nil || called {
		t.Fatal("cancelled execution invoked adapter")
	}
}
func TestRunCLIEnforcesOutputBudget(t *testing.T) {
	plan, adapter := adapterFixture(t, "head -c 1048577 /dev/zero")
	var out bytes.Buffer
	err := RunCLI(context.Background(), []string{"--plan", plan, "--adapter", adapter}, &out)
	if err == nil || !strings.Contains(err.Error(), "byte budget") {
		t.Fatalf("unbounded output accepted/read: %v", err)
	}
}

func TestRunCommandRequiresTrustedHashAndDurableAttempt(t *testing.T) {
	plan, adapter := adapterFixture(t, "printf '{}' ")
	var out bytes.Buffer
	if err := RunCommand([]string{"--plan", plan, "--adapter", adapter}, "", &out); err == nil || out.Len() != 0 {
		t.Fatal("unsafe public invocation accepted")
	}
	record := filepath.Join(t.TempDir(), "attempt.json")
	if err := RunCommand([]string{"--plan", plan, "--adapter", adapter, "--record", record, "--expected-plan-sha256", "wrong"}, "", &out); err == nil {
		t.Fatal("wrong producer identity accepted")
	}
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Fatal("attempt created before producer authentication")
	}
}
func TestRunCLIPersistsPartialReceiptAndRefusesOverwrite(t *testing.T) {
	p := reviewpacket.Build([]reviewpacket.Input{{SourceID: "scope", ADR: adr.ADR{ID: "0040", Decision: "Rule", DiffContext: false}, Files: []string{"a.ts"}, Diff: "diff --git a/a.ts b/a.ts\n@@ -1 +1 @@\n+first\n@@ -9 +9 @@\n+second\n"}}, reviewpacket.Options{MaxTokensPerChunk: 1024})
	plan, adapter := adapterFixture(t, "if test -f \"$0.once\"; then exit 9; fi\ntouch \"$0.once\"\nprintf '{\"findings\":[]}'")
	raw, _ := json.Marshal(p)
	if err := os.WriteFile(plan, raw, 0600); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(t.TempDir(), "attempt.json")
	args := []string{"--plan", plan, "--adapter", adapter, "--record", record, "--expected-plan-sha256", p.PlanSHA256}
	var out bytes.Buffer
	if err := RunCommand(args, "", &out); err == nil {
		t.Fatal("second failure hidden")
	}
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	var run Run
	if err := json.Unmarshal(raw, &run); err != nil {
		t.Fatal(err)
	}
	if !run.Incomplete || len(run.Receipts) != 1 || run.FailedPacketIndex != 2 || run.Receipts[0].PacketSHA256 != reviewpacket.PacketSHA256(p.Packets[0]) {
		t.Fatalf("partial lost: %+v", run)
	}
	before := string(raw)
	out.Reset()
	if err := RunCommand(args, "", &out); err == nil {
		t.Fatal("overwrote prior attempt")
	}
	raw, _ = os.ReadFile(record)
	if string(raw) != before {
		t.Fatal("old attempt changed")
	}
}
func TestRunCLITimeoutBoundsInheritedAdapterPipes(t *testing.T) {
	plan, adapter := adapterFixture(t, "sleep 60 &\nwait")
	var out bytes.Buffer
	started := time.Now()
	err := RunCLI(context.Background(), []string{"--plan", plan, "--adapter", adapter, "--timeout", "50ms"}, &out)
	if err == nil || time.Since(started) > 3*time.Second {
		t.Fatalf("timeout not bounded: %v %s", err, time.Since(started))
	}
}

func TestOpaqueNestedResultDoesNotExpandAttemptPastBudget(t *testing.T) {
	nested := strings.Repeat("[", 2200) + "0" + strings.Repeat("]", 2200)
	run, err := Execute(context.Background(), onePacket(), func(context.Context, reviewpacket.Packet) ([]byte, error) { return []byte(nested), nil })
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	// This models the production encoder, not adapter size. The public encoder
	// must keep opaque nested JSON compact rather than amplifying indentation.
	var out bytes.Buffer
	plan, adapter := adapterFixture(t, "printf '"+nested+"'")
	if err := RunCLI(context.Background(), []string{"--plan", plan, "--adapter", adapter}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() > maxRunBytes {
		t.Fatalf("attempt amplification: raw%d pretty%d actual%d", len(nested), len(raw), out.Len())
	}
}
