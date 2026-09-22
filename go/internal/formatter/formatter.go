// Package formatter renders LintResults for human consumption and
// implements the pre-filter short-circuit.
package formatter

import (
	"fmt"
	"strings"

	"github.com/wbern/adrian/go/internal/adr"
	"github.com/wbern/adrian/go/internal/types"
)

// StatusIcons maps each ResultStatus to a single display glyph.
var StatusIcons = map[types.ResultStatus]string{
	types.StatusPASS:    "✅",
	types.StatusFAIL:    "❌",
	types.StatusWARN:    "⚠️",
	types.StatusERROR:   "🔴",
	types.StatusSKIPPED: "⏭️",
}

// FormatResultHeading renders the headline for a LintResult.
func FormatResultHeading(r types.LintResult) string {
	isCachedPass := r.Cached && r.Status == types.StatusPASS
	icon := StatusIcons[r.Status]
	spacing := " "
	if isCachedPass {
		icon = "☑️"
		spacing = "  " // ballot-box emoji renders narrower; pad for alignment
	}

	var tokenInfo string
	switch {
	case r.TokenUsage != nil && r.Cached:
		tokenInfo = fmt.Sprintf(" (%s tokens, cached)", formatThousands(r.TokenUsage.TotalTokens))
	case r.TokenUsage != nil:
		tokenInfo = fmt.Sprintf(" (%s tokens)", formatThousands(r.TokenUsage.TotalTokens))
	case r.Cached:
		tokenInfo = " (cached)"
	}

	return icon + spacing + r.ADR.Title + tokenInfo
}

// formatThousands renders n with groups of three digits separated by
// commas (e.g. 1234567 -> "1,234,567").
func formatThousands(n int) string {
	negative := n < 0
	if negative {
		n = -n
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		if negative {
			return "-" + s
		}
		return s
	}
	var b strings.Builder
	first := len(s) % 3
	if first == 0 {
		first = 3
	}
	b.WriteString(s[:first])
	for i := first; i < len(s); i += 3 {
		b.WriteByte(',')
		b.WriteString(s[i : i+3])
	}
	if negative {
		return "-" + b.String()
	}
	return b.String()
}

// FormatTokenStats renders a one-line summary across all results.
func FormatTokenStats(results []types.LintResult) string {
	var promptSum, completionSum, cachedSum int
	for _, r := range results {
		if r.TokenUsage == nil {
			continue
		}
		promptSum += r.TokenUsage.PromptTokens
		completionSum += r.TokenUsage.CompletionTokens
		if r.TokenUsage.CachedTokens != nil {
			cachedSum += *r.TokenUsage.CachedTokens
		}
	}
	total := promptSum + completionSum
	base := fmt.Sprintf("Tokens: %s total (%s input, %s output)",
		formatThousands(total), formatThousands(promptSum), formatThousands(completionSum))
	if cachedSum > 0 && total > 0 {
		savings := int((float64(cachedSum)/float64(total))*100 + 0.5)
		return fmt.Sprintf("%s - %s cached (%d%% savings)", base, formatThousands(cachedSum), savings)
	}
	return base
}

// CheckPreFilter short-circuits the LLM call when the diff contains
// none of the trigger patterns in adr.PreFilter. Returns a synthetic
// PASS result when no patterns match, or nil when the LLM should
// still be invoked.
func CheckPreFilter(a adr.ADR, diff string) *types.LintResult {
	if len(a.PreFilter) == 0 {
		return nil
	}
	for _, p := range a.PreFilter {
		if strings.Contains(diff, p) {
			return nil
		}
	}

	quoted := make([]string, len(a.PreFilter))
	for i, p := range a.PreFilter {
		quoted[i] = `"` + p + `"`
	}
	patternDesc := strings.Join(quoted, " or ")

	// NOT EVALUATED, not passed. Nothing checked this rule, so claiming PASS
	// makes an unenforced rule indistinguishable from an enforced one — and
	// anything counting passes, a merge gate most of all, then over-reports how
	// much policy it actually applied.
	//
	// Measured on crm PR #1302: 9 ADRs were pre-filtered and 2 of them FAIL when
	// actually checked. ADR-0011 forbids transport-layer mocking but its filter
	// lists only flaky-test idioms; ADR-0059 is architectural but its filter
	// lists implementation tokens. Both filters covered ONE CLAUSE of a
	// multi-clause rule, and both reported PASS with high confidence.
	//
	// No Confidence either: no model ran, so there is no verdict to be
	// confident about. Asserting "high" was the most misleading part.
	return &types.LintResult{
		ADR:         a,
		Status:      types.StatusSKIPPED,
		Explanation: "Not evaluated (pre-filter: " + patternDesc + " not found in diff). This rule was NOT checked.",
	}
}
