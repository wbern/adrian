package reviewpacket

import (
	"strings"
	"testing"

	"github.com/wbern/adr-lint/go/internal/adr"
)

func TestBuildPreFilterKeepsOnlyMatchingHunk(t *testing.T) {
	diff := `diff --git a/src/theme.css b/src/theme.css
index 1111111..2222222 100644
--- a/src/theme.css
+++ b/src/theme.css
@@ -1,2 +1,2 @@
-color: var(--color-brand-700);
+color: var(--color-brand-800);
@@ -10,2 +10,2 @@
-background: var(--color-surface);
+background: var(--color-danger);
`

	plan := Build([]Input{{
		ADR: adr.ADR{
			ID:         "37",
			Title:      "Contrast tokens",
			Complexity: adr.ComplexityLite,
			Decision:   "Use a readable foreground token.",
			PreFilter:  []string{"color-brand"},
		},
		Files: []string{"src/theme.css"},
		Diff:  diff,
	}}, Options{MaxTokensPerChunk: 256})

	if len(plan.Packets) != 1 {
		t.Fatalf("packets = %d, want 1", len(plan.Packets))
	}
	packet := plan.Packets[0]
	if !strings.Contains(packet.Diff, "color-brand-800") {
		t.Errorf("packet omitted matching hunk: %q", packet.Diff)
	}
	if strings.Contains(packet.Diff, "color-danger") {
		t.Errorf("packet retained unrelated hunk: %q", packet.Diff)
	}
	if got, want := packet.PreFilterHits, []string{"color-brand"}; !sameStrings(got, want) {
		t.Errorf("preFilterHits = %v, want %v", got, want)
	}
}

func TestBuildSplitsFilesAtConfiguredTokenBudget(t *testing.T) {
	diff := `diff --git a/src/one.css b/src/one.css
index 1111111..2222222 100644
--- a/src/one.css
+++ b/src/one.css
@@ -1 +1 @@
-color: old;
+color: brand;
diff --git a/src/two.css b/src/two.css
index 3333333..4444444 100644
--- a/src/two.css
+++ b/src/two.css
@@ -1 +1 @@
-color: old;
+color: danger;
`

	plan := Build([]Input{{
		ADR:   adr.ADR{ID: "37", Title: "Tokens", Complexity: adr.ComplexityLite, Decision: "Use tokens."},
		Files: []string{"src/one.css", "src/two.css"},
		Diff:  diff,
	}}, Options{MaxTokensPerChunk: 60})

	if len(plan.Packets) != 2 {
		t.Fatalf("packets = %d, want 2", len(plan.Packets))
	}
	if plan.Packets[0].ChunkIndex != 1 || plan.Packets[0].ChunkCount != 2 {
		t.Errorf("first chunk = %d/%d, want 1/2", plan.Packets[0].ChunkIndex, plan.Packets[0].ChunkCount)
	}
	if plan.Packets[1].ChunkIndex != 2 || plan.Packets[1].ChunkCount != 2 {
		t.Errorf("second chunk = %d/%d, want 2/2", plan.Packets[1].ChunkIndex, plan.Packets[1].ChunkCount)
	}
	if strings.Contains(plan.Packets[0].Diff, "two.css") || strings.Contains(plan.Packets[1].Diff, "one.css") {
		t.Errorf("packets should preserve file boundaries: %#v", plan.Packets)
	}
	if got, want := plan.Packets[0].Files, []string{"src/one.css"}; !sameStrings(got, want) {
		t.Errorf("first packet files = %v, want %v", got, want)
	}
	if got, want := plan.Packets[1].Files, []string{"src/two.css"}; !sameStrings(got, want) {
		t.Errorf("second packet files = %v, want %v", got, want)
	}
}

func TestBuildSplitsOversizedHunkWithoutDroppingLines(t *testing.T) {
	diff := `diff --git a/src/theme.css b/src/theme.css
index 1111111..2222222 100644
--- a/src/theme.css
+++ b/src/theme.css
@@ -1,6 +1,6 @@
+color: token-one;
+color: token-two;
+color: token-three;
+color: token-four;
+color: token-five;
+color: token-six;
`

	plan := Build([]Input{{
		ADR:   adr.ADR{ID: "37", Title: "Tokens", Complexity: adr.ComplexityLite, Decision: "Use tokens."},
		Files: []string{"src/theme.css"},
		Diff:  diff,
	}}, Options{MaxTokensPerChunk: 45})

	if len(plan.Packets) < 2 {
		t.Fatalf("packets = %d, want an oversized hunk to split", len(plan.Packets))
	}
	joined := ""
	for _, packet := range plan.Packets {
		if packet.EstimatedTokens > 45 {
			t.Errorf("packet estimated at %d tokens, exceeds budget", packet.EstimatedTokens)
		}
		if !strings.Contains(packet.Diff, "@@ -1,6 +1,6 @@") {
			t.Errorf("split packet lost its hunk location: %q", packet.Diff)
		}
		joined += packet.Diff
	}
	for _, line := range []string{"token-one", "token-two", "token-three", "token-four", "token-five", "token-six"} {
		if strings.Count(joined, line) != 1 {
			t.Errorf("%s appears %d times, want exactly once", line, strings.Count(joined, line))
		}
	}
}

func TestBuildCanRequirePreFilterForReviewablePackets(t *testing.T) {
	plan := Build([]Input{{
		ADR:   adr.ADR{ID: "37", Title: "Broad CSS rule", Complexity: adr.ComplexityLite, Decision: "Use tokens."},
		Files: []string{"src/theme.css"},
		Diff:  "diff --git a/src/theme.css b/src/theme.css\n@@ -1 +1 @@\n+color: brand;\n",
	}}, Options{MaxTokensPerChunk: 256, RequirePreFilter: true})

	if len(plan.Packets) != 0 {
		t.Errorf("packets = %d, want 0", len(plan.Packets))
	}
	if len(plan.Skipped) != 1 || plan.Skipped[0].Reason != "pre_filter_required" {
		t.Errorf("skipped = %#v, want pre_filter_required", plan.Skipped)
	}
}

func TestBuildWithoutDiffContextKeepsMatchingHunksSeparate(t *testing.T) {
	diff := `diff --git a/src/theme.css b/src/theme.css
index 1111111..2222222 100644
--- a/src/theme.css
+++ b/src/theme.css
@@ -1 +1 @@
+color: token-one;
@@ -10 +10 @@
+color: token-two;
`

	plan := Build([]Input{{
		ADR: adr.ADR{
			ID: "37", Title: "Tokens", Complexity: adr.ComplexityLite,
			Decision: "Use tokens.", PreFilter: []string{"token"}, DiffContext: false,
		},
		Files: []string{"src/theme.css"},
		Diff:  diff,
	}}, Options{MaxTokensPerChunk: 512})

	if len(plan.Packets) != 2 {
		t.Fatalf("packets = %d, want 2", len(plan.Packets))
	}
	if !strings.Contains(plan.Packets[0].Diff, "token-one") || strings.Contains(plan.Packets[0].Diff, "token-two") {
		t.Errorf("first packet does not isolate first hunk: %q", plan.Packets[0].Diff)
	}
	if !strings.Contains(plan.Packets[1].Diff, "token-two") || strings.Contains(plan.Packets[1].Diff, "token-one") {
		t.Errorf("second packet does not isolate second hunk: %q", plan.Packets[1].Diff)
	}
}

func TestBuildCountsPolicyAgainstPacketBudget(t *testing.T) {
	diff := `diff --git a/src/theme.css b/src/theme.css
index 1111111..2222222 100644
--- a/src/theme.css
+++ b/src/theme.css
@@ -1,4 +1,4 @@
+color: token-one;
+color: token-two;
+color: token-three;
+color: token-four;
`
	plan := Build([]Input{{
		ADR: adr.ADR{
			ID: "37", Title: "Tokens", Complexity: adr.ComplexityLite,
			Decision:  strings.Repeat("Use semantic color tokens. ", 8),
			PreFilter: []string{"token"}, DiffContext: true,
		},
		Files: []string{"src/theme.css"},
		Diff:  diff,
	}}, Options{MaxTokensPerChunk: 100})

	if len(plan.Packets) < 2 {
		t.Fatalf("packets = %d, want policy cost to force a split", len(plan.Packets))
	}
	for _, packet := range plan.Packets {
		if packet.EstimatedTokens > 100 {
			t.Errorf("packet estimated at %d tokens, exceeds budget", packet.EstimatedTokens)
		}
	}
}

func TestBuildSkipsWhenPolicyAloneExceedsBudget(t *testing.T) {
	plan := Build([]Input{{
		ADR: adr.ADR{
			ID: "37", Title: "Tokens", Complexity: adr.ComplexityLite,
			Decision:  strings.Repeat("Use semantic color tokens. ", 20),
			PreFilter: []string{"token"},
		},
		Files: []string{"src/theme.css"},
		Diff:  "diff --git a/src/theme.css b/src/theme.css\n@@ -1 +1 @@\n+color: token;\n",
	}}, Options{MaxTokensPerChunk: 20})

	if len(plan.Packets) != 0 {
		t.Errorf("packets = %d, want 0", len(plan.Packets))
	}
	if len(plan.Skipped) != 1 || plan.Skipped[0].Reason != "policy_exceeds_token_budget" {
		t.Errorf("skipped = %#v, want policy_exceeds_token_budget", plan.Skipped)
	}
}

func TestBuildSkipsWhenOneHunkCannotFitRemainingBudget(t *testing.T) {
	plan := Build([]Input{{
		ADR: adr.ADR{
			ID: "37", Title: "Tokens", Complexity: adr.ComplexityLite,
			Decision:  strings.Repeat("Use semantic color tokens. ", 10),
			PreFilter: []string{"token"},
		},
		Files: []string{"src/theme.css"},
		Diff:  "diff --git a/src/theme.css b/src/theme.css\n--- a/src/theme.css\n+++ b/src/theme.css\n@@ -1 +1 @@\n+color: token;\n",
	}}, Options{MaxTokensPerChunk: 90})

	if len(plan.Packets) != 0 {
		t.Errorf("packets = %d, want 0", len(plan.Packets))
	}
	if len(plan.Skipped) != 1 || plan.Skipped[0].Reason != "diff_exceeds_token_budget" {
		t.Errorf("skipped = %#v, want diff_exceeds_token_budget", plan.Skipped)
	}
}

func TestBuildReportsWhenPacketCountExceedsConfiguredLimit(t *testing.T) {
	diff := `diff --git a/src/theme.css b/src/theme.css
index 1111111..2222222 100644
--- a/src/theme.css
+++ b/src/theme.css
@@ -1 +1 @@
+color: token-one;
@@ -10 +10 @@
+color: token-two;
`
	plan := Build([]Input{{
		ADR: adr.ADR{
			ID: "37", Title: "Tokens", Complexity: adr.ComplexityLite,
			Decision: "Use tokens.", PreFilter: []string{"token"}, DiffContext: false,
		},
		Files: []string{"src/theme.css"},
		Diff:  diff,
	}}, Options{MaxTokensPerChunk: 512, MaxPackets: 1})

	if len(plan.Packets) != 2 {
		t.Fatalf("packets = %d, want 2", len(plan.Packets))
	}
	if !plan.Limits.PacketLimitExceeded {
		t.Error("PacketLimitExceeded should be true")
	}
	if plan.Limits.MaxPackets != 1 {
		t.Errorf("MaxPackets = %d, want 1", plan.Limits.MaxPackets)
	}
}

func TestBuildAssignsStablePlanHash(t *testing.T) {
	input := []Input{{
		ADR: adr.ADR{
			ID: "37", Title: "Tokens", Complexity: adr.ComplexityLite,
			Decision: "Use tokens.", PreFilter: []string{"token"},
		},
		Files: []string{"src/theme.css"},
		Diff:  "diff --git a/src/theme.css b/src/theme.css\n@@ -1 +1 @@\n+color: token;\n",
	}}

	first := Build(input, Options{MaxTokensPerChunk: 512})
	second := Build(input, Options{MaxTokensPerChunk: 512})
	if first.PlanSHA256 == "" {
		t.Fatal("PlanSHA256 should be populated")
	}
	if first.PlanSHA256 != second.PlanSHA256 {
		t.Errorf("plan hashes differ: %q != %q", first.PlanSHA256, second.PlanSHA256)
	}
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
