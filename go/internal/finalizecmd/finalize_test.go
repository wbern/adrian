package finalizecmd_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wbern/adrian/go/internal/finalizecmd"
)

func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal json: %v", err)
	}
	if err := os.WriteFile(path, b, 0644); err != nil {
		t.Fatalf("failed to write json file %s: %v", path, err)
	}
}

func TestFinalize_PlanSelfConsistency(t *testing.T) {
	tmp := t.TempDir()
	planFile := filepath.Join(tmp, "plan.json")
	bodyFile := filepath.Join(tmp, "body.md")
	coveredFile := filepath.Join(tmp, "covered.json")

	// Inconsistent: segments total bytes is 100, but baseline says 200
	plan := map[string]any{
		"head":    "abcd123",
		"diff_id": "diff999",
		"coverage_baseline": map[string]any{
			"segments": 1,
			"bytes":    200,
		},
		"segments": []map[string]any{
			{
				"segment": 1,
				"bytes":   100,
				"paths":   []string{"src/foo.ts"},
			},
		},
	}
	writeJSONFile(t, planFile, plan)
	if err := os.WriteFile(bodyFile, []byte("verdict: approve\n"), 0644); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, coveredFile, []int{1})

	var out, errOut bytes.Buffer
	err := finalizecmd.Run([]string{
		"--plan", planFile,
		"--body-file", bodyFile,
		"--segments-covered", coveredFile,
	}, &out, &errOut)

	if err == nil {
		t.Fatal("expected error on internally inconsistent plan, got nil")
	}
	if !strings.Contains(err.Error(), "coverage_baseline does not match its own segments") {
		t.Errorf("expected inconsistency error, got: %v", err)
	}
}

func TestFinalize_NonNumericSegmentsRefusal(t *testing.T) {
	tmp := t.TempDir()
	planFile := filepath.Join(tmp, "plan.json")
	bodyFile := filepath.Join(tmp, "body.md")
	coveredFile := filepath.Join(tmp, "covered.json")

	plan := map[string]any{
		"head":    "abcd123",
		"diff_id": "diff999",
		"coverage_baseline": map[string]any{
			"segments": 1,
			"bytes":    100,
		},
		"segments": []map[string]any{
			{
				"segment": 1,
				"bytes":   100,
				"paths":   []string{"src/foo.ts"},
			},
		},
	}
	writeJSONFile(t, planFile, plan)
	if err := os.WriteFile(bodyFile, []byte("verdict: approve\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Pass string array ["1"] instead of numbers [1]
	writeJSONFile(t, coveredFile, []string{"1"})

	var out, errOut bytes.Buffer
	err := finalizecmd.Run([]string{
		"--plan", planFile,
		"--body-file", bodyFile,
		"--segments-covered", coveredFile,
	}, &out, &errOut)

	if err == nil {
		t.Fatal("expected error on non-numeric segments, got nil")
	}
	if !strings.Contains(err.Error(), "must be a JSON array of segment NUMBERS") {
		t.Errorf("expected NUMBERS error, got: %v", err)
	}
}

func TestFinalize_CleanApprove(t *testing.T) {
	tmp := t.TempDir()
	planFile := filepath.Join(tmp, "plan.json")
	bodyFile := filepath.Join(tmp, "body.md")
	coveredFile := filepath.Join(tmp, "covered.json")

	isComplete := true
	plan := map[string]any{
		"head":    "0123456789abcdef",
		"diff_id": "feedbeef",
		"binding": []string{"ADR-0021", "ADR-0032"},
		"coverage_baseline": map[string]any{
			"segments": 2,
			"bytes":    2500,
		},
		"segments": []map[string]any{
			{
				"segment": 1,
				"bytes":   1000,
				"paths":   []string{"src/a.ts"},
			},
			{
				"segment": 2,
				"bytes":   1500,
				"paths":   []string{"src/b.ts"},
			},
		},
		"evidence_packet": map[string]any{
			"complete": isComplete,
		},
		"corpus": map[string]any{
			"state":      "current",
			"behind":     0,
			"ahead":      0,
			"dirty_adrs": 0,
		},
	}
	writeJSONFile(t, planFile, plan)
	if err := os.WriteFile(bodyFile, []byte("## Review\n\nverdict: approve\nLooks good!"), 0644); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, coveredFile, []int{1, 2})

	var out, errOut bytes.Buffer
	err := finalizecmd.Run([]string{
		"--plan", planFile,
		"--body-file", bodyFile,
		"--segments-covered", coveredFile,
	}, &out, &errOut)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.TrimSpace(out.String()) != "approve" {
		t.Errorf("expected stdout 'approve', got %q", out.String())
	}

	finalContent, err := os.ReadFile(bodyFile)
	if err != nil {
		t.Fatal(err)
	}
	finalBody := string(finalContent)

	// Check pr-review-cost marker
	if !strings.Contains(finalBody, "<!-- pr-review-cost:v1 head=0123456789abcdef diff=feedbeef adr_scope=ADR-0021,ADR-0032 adr_bytes=na tokens_total=na tokens_in=na tokens_out=na payload_bytes=na reviewed_bytes=2500 diff_bytes=2500 passes=2 schema=1 -->") {
		t.Errorf("missing or incorrect pr-review-cost marker in body:\n%s", finalBody)
	}

	// Check coverage ledger
	if !strings.Contains(finalBody, "<details>\n<summary>Coverage ledger</summary>\n\ncoverage ledger: 2/2 segments; 2500/2500 bytes.\n\n</details>") {
		t.Errorf("missing coverage ledger in body:\n%s", finalBody)
	}

	// Check pr-review-corpus marker
	if !strings.Contains(finalBody, "<!-- pr-review-corpus:v1 head=0123456789abcdef state=current behind=0 ahead=0 dirty_adrs=0 schema=1 -->") {
		t.Errorf("missing pr-review-corpus marker in body:\n%s", finalBody)
	}

	// Must NOT contain escalated blocker
	if strings.Contains(finalBody, "verdict: request_changes (ESCALATED)") {
		t.Errorf("clean approve should not contain escalated refusal:\n%s", finalBody)
	}
}

func TestFinalize_IncompleteCoverageEscalation(t *testing.T) {
	tmp := t.TempDir()
	planFile := filepath.Join(tmp, "plan.json")
	bodyFile := filepath.Join(tmp, "body.md")
	coveredFile := filepath.Join(tmp, "covered.json")

	isComplete := true
	plan := map[string]any{
		"head":    "0123456789abcdef",
		"diff_id": "feedbeef",
		"binding": []string{"ADR-0021"},
		"coverage_baseline": map[string]any{
			"segments": 2,
			"bytes":    2500,
		},
		"segments": []map[string]any{
			{
				"segment": 1,
				"bytes":   1000,
				"paths":   []string{"src/a.ts"},
			},
			{
				"segment": 2,
				"bytes":   1500,
				"paths":   []string{"src/b.ts"},
			},
		},
		"evidence_packet": map[string]any{
			"complete": isComplete,
		},
		"corpus": map[string]any{
			"state":      "current",
			"behind":     0,
			"ahead":      0,
			"dirty_adrs": 0,
		},
	}
	writeJSONFile(t, planFile, plan)
	if err := os.WriteFile(bodyFile, []byte("verdict: approve\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Only segment 1 covered, segment 2 missing!
	writeJSONFile(t, coveredFile, []int{1})

	var out, errOut bytes.Buffer
	err := finalizecmd.Run([]string{
		"--plan", planFile,
		"--body-file", bodyFile,
		"--segments-covered", coveredFile,
	}, &out, &errOut)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.TrimSpace(out.String()) != "request_changes" {
		t.Errorf("expected stdout 'request_changes', got %q", out.String())
	}

	finalContent, err := os.ReadFile(bodyFile)
	if err != nil {
		t.Fatal(err)
	}
	finalBody := string(finalContent)

	if !strings.Contains(finalBody, "verdict: request_changes (ESCALATED)") {
		t.Errorf("expected escalated verdict in body:\n%s", finalBody)
	}
	if !strings.Contains(finalBody, "Never judged: src/b.ts") {
		t.Errorf("expected missing path src/b.ts in escalation body:\n%s", finalBody)
	}
	if !strings.Contains(finalBody, "Planned segments [1,2]; the reviewer accounted for [1]; 1000 of 2500 bytes were judged.") {
		t.Errorf("expected segment/byte accounting in escalation body:\n%s", finalBody)
	}
}

func TestFinalize_CorpusNotCurrentEscalation(t *testing.T) {
	tmp := t.TempDir()
	planFile := filepath.Join(tmp, "plan.json")
	bodyFile := filepath.Join(tmp, "body.md")
	coveredFile := filepath.Join(tmp, "covered.json")

	isComplete := true
	plan := map[string]any{
		"head":    "0123456789abcdef",
		"diff_id": "feedbeef",
		"coverage_baseline": map[string]any{
			"segments": 1,
			"bytes":    1000,
		},
		"segments": []map[string]any{
			{
				"segment": 1,
				"bytes":   1000,
				"paths":   []string{"src/a.ts"},
			},
		},
		"evidence_packet": map[string]any{
			"complete": isComplete,
		},
		"corpus": map[string]any{
			"state":      "behind",
			"behind":     5,
			"ahead":      0,
			"dirty_adrs": 1,
		},
	}
	writeJSONFile(t, planFile, plan)
	if err := os.WriteFile(bodyFile, []byte("verdict: approve\n"), 0644); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, coveredFile, []int{1})

	var out, errOut bytes.Buffer
	err := finalizecmd.Run([]string{
		"--plan", planFile,
		"--body-file", bodyFile,
		"--segments-covered", coveredFile,
	}, &out, &errOut)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.TrimSpace(out.String()) != "request_changes" {
		t.Errorf("expected stdout 'request_changes', got %q", out.String())
	}

	finalContent, err := os.ReadFile(bodyFile)
	if err != nil {
		t.Fatal(err)
	}
	finalBody := string(finalContent)

	if !strings.Contains(finalBody, "verdict: request_changes (ESCALATED)") {
		t.Errorf("expected escalated verdict in body:\n%s", finalBody)
	}
	if !strings.Contains(finalBody, "the ADR corpus this review enforced was read from") {
		t.Errorf("expected corpus refusal blocker in body:\n%s", finalBody)
	}
}

func TestFinalize_HedgedVerdict(t *testing.T) {
	tmp := t.TempDir()
	planFile := filepath.Join(tmp, "plan.json")
	bodyFile := filepath.Join(tmp, "body.md")
	coveredFile := filepath.Join(tmp, "covered.json")

	isComplete := true
	plan := map[string]any{
		"head":    "0123456789abcdef",
		"diff_id": "feedbeef",
		"coverage_baseline": map[string]any{
			"segments": 1,
			"bytes":    1000,
		},
		"segments": []map[string]any{
			{
				"segment": 1,
				"bytes":   1000,
				"paths":   []string{"src/a.ts"},
			},
		},
		"evidence_packet": map[string]any{
			"complete": isComplete,
		},
		"corpus": map[string]any{
			"state":      "current",
			"behind":     0,
			"ahead":      0,
			"dirty_adrs": 0,
		},
	}
	writeJSONFile(t, planFile, plan)
	// Hedged verdict
	if err := os.WriteFile(bodyFile, []byte("verdict: approve pending test fix\n"), 0644); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, coveredFile, []int{1})

	var out, errOut bytes.Buffer
	err := finalizecmd.Run([]string{
		"--plan", planFile,
		"--body-file", bodyFile,
		"--segments-covered", coveredFile,
	}, &out, &errOut)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.TrimSpace(out.String()) != "request_changes" {
		t.Errorf("expected stdout 'request_changes' for hedged verdict, got %q", out.String())
	}
}
