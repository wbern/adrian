// Package patternmatcher resolves whether a file path matches an ADR's
// `applies_to` glob set, using doublestar's `**` recursive-glob
// semantics.
package patternmatcher

import (
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/wbern/adrian/go/internal/adr"
)

// MatchesADR reports whether file is targeted by the ADR's applies_to list.
//
// Rules:
//  1. Negation patterns (prefixed "!") are checked first and short-circuit
//     to false regardless of their position in the slice.
//  2. Otherwise a positive match anywhere in the list returns true.
//  3. If the list contains only negations and none match, the file is
//     considered matched — the "exclude these from an otherwise-implicit
//     universe" idiom.
func MatchesADR(file string, a adr.ADR) bool {
	if len(a.AppliesToScopes) > 0 {
		for _, patterns := range a.AppliesToScopes {
			if matchesPatterns(file, patterns) {
				return true
			}
		}
		return false
	}
	return matchesPatterns(file, a.AppliesTo)
}

func matchesPatterns(file string, patterns []string) bool {
	for _, p := range patterns {
		if strings.HasPrefix(p, "!") && matchGlob(file, p[1:]) {
			return false
		}
	}

	hasPositive := false
	for _, p := range patterns {
		if strings.HasPrefix(p, "!") {
			continue
		}
		hasPositive = true
		if matchGlob(file, p) {
			return true
		}
	}

	return !hasPositive
}

func matchGlob(file, pattern string) bool {
	ok, err := doublestar.PathMatch(pattern, file)
	if err != nil {
		return false
	}
	return ok
}
