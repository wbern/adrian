package formatter

import (
	"strings"
	"testing"

	"github.com/wbern/adrian/go/internal/adr"
	"github.com/wbern/adrian/go/internal/types"
)

func mockADR() adr.ADR {
	return adr.ADR{
		ID:          "0001",
		Title:       "ADR Structure and Format",
		Status:      adr.StatusAccepted,
		AppliesTo:   []string{"doc/adr/*.md"},
		Complexity:  adr.ComplexityLite,
		Decision:    "Check structure",
		FilePath:    "doc/adr/0001-adr-structure.md",
		Content:     "",
		DiffContext: true,
	}
}

func TestFormatResultHeading_IncludesTokenCount(t *testing.T) {
	r := types.LintResult{
		ADR:         mockADR(),
		Status:      types.StatusPASS,
		Explanation: "All good",
		TokenUsage: &types.TokenUsage{
			PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 18432,
			Model: "haiku",
		},
	}

	got := FormatResultHeading(r)
	want := "✅ ADR Structure and Format (18,432 tokens)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatResultHeading_OmitsTokenCountWhenAbsent(t *testing.T) {
	r := types.LintResult{ADR: mockADR(), Status: types.StatusPASS, Explanation: "All good"}
	got := FormatResultHeading(r)
	want := "✅ ADR Structure and Format"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatResultHeading_LargeTokenCountsHaveCommas(t *testing.T) {
	r := types.LintResult{
		ADR:         mockADR(),
		Status:      types.StatusFAIL,
		Explanation: "Violation found",
		TokenUsage: &types.TokenUsage{
			PromptTokens: 100000, CompletionTokens: 30920, TotalTokens: 130920,
			Model: "claude-opus-4-6",
		},
	}
	got := FormatResultHeading(r)
	want := "❌ ADR Structure and Format (130,920 tokens)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatResultHeading_SkippedIcon(t *testing.T) {
	r := types.LintResult{
		ADR:         mockADR(),
		Status:      types.StatusSKIPPED,
		Explanation: "Pre-filter not matched",
	}
	got := FormatResultHeading(r)
	want := "⏭️ ADR Structure and Format"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatTokenStats_NoCached(t *testing.T) {
	results := []types.LintResult{
		{ADR: mockADR(), Status: types.StatusPASS, Explanation: "OK", TokenUsage: &types.TokenUsage{
			PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500, Model: "x",
		}},
		{ADR: mockADR(), Status: types.StatusFAIL, Explanation: "Violation", TokenUsage: &types.TokenUsage{
			PromptTokens: 2000, CompletionTokens: 800, TotalTokens: 2800, Model: "x",
		}},
	}
	got := FormatTokenStats(results)
	want := "Tokens: 4,300 total (3,000 input, 1,300 output)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatTokenStats_WithCached(t *testing.T) {
	c1, c2 := 300, 600
	results := []types.LintResult{
		{ADR: mockADR(), Status: types.StatusPASS, Explanation: "OK", TokenUsage: &types.TokenUsage{
			PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500, CachedTokens: &c1, Model: "x",
		}},
		{ADR: mockADR(), Status: types.StatusFAIL, Explanation: "V", TokenUsage: &types.TokenUsage{
			PromptTokens: 2000, CompletionTokens: 800, TotalTokens: 2800, CachedTokens: &c2, Model: "x",
		}},
	}
	got := FormatTokenStats(results)
	want := "Tokens: 4,300 total (3,000 input, 1,300 output) - 900 cached (21% savings)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCheckPreFilter_NoPreFilter(t *testing.T) {
	if got := CheckPreFilter(mockADR(), "+ some code"); got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

func TestCheckPreFilter_PatternMatches(t *testing.T) {
	a := mockADR()
	a.PreFilter = []string{"gomock"}
	if got := CheckPreFilter(a, `+ import "github.com/golang/mock/gomock";`); got != nil {
		t.Errorf("expected nil (match found), got %+v", got)
	}
}

// Renamed from ...ReturnsPASS: a pre-filtered rule is SKIPPED, not passed.
// It was never checked, so it must not be counted as enforced (gci-0pq6w.9).
func TestCheckPreFilter_NoMatchReturnsSKIPPED(t *testing.T) {
	a := mockADR()
	a.PreFilter = []string{"gomock"}
	got := CheckPreFilter(a, `+ import "github.com/stretchr/testify/assert";`)
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if got.Status != types.StatusSKIPPED {
		t.Errorf("status = %q, want SKIPPED", got.Status)
	}
	if !strings.Contains(got.Explanation, "pre-filter") {
		t.Errorf("explanation missing 'pre-filter': %q", got.Explanation)
	}
	if !strings.Contains(got.Explanation, `"gomock"`) {
		t.Errorf(`explanation missing "gomock": %q`, got.Explanation)
	}
}

func TestCheckPreFilter_ArrayPatternsORLogic(t *testing.T) {
	a := mockADR()
	a.PreFilter = []string{"as any", ": any"}
	if got := CheckPreFilter(a, "+ const x: any = 5;"); got != nil {
		t.Errorf("expected nil (one pattern matches), got %+v", got)
	}
}

func TestCheckPreFilter_ArrayPatternsNoMatch(t *testing.T) {
	a := mockADR()
	a.PreFilter = []string{"as any", ": any"}
	got := CheckPreFilter(a, "+ const x: string = 'hello';")
	if got == nil {
		t.Fatal("expected non-nil")
	}
	if got.Status != types.StatusSKIPPED {
		t.Errorf("status = %q, want SKIPPED", got.Status)
	}
	if !strings.Contains(got.Explanation, `"as any" or ": any"`) {
		t.Errorf("explanation = %q", got.Explanation)
	}
}

var _ = strings.Contains
