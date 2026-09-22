package resultaggregator

import (
	"strings"
	"testing"

	"github.com/wbern/adrian/go/internal/adr"
	"github.com/wbern/adrian/go/internal/types"
)

func mockADR() adr.ADR {
	return adr.ADR{
		ID:          "1",
		Title:       "Test ADR",
		Status:      adr.StatusAccepted,
		AppliesTo:   []string{"**/*.go"},
		Complexity:  adr.ComplexityLite,
		Decision:    "test",
		FilePath:    "/test/adr.md",
		Content:     "test content",
		DiffContext: true,
	}
}

func TestAggregate_UsesLowestConfidence(t *testing.T) {
	a := mockADR()
	high := types.Confidence("high")
	low := types.Confidence("low")
	chunks := []types.LintResult{
		{ADR: a, Status: types.StatusFAIL, Explanation: "High", Confidence: &high},
		{ADR: a, Status: types.StatusFAIL, Explanation: "Low", Confidence: &low},
	}
	got := Aggregate(chunks, a)
	if got.Confidence == nil || *got.Confidence != "low" {
		t.Errorf("confidence = %v, want low", got.Confidence)
	}
}

func TestAggregate_CombinesSuggestions(t *testing.T) {
	a := mockADR()
	s1, s2 := "Fix file1", "Fix file2"
	chunks := []types.LintResult{
		{ADR: a, Status: types.StatusFAIL, Explanation: "Issue 1", Suggestion: &s1},
		{ADR: a, Status: types.StatusFAIL, Explanation: "Issue 2", Suggestion: &s2},
	}
	got := Aggregate(chunks, a)
	if got.Suggestion == nil {
		t.Fatal("suggestion is nil")
	}
	for _, want := range []string{"Fix file1", "Fix file2"} {
		if !strings.Contains(*got.Suggestion, want) {
			t.Errorf("suggestion %q missing %q", *got.Suggestion, want)
		}
	}
}

func TestAggregate_SumsTokenUsage(t *testing.T) {
	a := mockADR()
	c1 := 500
	c2 := 1000
	chunks := []types.LintResult{
		{ADR: a, Status: types.StatusPASS, Explanation: "OK", TokenUsage: &types.TokenUsage{
			PromptTokens: 1000, CompletionTokens: 100, TotalTokens: 1100, CachedTokens: &c1, Model: "claude-haiku-4-5",
		}},
		{ADR: a, Status: types.StatusPASS, Explanation: "OK", TokenUsage: &types.TokenUsage{
			PromptTokens: 2000, CompletionTokens: 200, TotalTokens: 2200, CachedTokens: &c2, Model: "claude-haiku-4-5",
		}},
	}
	got := Aggregate(chunks, a)
	if got.TokenUsage == nil {
		t.Fatal("TokenUsage is nil")
	}
	tu := got.TokenUsage
	if tu.PromptTokens != 3000 || tu.CompletionTokens != 300 || tu.TotalTokens != 3300 {
		t.Errorf("token sums wrong: %+v", *tu)
	}
	if tu.CachedTokens == nil || *tu.CachedTokens != 1500 {
		t.Errorf("CachedTokens = %v, want 1500", tu.CachedTokens)
	}
	if tu.Model != "claude-haiku-4-5" {
		t.Errorf("model = %q", tu.Model)
	}
}

func TestAggregate_ReportsFAILWhenAnyChunkFails(t *testing.T) {
	a := mockADR()
	high := types.Confidence("high")
	chunks := []types.LintResult{
		{ADR: a, Status: types.StatusPASS, Explanation: "No issues", Confidence: &high},
		{ADR: a, Status: types.StatusFAIL, Explanation: "Violation", Locations: []string{"file2.go:10"}, Confidence: &high},
	}

	got := Aggregate(chunks, a)
	if got.Status != types.StatusFAIL {
		t.Errorf("status = %q, want FAIL", got.Status)
	}
	if len(got.Locations) != 1 || got.Locations[0] != "file2.go:10" {
		t.Errorf("locations = %v", got.Locations)
	}
}

func TestAggregate_PrioritizesERROROverFAIL(t *testing.T) {
	a := mockADR()
	chunks := []types.LintResult{
		{ADR: a, Status: types.StatusFAIL, Explanation: "Violation found"},
		{ADR: a, Status: types.StatusERROR, Explanation: "LLM error occurred"},
	}

	got := Aggregate(chunks, a)
	if got.Status != types.StatusERROR {
		t.Errorf("status = %q, want ERROR", got.Status)
	}
	if !strings.Contains(strings.ToLower(got.Explanation), "error") {
		t.Errorf("explanation = %q", got.Explanation)
	}
}

func TestAggregate_PreservesWarnInsteadOfEscalating(t *testing.T) {
	a := mockADR()
	high := types.Confidence("high")
	low := types.Confidence("low")
	chunks := []types.LintResult{
		{ADR: a, Status: types.StatusPASS, Explanation: "No issues", Confidence: &high},
		{ADR: a, Status: types.StatusWARN, Explanation: "Low confidence finding", Confidence: &low},
	}

	got := Aggregate(chunks, a)
	if got.Status != types.StatusWARN {
		t.Errorf("status = %q, want WARN", got.Status)
	}
	if !strings.Contains(got.Explanation, "Low confidence finding") {
		t.Errorf("explanation = %q", got.Explanation)
	}
}

func TestAggregate_AllPassExplanationMentionsChunkCount(t *testing.T) {
	a := mockADR()
	chunks := []types.LintResult{
		{ADR: a, Status: types.StatusPASS, Explanation: "OK"},
		{ADR: a, Status: types.StatusPASS, Explanation: "OK"},
		{ADR: a, Status: types.StatusPASS, Explanation: "OK"},
	}
	got := Aggregate(chunks, a)
	if !strings.Contains(got.Explanation, "3 chunks") {
		t.Errorf("explanation = %q", got.Explanation)
	}
}

func TestAggregate_CombinesLocationsFromFailingChunks(t *testing.T) {
	a := mockADR()
	high := types.Confidence("high")
	chunks := []types.LintResult{
		{ADR: a, Status: types.StatusFAIL, Explanation: "Violation in file1", Locations: []string{"file1.go:10"}, Confidence: &high},
		{ADR: a, Status: types.StatusFAIL, Explanation: "Violation in file2", Locations: []string{"file2.go:20"}, Confidence: &high},
	}

	got := Aggregate(chunks, a)
	if got.Status != types.StatusFAIL {
		t.Errorf("status = %q, want FAIL", got.Status)
	}
	wantLocs := []string{"file1.go:10", "file2.go:20"}
	if len(got.Locations) != 2 || got.Locations[0] != wantLocs[0] || got.Locations[1] != wantLocs[1] {
		t.Errorf("locations = %v, want %v", got.Locations, wantLocs)
	}
	for _, want := range []string{"2 chunks", "file1.go:10", "file2.go:20"} {
		if !strings.Contains(got.Explanation, want) {
			t.Errorf("explanation %q missing %q", got.Explanation, want)
		}
	}
}

func TestAggregate_SingleChunkPassthrough(t *testing.T) {
	a := mockADR()
	conf := types.Confidence("high")
	single := types.LintResult{
		ADR:         a,
		Status:      types.StatusPASS,
		Explanation: "No violations found",
		Confidence:  &conf,
	}

	got := Aggregate([]types.LintResult{single}, a)
	if got.Status != types.StatusPASS {
		t.Errorf("status = %q, want PASS", got.Status)
	}
	if got.Explanation != "No violations found" {
		t.Errorf("explanation = %q", got.Explanation)
	}
}

func TestAggregate_EmptyChunksReturnsError(t *testing.T) {
	got := Aggregate(nil, mockADR())
	if got.Status != types.StatusERROR {
		t.Errorf("status = %q, want ERROR", got.Status)
	}
	if !strings.Contains(got.Explanation, "No chunks") {
		t.Errorf("explanation = %q", got.Explanation)
	}
}
