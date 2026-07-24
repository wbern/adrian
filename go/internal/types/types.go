// Package types holds shared result/provider types used across the
// adr-lint Go modules. Optional fields are pointers so a missing value
// is distinguishable from a zero value.
package types

import "github.com/wbern/adr-lint/go/internal/adr"

// Provider names a supported LLM backend.
type Provider string

const (
	ProviderClaude Provider = "claude"
)

// ResultStatus is one of PASS/FAIL/WARN/ERROR/SKIPPED.
type ResultStatus string

const (
	StatusPASS    ResultStatus = "PASS"
	StatusFAIL    ResultStatus = "FAIL"
	StatusWARN    ResultStatus = "WARN"
	StatusERROR   ResultStatus = "ERROR"
	StatusSKIPPED ResultStatus = "SKIPPED"
)

// Confidence is a low/medium/high tag attached to lint results.
type Confidence string

// FileStats holds per-file added/removed/context line counts. Kept
// alongside diffstats.FileStats to avoid an import cycle.
type FileStats struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Context int    `json:"context"`
}

// TokenUsage records per-call LLM token consumption.
type TokenUsage struct {
	PromptTokens     int    `json:"promptTokens"`
	CompletionTokens int    `json:"completionTokens"`
	TotalTokens      int    `json:"totalTokens"`
	CachedTokens     *int   `json:"cachedTokens,omitempty"`
	Model            string `json:"model"`
}

// LintOptions is the parsed CLI configuration passed to runner.Run.
//
// Branch has tri-valued semantics: BranchSet=false means "not branch
// mode"; BranchSet=true with empty BranchRef means "branch mode against
// HEAD"; BranchSet=true with BranchRef set means "branch mode against
// the named ref".
type LintOptions struct {
	CI                bool
	Verbose           bool
	DryRun            bool
	BranchSet         bool
	BranchRef         string
	NoCache           bool
	Files             []string
	Provider          Provider
	ADRs              []string
	Parallel          *int
	PerFile           bool
	ReviewPlan        bool
	MaxTokensPerChunk int
	MaxPackets        int
}

// LintResult is the outcome of checking a single ADR against a diff.
type LintResult struct {
	ADR          adr.ADR      `json:"adr"`
	Status       ResultStatus `json:"status"`
	Explanation  string       `json:"explanation"`
	Suggestion   *string      `json:"suggestion,omitempty"`
	Confidence   *Confidence  `json:"confidence,omitempty"`
	TokenUsage   *TokenUsage  `json:"tokenUsage,omitempty"`
	Locations    []string     `json:"locations,omitempty"`
	CheckedFiles []string     `json:"checkedFiles,omitempty"`
	FileStats    []FileStats  `json:"fileStats,omitempty"`
	Cached       bool         `json:"cached,omitempty"`
}
