// Package formatter renders LintResults for human consumption and
// implements the pre-filter short-circuit.
package formatter

import (
	"fmt"
	"strings"

	"github.com/wbern/adrian/go/internal/adr"
	"github.com/wbern/adrian/go/internal/prefilter"
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
// StatusSKIPPED result when no patterns match, or nil when the LLM should
// still be invoked.
func CheckPreFilter(a adr.ADR, diff string) *types.LintResult {
	return prefilter.Check(a, diff)
}
