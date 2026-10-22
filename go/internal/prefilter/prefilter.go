// Package prefilter evaluates whether code diffs or file contents match
// an ADR's pre-filter trigger conditions. It provides case-insensitive
// matching, wildcard expansion, and morphological stemming for technical terms
// to eliminate false-negative skips caused by naming variations (e.g.
// "mutation" triggering on "authedMutatingRpc" or "mutateResource").
package prefilter

import (
	"strings"
	"unicode"

	"github.com/wbern/adrian/go/internal/adr"
	"github.com/wbern/adrian/go/internal/types"
)

// MatchesAny reports whether any of the patterns in the slice match the given
// content (case-insensitively, with stem expansion and wildcard support).
func MatchesAny(patterns []string, content string) bool {
	if len(patterns) == 0 {
		return true
	}
	contentLower := strings.ToLower(content)
	for _, p := range patterns {
		if Matches(p, contentLower) {
			return true
		}
	}
	return false
}

// Matches reports whether a single pattern matches contentLower. contentLower
// MUST be lowercase.
func Matches(pattern string, contentLower string) bool {
	p := strings.ToLower(strings.TrimSpace(pattern))
	if p == "" {
		return false
	}

	// 1. Wildcard matching: e.g. "auth*rpc" or "mutat*"
	if strings.Contains(p, "*") {
		return matchWildcard(contentLower, p)
	}

	// 2. Direct substring match (case-insensitive)
	if strings.Contains(contentLower, p) {
		return true
	}

	// 3. Stem matching for technical terms (length >= 4 and single word)
	stems := DeriveStems(p)
	for _, stem := range stems {
		if strings.Contains(contentLower, stem) {
			return true
		}
	}

	return false
}

// matchWildcard returns true if s matches wildcard pattern p (where '*' matches any sequence).
func matchWildcard(s, p string) bool {
	parts := strings.Split(p, "*")
	idx := 0
	for _, part := range parts {
		if part == "" {
			continue
		}
		pos := strings.Index(s[idx:], part)
		if pos < 0 {
			return false
		}
		idx += pos + len(part)
	}
	return true
}

// DeriveStems extracts common technical morphological stems for a term.
// Short terms (<4 chars) and multi-token phrases are not stemmed.
func DeriveStems(term string) []string {
	term = strings.ToLower(strings.TrimSpace(term))
	if len(term) < 4 {
		return nil
	}
	// Do not stem phrases with whitespace or punctuation expressions (e.g. ": any", "as const")
	for _, r := range term {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			return nil
		}
	}

	seen := make(map[string]bool)
	var stems []string
	add := func(s string) {
		if len(s) >= 4 && s != term && !seen[s] {
			seen[s] = true
			stems = append(stems, s)
		}
	}

	// -tions / -tion (e.g. mutations -> mutat, mutation -> mutat, migration -> migrat)
	if strings.HasSuffix(term, "tions") && len(term) > 5 {
		add(term[:len(term)-5] + "t")
		add(term[:len(term)-1]) // strip 's' -> mutation
	} else if strings.HasSuffix(term, "tion") && len(term) > 4 {
		add(term[:len(term)-4] + "t")
	}

	// -ings / -ing (e.g. mutating -> mutat, caching -> cach, routing -> rout)
	if strings.HasSuffix(term, "tings") && len(term) > 5 {
		add(term[:len(term)-4])
	} else if strings.HasSuffix(term, "ting") && len(term) > 4 {
		add(term[:len(term)-3])
	} else if strings.HasSuffix(term, "ings") && len(term) > 4 {
		add(term[:len(term)-4])
	} else if strings.HasSuffix(term, "ing") && len(term) > 3 {
		add(term[:len(term)-3])
	}

	// -ies / -y (e.g. queries -> query, quer)
	if strings.HasSuffix(term, "ies") && len(term) > 4 {
		add(term[:len(term)-3] + "y")
		add(term[:len(term)-3])
	} else if strings.HasSuffix(term, "y") && len(term) > 4 {
		add(term[:len(term)-1])
	}

	// -ted / -ed (e.g. mutated -> mutat, cached -> cach)
	if strings.HasSuffix(term, "ted") && len(term) > 4 {
		add(term[:len(term)-2])
	} else if strings.HasSuffix(term, "ed") && len(term) > 4 {
		add(term[:len(term)-2])
	}

	// -tes / -es (e.g. mutates -> mutat, caches -> cach)
	if strings.HasSuffix(term, "tes") && len(term) > 4 {
		add(term[:len(term)-2])
	} else if strings.HasSuffix(term, "es") && len(term) > 4 {
		add(term[:len(term)-2])
	}

	// trailing -e (e.g. mutate -> mutat, invalidate -> invalidat, cache -> cach)
	if strings.HasSuffix(term, "e") && len(term) > 4 {
		add(term[:len(term)-1])
	}

	// trailing -s (e.g. tokens -> token, events -> event)
	if strings.HasSuffix(term, "s") && !strings.HasSuffix(term, "ss") && len(term) > 4 {
		add(term[:len(term)-1])
	}

	return stems
}

// Check evaluates an ADR's pre-filter against a diff string. If the ADR has
// no pre-filter, or if the diff matches any pre-filter pattern or stem,
// it returns nil (meaning review should proceed). Otherwise, it returns a
// synthetic StatusSKIPPED LintResult.
func Check(a adr.ADR, diff string) *types.LintResult {
	if len(a.PreFilter) == 0 {
		return nil
	}
	if MatchesAny(a.PreFilter, diff) {
		return nil
	}

	quoted := make([]string, len(a.PreFilter))
	for i, p := range a.PreFilter {
		quoted[i] = `"` + p + `"`
	}
	patternDesc := strings.Join(quoted, " or ")

	return &types.LintResult{
		ADR:         a,
		Status:      types.StatusSKIPPED,
		Explanation: "Not evaluated (pre-filter: " + patternDesc + " not found in diff). This rule was NOT checked.",
	}
}
