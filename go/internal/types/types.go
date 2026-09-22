// Package types holds shared result/provider types used across the
// adr-lint Go modules. Optional fields are pointers so a missing value
// is distinguishable from a zero value.
package types

import "github.com/wbern/adrian/go/internal/adr"

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
	CI        bool
	Verbose   bool
	DryRun    bool
	BranchSet bool
	BranchRef string
	NoCache   bool
	Files     []string
	Provider  Provider
	ADRs      []string
	Parallel  *int
	PerFile   bool

	// DiffSet/DiffPath supply the diff from outside git — a PR diff piped in,
	// for example. DiffPath "-" reads stdin. This is what lets adr-lint run
	// against a pull request without checking the branch out; the ADR corpus
	// still comes from the working directory.
	DiffSet  bool
	DiffPath string

	// CacheDir/ReportDir relocate the two things a run writes. Both default to
	// paths under the repository root, which is wrong whenever the repository
	// is a shared read-only checkout being borrowed for its doc/adr.
	CacheDir  string
	ReportDir string

	// NoPreFilter checks every applicable ADR with the model, ignoring
	// pre_filter. Deliberately not a performance knob: it is the control arm
	// for detecting a pre_filter whose terms no longer match the rule it
	// guards, which otherwise fails silently as a pass.
	NoPreFilter bool
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

	// PromptBytes is what was actually sent to the model for this ADR —
	// measured, not estimated. It answers "what did enforcing this rule cost"
	// without needing a tokenizer, and it is the per-ADR analogue of the byte
	// accounting a review gate uses to prove coverage. Zero means nothing was
	// sent (a skip or a cache hit), which is a different claim from "cheap".
	PromptBytes int `json:"promptBytes,omitempty"`
}
