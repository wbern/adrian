package patternmatcher

import (
	"testing"

	"github.com/wbern/adrian/go/internal/adr"
)

func newADR(appliesTo []string) adr.ADR {
	return adr.ADR{
		ID:          "1",
		Title:       "Test ADR",
		Status:      adr.StatusAccepted,
		AppliesTo:   appliesTo,
		Complexity:  adr.ComplexityStandard,
		Decision:    "Check something",
		FilePath:    "/test/adr.md",
		Content:     "Test content",
		DiffContext: true,
	}
}

func TestMatchesADR_PositivePattern(t *testing.T) {
	a := newADR([]string{"**/*.go"})

	if !MatchesADR("pkg/utils.go", a) {
		t.Error("expected match for pkg/utils.go under **/*.go")
	}
	if MatchesADR("pkg/utils.py", a) {
		t.Error("expected no match for pkg/utils.py under **/*.go")
	}
}

func TestTypedScopesPreserveLegacyLintApplicability(t *testing.T) {
	a := adr.ParseADR(`---
status: accepted
applies_to:
  - paths: ['src/**/*.tsx', '!src/**/*.test.tsx']
    review: {requires: [visual-product-judgment]}
  - paths: ['src/**/*.test.tsx']
    review: {requires: [test-quality]}
---
# 0014. Mobile

## Decision

Apply architecture to both implementation and its tests.
`, "0014-mobile.md")
	for _, p := range []string{"src/login.tsx", "src/login.test.tsx"} {
		if !MatchesADR(p, a) {
			t.Fatalf("lost scope %s", p)
		}
	}
	if MatchesADR("backend/unrelated.go", a) {
		t.Fatal("typed scopes silently became universe")
	}
}

func TestMatchesADR_NegationExcludes(t *testing.T) {
	a := newADR([]string{"**/*.go", "!**/*_test.go"})

	if !MatchesADR("pkg/utils.go", a) {
		t.Error("expected pkg/utils.go to match")
	}
	if MatchesADR("pkg/utils_test.go", a) {
		t.Error("expected pkg/utils_test.go to be excluded by negation")
	}
}

func TestMatchesADR_NegationOrderIndependent(t *testing.T) {
	// Negation listed after positive
	a1 := newADR([]string{"**/*.go", "!**/*_test.go"})
	if MatchesADR("pkg/utils_test.go", a1) {
		t.Error("negation after positive: expected exclusion")
	}

	// Negation listed before positive
	a2 := newADR([]string{"!**/*_test.go", "**/*.go"})
	if MatchesADR("pkg/utils_test.go", a2) {
		t.Error("negation before positive: expected exclusion")
	}
}

func TestMatchesADR_OnlyNegationsDefaultsToMatch(t *testing.T) {
	a := newADR([]string{"!**/*_test.go"})

	if !MatchesADR("pkg/utils.go", a) {
		t.Error("only-negation patterns should default-match non-excluded files")
	}
	if MatchesADR("pkg/utils_test.go", a) {
		t.Error("only-negation patterns should still exclude their target")
	}
}

func TestMatchesADR_MultiplePositive(t *testing.T) {
	a := newADR([]string{"**/*.go", "**/*.proto"})

	if !MatchesADR("pkg/service.proto", a) {
		t.Error("service.proto should match")
	}
	if !MatchesADR("pkg/utils.go", a) {
		t.Error("utils.go should match")
	}
	if MatchesADR("pkg/styles.css", a) {
		t.Error("styles.css should not match")
	}
}

func TestMatchesADR_ComplexGlobs(t *testing.T) {
	a := newADR([]string{
		"pkg/**/*.go",
		"!pkg/**/*_test.go",
		"!pkg/**/mock_*.go",
	})

	cases := []struct {
		path string
		want bool
	}{
		{"pkg/utils/helpers.go", true},
		{"pkg/utils/helpers_test.go", false},
		{"pkg/utils/mock_client.go", false},
		{"lib/utils.go", false},
	}
	for _, c := range cases {
		if got := MatchesADR(c.path, a); got != c.want {
			t.Errorf("MatchesADR(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
