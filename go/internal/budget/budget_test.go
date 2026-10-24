package budget

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStandardProfiles(t *testing.T) {
	profiles := StandardProfiles()

	ch, ok := profiles[ProfileCloudHigh]
	if !ok || ch.TargetBatchBudget != 80000 || ch.MaxParallelAgents != 8 || ch.PreferredAdapter != "codex" {
		t.Errorf("unexpected cloud-high profile: %+v", ch)
	}

	cs, ok := profiles[ProfileCloudStandard]
	if !ok || cs.TargetBatchBudget != 60000 || cs.MaxParallelAgents != 4 || cs.PreferredAdapter != "claude" {
		t.Errorf("unexpected cloud-standard profile: %+v", cs)
	}

	le, ok := profiles[ProfileLocalEdge]
	if !ok || le.TargetBatchBudget != 4000 || le.MaxParallelAgents != 1 || le.AllowFullFileContext || le.AllowADRBodies {
		t.Errorf("unexpected local-edge profile: %+v", le)
	}
}

func TestResolveProfile(t *testing.T) {
	profiles := StandardProfiles()

	p1 := ResolveProfile(profiles, "cloud-high", "")
	if p1.Name != ProfileCloudHigh {
		t.Errorf("expected cloud-high, got %s", p1.Name)
	}

	p2 := ResolveProfile(profiles, "", "local-edge")
	if p2.Name != ProfileLocalEdge {
		t.Errorf("expected local-edge, got %s", p2.Name)
	}

	p3 := ResolveProfile(profiles, "unknown", "")
	if p3.Name != ProfileCloudStandard {
		t.Errorf("expected fallback to cloud-standard, got %s", p3.Name)
	}
}

func TestEstimateTokens_ComplexityAndDiffContext(t *testing.T) {
	profile := StandardProfiles()[ProfileCloudStandard] // token ratio = 3.5

	// 1. Ultralite overhead (250 tokens), 350 diff bytes -> 100 tokens + 250 = 350
	tokens, overhead := EstimateTokens(350, 0, 0, "ultralite", "none", profile)
	if overhead != 250 {
		t.Errorf("expected 250 overhead, got %d", overhead)
	}
	if tokens != 350 {
		t.Errorf("expected 350 tokens, got %d", tokens)
	}

	// 2. Lite overhead (500 tokens)
	_, overheadLite := EstimateTokens(350, 0, 0, "lite", "none", profile)
	if overheadLite != 500 {
		t.Errorf("expected 500 overhead for lite, got %d", overheadLite)
	}

	// 3. Standard/complex overhead (1200 tokens)
	_, overheadStd := EstimateTokens(350, 0, 0, "standard", "none", profile)
	if overheadStd != 1200 {
		t.Errorf("expected 1200 overhead for standard, got %d", overheadStd)
	}

	// 4. Function diff context (1.8x expansion):
	// 350 bytes * 1.8 = 630 bytes -> 180 tokens + 1200 = 1380
	tokensFunc, _ := EstimateTokens(350, 0, 0, "standard", "function", profile)
	if tokensFunc != 1380 {
		t.Errorf("expected 1380 tokens for function context, got %d", tokensFunc)
	}

	// 5. Full file context: 700 bytes -> 200 tokens + 100 diff tokens + 1200 = 1500
	tokensFull, _ := EstimateTokens(350, 0, 700, "standard", "full", profile)
	if tokensFull != 1500 {
		t.Errorf("expected 1500 tokens for full context, got %d", tokensFull)
	}
}

func TestEstimateTokens_EnvOverrides(t *testing.T) {
	profile := StandardProfiles()[ProfileCloudStandard]

	os.Setenv("ADRIAN_COMPLEXITY", "ultralite")
	defer os.Unsetenv("ADRIAN_COMPLEXITY")

	os.Setenv("ADRIAN_DIFF_CONTEXT", "function")
	defer os.Unsetenv("ADRIAN_DIFF_CONTEXT")

	tokens, overhead := EstimateTokens(350, 0, 0, "complex", "none", profile)
	if overhead != 250 {
		t.Errorf("expected env override to ultralite (250 tokens), got %d", overhead)
	}
	// 350 * 1.8 = 630 / 3.5 = 180 + 250 = 430
	if tokens != 430 {
		t.Errorf("expected 430 tokens, got %d", tokens)
	}
}

func TestEvaluateExecutionMode(t *testing.T) {
	profile := StandardProfiles()[ProfileCloudStandard] // TargetBatchBudget = 60,000
	cfg := DefaultConfig()                              // FanoutThresholdBytes = 250,000, FanoutThresholdSegments = 2

	// 1. Fits within all thresholds -> Monolithic
	mode, _ := EvaluateExecutionMode(10000, 50000, 1, profile, cfg)
	if mode != ExecutionModeMonolithic {
		t.Errorf("expected monolithic_single, got %s", mode)
	}

	// 2. Diff bytes exceeds threshold (260,000 > 250,000) -> Partitioned
	modeBytes, reasonBytes := EvaluateExecutionMode(10000, 260000, 1, profile, cfg)
	if modeBytes != ExecutionModePartitioned {
		t.Errorf("expected partitioned_fanout for diff bytes, got %s", modeBytes)
	}
	if reasonBytes == "" {
		t.Errorf("expected non-empty reason")
	}

	// 3. Segment count exceeds threshold (3 > 2) -> Partitioned
	modeSegs, _ := EvaluateExecutionMode(10000, 50000, 3, profile, cfg)
	if modeSegs != ExecutionModePartitioned {
		t.Errorf("expected partitioned_fanout for segment count, got %s", modeSegs)
	}

	// 4. Token budget exceeded (70,000 > 60,000) -> Partitioned
	modeTokens, _ := EvaluateExecutionMode(70000, 50000, 1, profile, cfg)
	if modeTokens != ExecutionModePartitioned {
		t.Errorf("expected partitioned_fanout for token budget, got %s", modeTokens)
	}

	// 5. Explicit monolithic mode overrides threshold triggers
	cfgMonolithic := cfg
	cfgMonolithic.Mode = ModeMonolithic
	modeExplicitMono, _ := EvaluateExecutionMode(70000, 300000, 10, profile, cfgMonolithic)
	if modeExplicitMono != ExecutionModeMonolithic {
		t.Errorf("expected explicit monolithic mode to override triggers, got %s", modeExplicitMono)
	}
}

func TestPartitionSegments_AffinityAndLimits(t *testing.T) {
	profile := StandardProfiles()[ProfileCloudStandard]
	cfg := BudgetConfig{
		Mode:             ModeAuto,
		BatchMaxBytes:    1000,
		BatchMaxSegments: 2,
	}

	segs := []Segment{
		{ID: 1, Bytes: 400, Paths: []string{"src/routes/app.tsx"}, EstimatedTokens: 500},
		{ID: 2, Bytes: 400, Paths: []string{"src/routes/login.tsx"}, EstimatedTokens: 500},
		{ID: 3, Bytes: 400, Paths: []string{"supabase/migrations/001.sql"}, EstimatedTokens: 500},
		{ID: 4, Bytes: 400, Paths: []string{"supabase/migrations/002.sql"}, EstimatedTokens: 500},
	}

	batches := PartitionSegments(segs, profile, cfg)
	if len(batches) != 2 {
		t.Fatalf("expected 2 batches, got %d", len(batches))
	}

	// Batch 1 should have segment 1 & 2 (routes affinity)
	// Batch 2 should have segment 3 & 4 (migrations affinity)
	b1 := batches[0]
	b2 := batches[1]

	if len(b1.Segments) != 2 || len(b2.Segments) != 2 {
		t.Errorf("expected 2 segments in each batch, got b1=%d, b2=%d", len(b1.Segments), len(b2.Segments))
	}
}

func TestLoadConfig_TOMLAndEnv(t *testing.T) {
	dir := t.TempDir()
	tomlContent := `
version = 1

[budget]
mode = "auto"
default_profile = "cloud-high"
fanout_threshold_bytes = 150000
fanout_threshold_segments = 3
batch_max_bytes = 100000
batch_max_segments = 2
safety_margin = 0.15

[budget.profiles.custom-fast]
context_window = 32000
target_batch_budget = 10000
token_ratio = 3.8
max_parallel_agents = 6
preferred_adapter = "claude"
`
	configPath := filepath.Join(dir, ".adrian.toml")
	if err := os.WriteFile(configPath, []byte(tomlContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, profiles, err := LoadConfig(configPath, dir)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.DefaultProfile != "cloud-high" {
		t.Errorf("expected default_profile cloud-high, got %s", cfg.DefaultProfile)
	}
	if cfg.FanoutThresholdBytes != 150000 {
		t.Errorf("expected FanoutThresholdBytes 150000, got %d", cfg.FanoutThresholdBytes)
	}
	if cfg.BatchMaxSegments != 2 {
		t.Errorf("expected BatchMaxSegments 2, got %d", cfg.BatchMaxSegments)
	}

	custom, ok := profiles["custom-fast"]
	if !ok || custom.TargetBatchBudget != 10000 || custom.MaxParallelAgents != 6 {
		t.Errorf("failed to load custom profile: %+v", custom)
	}

	// Test environment override
	os.Setenv("ADRIAN_BUDGET_PROFILE", "local-edge")
	defer os.Unsetenv("ADRIAN_BUDGET_PROFILE")

	cfgEnv, _, _ := LoadConfig(configPath, dir)
	if cfgEnv.DefaultProfile != "local-edge" {
		t.Errorf("expected env override to local-edge, got %s", cfgEnv.DefaultProfile)
	}
}
