package budget

// Known profile names.
const (
	ProfileCloudHigh     = "cloud-high"
	ProfileCloudStandard = "cloud-standard"
	ProfileLocalEdge     = "local-edge"
)

// Execution modes.
const (
	ModeAuto        = "auto"
	ModeMonolithic  = "monolithic"
	ModePartitioned = "partitioned"

	ExecutionModeMonolithic  = "monolithic_single"
	ExecutionModePartitioned = "partitioned_fanout"
)

// Profile defines capacity, budget, and capability constraints for a target LLM class.
type Profile struct {
	Name                 string  `json:"name" toml:"name"`
	ContextWindow        int     `json:"context_window" toml:"context_window"`
	TargetBatchBudget    int     `json:"target_batch_budget" toml:"target_batch_budget"`
	TokenRatio           float64 `json:"token_ratio" toml:"token_ratio"`
	MaxParallelAgents    int     `json:"max_parallel_agents" toml:"max_parallel_agents"`
	PreferredAdapter     string  `json:"preferred_adapter" toml:"preferred_adapter"`
	AllowFullFileContext bool    `json:"allow_full_file_context" toml:"allow_full_file_context"`
	AllowADRBodies       bool    `json:"allow_adr_bodies" toml:"allow_adr_bodies"`
}

// StandardProfiles returns the built-in model capability profiles.
func StandardProfiles() map[string]Profile {
	return map[string]Profile{
		ProfileCloudHigh: {
			Name:                 ProfileCloudHigh,
			ContextWindow:        256000,
			TargetBatchBudget:    80000,
			TokenRatio:           3.5,
			MaxParallelAgents:    8,
			PreferredAdapter:     "codex",
			AllowFullFileContext: true,
			AllowADRBodies:       true,
		},
		ProfileCloudStandard: {
			Name:                 ProfileCloudStandard,
			ContextWindow:        200000,
			TargetBatchBudget:    60000,
			TokenRatio:           3.5,
			MaxParallelAgents:    4,
			PreferredAdapter:     "claude",
			AllowFullFileContext: true,
			AllowADRBodies:       true,
		},
		ProfileLocalEdge: {
			Name:                 ProfileLocalEdge,
			ContextWindow:        8192,
			TargetBatchBudget:    4000,
			TokenRatio:           4.0,
			MaxParallelAgents:    1,
			PreferredAdapter:     "cmd",
			AllowFullFileContext: false,
			AllowADRBodies:       false,
		},
	}
}

// BudgetConfig defines preemptive capacity thresholds and bin-packing limits.
type BudgetConfig struct {
	Mode                    string  `json:"mode" toml:"mode"`
	DefaultProfile          string  `json:"default_profile" toml:"default_profile"`
	FanoutThresholdBytes    int64   `json:"fanout_threshold_bytes" toml:"fanout_threshold_bytes"`
	FanoutThresholdSegments int     `json:"fanout_threshold_segments" toml:"fanout_threshold_segments"`
	BatchMaxBytes           int64   `json:"batch_max_bytes" toml:"batch_max_bytes"`
	BatchMaxSegments        int     `json:"batch_max_segments" toml:"batch_max_segments"`
	SafetyMargin            float64 `json:"safety_margin" toml:"safety_margin"`
}

// DefaultConfig provides conservative fallback limits.
func DefaultConfig() BudgetConfig {
	return BudgetConfig{
		Mode:                    ModeAuto,
		DefaultProfile:          ProfileCloudStandard,
		FanoutThresholdBytes:    250000, // 250 KB
		FanoutThresholdSegments: 2,
		BatchMaxBytes:           200000, // 200 KB
		BatchMaxSegments:        4,
		SafetyMargin:            0.20,
	}
}

// Segment represents a reviewable unit (diff + paths + context).
type Segment struct {
	ID               int      `json:"segment"`
	Bytes            int64    `json:"bytes"`
	Paths            []string `json:"paths"`
	FullContextBytes int64    `json:"full_context_bytes"`
	BindingADRBytes  int64    `json:"binding_adr_bytes"`
	EstimatedTokens  int      `json:"estimated_tokens"`
}

// Batch groups one or more segments allocated to a single review execution subagent.
type Batch struct {
	BatchID         string    `json:"batch_id"`
	Segments        []Segment `json:"segments"`
	SegmentIDs      []int     `json:"segment_ids"`
	Bytes           int64     `json:"bytes"`
	Paths           []string  `json:"paths"`
	EstimatedTokens int       `json:"estimated_tokens"`
}

// PlanSummary encapsulates the preemptive capacity budgeting evaluation.
type PlanSummary struct {
	ExecutionMode      string  `json:"execution_mode"`
	Profile            string  `json:"profile"`
	EstimatedTokens    int     `json:"estimated_tokens"`
	TargetBatchBudget  int     `json:"target_batch_budget"`
	DiffBytes          int64   `json:"diff_bytes"`
	BindingADRBytes    int64   `json:"binding_adr_bytes"`
	FullContextBytes   int64   `json:"full_context_bytes"`
	ComplexityOverhead int     `json:"complexity_overhead"`
	Reason             string  `json:"reason"`
	Batches            []Batch `json:"batches,omitempty"`
}
