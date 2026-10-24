package budget

import (
	"math"
	"os"
	"strings"
)

const (
	MaxWholeFileSize = 128 * 1024 // 128 KiB cap per whole file
	DefaultTokenRatio = 3.5

	ComplexityUltraliteTokens = 250
	ComplexityLiteTokens      = 500
	ComplexityStandardTokens  = 1200
)

// EstimateTokens calculates the Total Review Envelope Cost (TREC) in tokens.
func EstimateTokens(diffBytes, bindingADRBytes, fullContextBytes int64, complexity, diffContext string, profile Profile) (int, int) {
	// 1. Resolve environmental overrides
	effComplexity := strings.ToLower(strings.TrimSpace(complexity))
	if env := strings.ToLower(strings.TrimSpace(os.Getenv("ADRIAN_COMPLEXITY"))); env != "" {
		effComplexity = env
	}

	effDiffContext := strings.ToLower(strings.TrimSpace(diffContext))
	if env := strings.ToLower(strings.TrimSpace(os.Getenv("ADRIAN_DIFF_CONTEXT"))); env != "" {
		effDiffContext = env
	}

	ratio := profile.TokenRatio
	if ratio <= 0 {
		ratio = DefaultTokenRatio
	}

	// 2. Diff bytes expansion
	effDiffBytes := float64(diffBytes)
	if effDiffContext == "function" {
		effDiffBytes = effDiffBytes * 1.8
	}
	tokensFromDiff := effDiffBytes / ratio

	// 3. Binding ADR tokens
	tokensFromADR := 0.0
	if profile.AllowADRBodies && bindingADRBytes > 0 {
		tokensFromADR = float64(bindingADRBytes) / ratio
	}

	// 4. Full file context tokens
	tokensFromContext := 0.0
	if profile.AllowFullFileContext && effDiffContext == "full" && fullContextBytes > 0 {
		tokensFromContext = float64(fullContextBytes) / ratio
	}

	// 5. Complexity overhead
	overhead := ComplexityOverhead(effComplexity)

	total := int(math.Ceil(tokensFromDiff + tokensFromADR + tokensFromContext + float64(overhead)))
	return total, overhead
}

// ComplexityOverhead maps complexity tier to static prompt token overhead.
func ComplexityOverhead(tier string) int {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "ultralite":
		return ComplexityUltraliteTokens
	case "lite":
		return ComplexityLiteTokens
	case "standard", "complex":
		return ComplexityStandardTokens
	default:
		return ComplexityStandardTokens
	}
}
