package formatter

import (
	"strings"
	"testing"

	"github.com/wbern/adrian/go/internal/adr"
	"github.com/wbern/adrian/go/internal/types"
)

// A pre-filtered ADR was NOT CHECKED. Reporting that as PASS makes an
// unevaluated rule indistinguishable from an enforced one, and any consumer
// counting passes — a merge gate above all — silently over-reports how much
// policy it enforced.
//
// This is not hypothetical. Measured on crm PR #1302: 9 ADRs were pre-filtered
// and 2 of them FAIL when actually checked (ADR-0011 mocks server functions at
// the transport layer; ADR-0059 hides existing state on page load). Both filters
// covered one clause of a multi-clause rule. Those two reported PASS.

func skippableADR() adr.ADR {
	return adr.ADR{
		ID:        "0011",
		Title:     "Testing Strategy",
		Status:    adr.StatusAccepted,
		PreFilter: []string{"waitForTimeout", "test.fixme"},
	}
}

func TestCheckPreFilter_ReportsNotEvaluatedRatherThanPassed(t *testing.T) {
	got := CheckPreFilter(skippableADR(), "+ some unrelated change\n")
	if got == nil {
		t.Fatal("expected a result for an ADR whose pre-filter terms are absent")
	}
	if got.Status == types.StatusPASS {
		t.Error("Status = PASS for a rule that was never checked; an unevaluated rule must not be indistinguishable from an enforced one")
	}
	if got.Status != types.StatusSKIPPED {
		t.Errorf("Status = %v, want SKIPPED", got.Status)
	}
}

func TestCheckPreFilter_ClaimsNoConfidenceInAVerdictItNeverFormed(t *testing.T) {
	got := CheckPreFilter(skippableADR(), "+ some unrelated change\n")
	if got == nil {
		t.Fatal("expected a result")
	}
	if got.Confidence != nil {
		t.Errorf("Confidence = %v; no model ran, so there is no confidence to report", *got.Confidence)
	}
}

func TestCheckPreFilter_ExplanationDoesNotAssertAbsenceOfViolations(t *testing.T) {
	got := CheckPreFilter(skippableADR(), "+ some unrelated change\n")
	if got == nil {
		t.Fatal("expected a result")
	}
	if strings.Contains(strings.ToLower(got.Explanation), "no violations") {
		t.Errorf("explanation asserts there are no violations, which was never established: %q", got.Explanation)
	}
	// It must still say WHY it was skipped, or the skip is unauditable.
	if !strings.Contains(got.Explanation, "waitForTimeout") {
		t.Errorf("explanation does not name the terms that were looked for: %q", got.Explanation)
	}
}

func TestCheckPreFilter_StillRunsTheCheckWhenATermIsPresent(t *testing.T) {
	if got := CheckPreFilter(skippableADR(), "+ await page.waitForTimeout(500)\n"); got != nil {
		t.Errorf("expected nil (proceed to the model) when a pre-filter term is present, got %+v", got)
	}
}

func TestCheckPreFilter_NoPreFilterAlwaysChecks(t *testing.T) {
	a := skippableADR()
	a.PreFilter = nil
	if got := CheckPreFilter(a, "+ anything\n"); got != nil {
		t.Errorf("an ADR with no pre_filter must always be checked, got %+v", got)
	}
}
