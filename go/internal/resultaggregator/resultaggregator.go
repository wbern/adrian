// Package resultaggregator collapses a slice of per-chunk LintResults
// into one whole-ADR LintResult.
package resultaggregator

import (
	"fmt"
	"strings"

	"github.com/wbern/adrian/go/internal/adr"
	"github.com/wbern/adrian/go/internal/types"
)

// Aggregate combines per-chunk results into one. Empty input yields
// an ERROR result — it indicates a bug in the caller.
func Aggregate(chunks []types.LintResult, a adr.ADR) types.LintResult {
	if len(chunks) == 0 {
		return types.LintResult{
			ADR:         a,
			Status:      types.StatusERROR,
			Explanation: "No chunks to process - this is likely a bug",
		}
	}
	if len(chunks) == 1 {
		return chunks[0]
	}

	var (
		hasError, hasFail, hasWarn bool
		errors                     []types.LintResult
		failures                   []types.LintResult
		allLocations               []string
	)
	for _, r := range chunks {
		switch r.Status {
		case types.StatusERROR:
			hasError = true
			errors = append(errors, r)
		case types.StatusFAIL:
			hasFail = true
			failures = append(failures, r)
		case types.StatusWARN:
			hasWarn = true
			failures = append(failures, r)
		}
		allLocations = append(allLocations, r.Locations...)
	}

	status := types.StatusPASS
	switch {
	case hasError:
		status = types.StatusERROR
	case hasFail:
		status = types.StatusFAIL
	case hasWarn:
		status = types.StatusWARN
	}

	var explanation string
	switch {
	case hasError:
		parts := make([]string, len(errors))
		for i, e := range errors {
			parts[i] = e.Explanation
		}
		explanation = fmt.Sprintf("Errors in %d of %d chunks: %s", len(errors), len(chunks), strings.Join(parts, "; "))
	case hasFail || hasWarn:
		locList := ""
		if len(allLocations) > 0 {
			locList = " at " + strings.Join(allLocations, ", ")
		}
		parts := make([]string, len(failures))
		for i, f := range failures {
			parts[i] = f.Explanation
		}
		explanation = fmt.Sprintf("Violations found in %d of %d chunks%s: %s", len(failures), len(chunks), locList, strings.Join(parts, "; "))
	default:
		explanation = fmt.Sprintf("All %d chunks passed", len(chunks))
	}

	out := types.LintResult{
		ADR:         a,
		Status:      status,
		Explanation: explanation,
	}
	if len(allLocations) > 0 {
		out.Locations = allLocations
	}
	if s := combineSuggestions(chunks); s != "" {
		out.Suggestion = &s
	}
	if c := lowestConfidence(chunks); c != "" {
		conf := types.Confidence(c)
		out.Confidence = &conf
	}
	if tu := aggregateTokenUsage(chunks); tu != nil {
		out.TokenUsage = tu
	}
	// Every chunk was a separate paid call, so the ADR's cost is their sum.
	// Carrying only one chunk's figure would make a rule look cheaper the
	// larger the diff it was checked against.
	for _, c := range chunks {
		out.PromptBytes += c.PromptBytes
	}
	return out
}

func combineSuggestions(chunks []types.LintResult) string {
	var parts []string
	for _, r := range chunks {
		if r.Suggestion != nil && *r.Suggestion != "" {
			parts = append(parts, *r.Suggestion)
		}
	}
	return strings.Join(parts, "; ")
}

var confidenceLevels = map[string]int{"low": 0, "medium": 1, "high": 2}

func lowestConfidence(chunks []types.LintResult) string {
	lowest := "high"
	seen := false
	for _, r := range chunks {
		if r.Confidence == nil {
			continue
		}
		seen = true
		c := string(*r.Confidence)
		if confidenceLevels[c] < confidenceLevels[lowest] {
			lowest = c
		}
	}
	if !seen {
		return ""
	}
	return lowest
}

func aggregateTokenUsage(chunks []types.LintResult) *types.TokenUsage {
	var usages []types.TokenUsage
	for _, r := range chunks {
		if r.TokenUsage != nil {
			usages = append(usages, *r.TokenUsage)
		}
	}
	if len(usages) == 0 {
		return nil
	}
	sum := types.TokenUsage{Model: usages[0].Model}
	cachedSum := 0
	for _, u := range usages {
		sum.PromptTokens += u.PromptTokens
		sum.CompletionTokens += u.CompletionTokens
		sum.TotalTokens += u.TotalTokens
		if u.CachedTokens != nil {
			cachedSum += *u.CachedTokens
		}
	}
	sum.CachedTokens = &cachedSum
	return &sum
}
