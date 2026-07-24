package planexec

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/wbern/adr-lint/go/internal/adr"
	"github.com/wbern/adr-lint/go/internal/reviewpacket"
)

func TestExecuteInvokesPacketsSequentiallyInPlanOrder(t *testing.T) {
	plan := reviewpacket.Build([]reviewpacket.Input{{
		ADR: adr.ADR{
			ID: "37", Title: "Tokens", Complexity: adr.ComplexityLite,
			Decision: "Use tokens.", PreFilter: []string{"token"}, DiffContext: false,
		},
		Files: []string{"src/theme.css"},
		Diff: `diff --git a/src/theme.css b/src/theme.css
index 1111111..2222222 100644
--- a/src/theme.css
+++ b/src/theme.css
@@ -1 +1 @@
+color: token-one;
@@ -10 +10 @@
+color: token-two;
`,
	}}, reviewpacket.Options{MaxTokensPerChunk: 512})

	var called []int
	run, err := Execute(context.Background(), plan, func(_ context.Context, packet reviewpacket.Packet) ([]byte, error) {
		called = append(called, packet.ChunkIndex)
		return []byte(`{"verdict":"ok"}`), nil
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(called) != 2 || called[0] != 1 || called[1] != 2 {
		t.Errorf("called = %v, want [1 2]", called)
	}
	if len(run.Receipts) != 2 {
		t.Errorf("receipts = %d, want 2", len(run.Receipts))
	}
}

func TestRunCLIExecutesAProviderNeutralAdapter(t *testing.T) {
	plan := reviewpacket.Build([]reviewpacket.Input{{
		ADR: adr.ADR{
			ID: "37", Title: "Tokens", Complexity: adr.ComplexityLite,
			Decision: "Use tokens.", PreFilter: []string{"token"},
		},
		Files: []string{"src/theme.css"},
		Diff:  "diff --git a/src/theme.css b/src/theme.css\n@@ -1 +1 @@\n+color: token;\n",
	}}, reviewpacket.Options{MaxTokensPerChunk: 512})

	dir := t.TempDir()
	planPath := filepath.Join(dir, "plan.json")
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath, planJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	adapterPath := filepath.Join(dir, "adapter.sh")
	if err := os.WriteFile(adapterPath, []byte("#!/bin/sh\ncat >/dev/null\nprintf '{\"verdict\":\"ok\"}'\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err = RunCLI(context.Background(), []string{"--plan", planPath, "--adapter", adapterPath}, &out)
	if err != nil {
		t.Fatalf("RunCLI: %v", err)
	}
	var run Run
	if err := json.Unmarshal(out.Bytes(), &run); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(run.Receipts) != 1 || !json.Valid(run.Receipts[0].Result) {
		t.Errorf("run = %#v", run)
	}
}

func TestExecuteRefusesAnOverLimitPlanBeforeInvokingAdapter(t *testing.T) {
	plan := reviewpacket.Build([]reviewpacket.Input{{
		ADR: adr.ADR{
			ID: "37", Title: "Tokens", Complexity: adr.ComplexityLite,
			Decision: "Use tokens.", PreFilter: []string{"token"}, DiffContext: false,
		},
		Files: []string{"src/theme.css"},
		Diff: `diff --git a/src/theme.css b/src/theme.css
@@ -1 +1 @@
+color: token-one;
@@ -10 +10 @@
+color: token-two;
`,
	}}, reviewpacket.Options{MaxTokensPerChunk: 512, MaxPackets: 1})

	called := false
	_, err := Execute(context.Background(), plan, func(_ context.Context, _ reviewpacket.Packet) ([]byte, error) {
		called = true
		return []byte(`{"verdict":"ok"}`), nil
	})
	if err == nil {
		t.Fatal("Execute should reject an over-limit plan")
	}
	if called {
		t.Error("adapter should not be invoked for an over-limit plan")
	}
}

func TestExecuteRejectsAPlanWhosePacketScopeChangedAfterPlanning(t *testing.T) {
	plan := reviewpacket.Build([]reviewpacket.Input{{
		ADR: adr.ADR{
			ID: "37", Title: "Tokens", Complexity: adr.ComplexityLite,
			Decision: "Use tokens.", PreFilter: []string{"token"},
		},
		Files: []string{"src/theme.css"},
		Diff:  "diff --git a/src/theme.css b/src/theme.css\n@@ -1 +1 @@\n+color: token;\n",
	}}, reviewpacket.Options{MaxTokensPerChunk: 512})
	plan.Packets[0].Diff = "tampered"

	called := false
	_, err := Execute(context.Background(), plan, func(_ context.Context, _ reviewpacket.Packet) ([]byte, error) {
		called = true
		return []byte(`{"verdict":"ok"}`), nil
	})
	if err == nil {
		t.Fatal("Execute should reject a changed plan")
	}
	if called {
		t.Error("adapter should not be invoked for a changed plan")
	}
}
