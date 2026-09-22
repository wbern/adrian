package promptbuilder

import (
	"strings"
	"testing"

	"github.com/wbern/adrian/go/internal/adr"
)

func sampleADR() adr.ADR {
	return adr.ADR{
		ID:          "0002",
		Title:       "Use Testify",
		Status:      adr.StatusAccepted,
		AppliesTo:   []string{"**/*_test.go"},
		Complexity:  adr.ComplexityUltralite,
		Decision:    "Check for gomock usage",
		FilePath:    "/test/adr.md",
		Content:     "Test content",
		DiffContext: true,
	}
}

func TestBuildPrompt_ContainsADRTitleAndDiff(t *testing.T) {
	a := sampleADR()
	diff := `+ import "github.com/stretchr/testify/assert";`

	prompt := BuildPrompt(a, diff)

	if !strings.Contains(prompt, "Use Testify") {
		t.Errorf("prompt missing ADR title 'Use Testify'")
	}
	if !strings.Contains(prompt, diff) {
		t.Errorf("prompt missing diff")
	}
}

func TestBuildPrompt_ComplexityGuardrailsBranch(t *testing.T) {
	cases := []struct {
		name       string
		complexity adr.Complexity
		marker     string
	}{
		{"ultralite", adr.ComplexityUltralite, "SIMPLE KEYWORD CHECK"},
		{"lite", adr.ComplexityLite, `Set confidence to "low" when uncertain`},
		{"standard", adr.ComplexityStandard, "architectural concerns"},
		{"complex", adr.ComplexityComplex, "architectural concerns"},
	}
	for _, c := range cases {
		a := sampleADR()
		a.Complexity = c.complexity
		prompt := BuildPrompt(a, "+ some line")
		if !strings.Contains(prompt, c.marker) {
			t.Errorf("complexity=%s: prompt missing %q", c.name, c.marker)
		}
	}
}
