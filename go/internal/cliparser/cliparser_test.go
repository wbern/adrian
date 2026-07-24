package cliparser

import (
	"strings"
	"testing"

	"github.com/wbern/adr-lint/go/internal/types"
)

func TestParseArgs_DefaultProviderIsClaude(t *testing.T) {
	got, err := ParseArgs([]string{})
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if got.Provider != types.ProviderClaude {
		t.Errorf("Provider = %q, want claude", got.Provider)
	}
}

func TestParseArgs_AcceptsProviderClaude(t *testing.T) {
	got, err := ParseArgs([]string{"--provider", "claude"})
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if got.Provider != types.ProviderClaude {
		t.Errorf("Provider = %q", got.Provider)
	}
}

func TestParseArgs_AcceptsProviderClaudeEqualsSyntax(t *testing.T) {
	got, err := ParseArgs([]string{"--provider=claude"})
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if got.Provider != types.ProviderClaude {
		t.Errorf("Provider = %q", got.Provider)
	}
}

func TestParseArgs_InvalidProviderErrors(t *testing.T) {
	_, err := ParseArgs([]string{"--provider", "openai"})
	if err == nil {
		t.Fatal("expected error for invalid provider")
	}
	if !strings.Contains(err.Error(), `invalid --provider value "openai"`) {
		t.Errorf("err = %q", err.Error())
	}
}

func TestParseArgs_UsesEnvVarWhenNoFlag(t *testing.T) {
	t.Setenv("ADR_LINT_PROVIDER", "claude")
	got, err := ParseArgs([]string{})
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if got.Provider != types.ProviderClaude {
		t.Errorf("Provider = %q", got.Provider)
	}
}

func TestParseArgs_IgnoresInvalidEnvVar(t *testing.T) {
	t.Setenv("ADR_LINT_PROVIDER", "openai")
	got, err := ParseArgs([]string{})
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if got.Provider != types.ProviderClaude {
		t.Errorf("invalid env var should fall back to default; got %q", got.Provider)
	}
}

func TestParseArgs_CIFlag(t *testing.T) {
	got, err := ParseArgs([]string{"--ci"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !got.CI {
		t.Error("CI should be true")
	}
}

func TestParseArgs_ReviewPlanFlag(t *testing.T) {
	got, err := ParseArgs([]string{"--review-plan"})
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if !got.ReviewPlan {
		t.Error("ReviewPlan should be true")
	}
}

func TestParseArgs_ReviewPlanTokenBudget(t *testing.T) {
	got, err := ParseArgs([]string{"--review-plan", "--max-tokens-per-chunk", "2048"})
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if got.MaxTokensPerChunk != 2048 {
		t.Errorf("MaxTokensPerChunk = %d, want 2048", got.MaxTokensPerChunk)
	}
}

func TestParseArgs_ReviewPlanPacketLimit(t *testing.T) {
	got, err := ParseArgs([]string{"--review-plan", "--max-packets", "8"})
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if got.MaxPackets != 8 {
		t.Errorf("MaxPackets = %d, want 8", got.MaxPackets)
	}
}

func TestParseArgs_VerboseFlag(t *testing.T) {
	got, err := ParseArgs([]string{"--verbose"})
	if err != nil || !got.Verbose {
		t.Errorf("Verbose should be true (err=%v)", err)
	}
}

func TestParseArgs_VShortFlag(t *testing.T) {
	got, err := ParseArgs([]string{"-v"})
	if err != nil || !got.Verbose {
		t.Errorf("Verbose should be true (err=%v)", err)
	}
}

func TestParseArgs_BranchFlagWithoutArgUsesHead(t *testing.T) {
	got, err := ParseArgs([]string{"--branch"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !got.BranchSet || got.BranchRef != "" {
		t.Errorf("BranchSet=%v BranchRef=%q, want set+empty", got.BranchSet, got.BranchRef)
	}
}

func TestParseArgs_BShortFlagWithoutArgUsesHead(t *testing.T) {
	got, err := ParseArgs([]string{"-b"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !got.BranchSet || got.BranchRef != "" {
		t.Errorf("BranchSet=%v BranchRef=%q", got.BranchSet, got.BranchRef)
	}
}

func TestParseArgs_BranchWithTargetRef(t *testing.T) {
	got, err := ParseArgs([]string{"--branch", "feat/other-branch"})
	if err != nil || got.BranchRef != "feat/other-branch" {
		t.Errorf("BranchRef = %q (err=%v)", got.BranchRef, err)
	}
}

func TestParseArgs_BShortWithTargetRef(t *testing.T) {
	got, err := ParseArgs([]string{"-b", "origin/main"})
	if err != nil || got.BranchRef != "origin/main" {
		t.Errorf("BranchRef = %q (err=%v)", got.BranchRef, err)
	}
}

func TestParseArgs_BranchTreatsFlagAsNotABranchArg(t *testing.T) {
	got, err := ParseArgs([]string{"--branch", "--verbose"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !got.BranchSet || got.BranchRef != "" {
		t.Errorf("BranchSet=%v BranchRef=%q", got.BranchSet, got.BranchRef)
	}
	if !got.Verbose {
		t.Error("verbose should be true")
	}
}

func TestParseArgs_FilesMultiplePatterns(t *testing.T) {
	got, err := ParseArgs([]string{"--files", "pkg/**/*.go", "internal/**/*.go"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []string{"pkg/**/*.go", "internal/**/*.go"}
	if len(got.Files) != 2 || got.Files[0] != want[0] || got.Files[1] != want[1] {
		t.Errorf("Files = %v, want %v", got.Files, want)
	}
}

func TestParseArgs_PerFileFlag(t *testing.T) {
	got, err := ParseArgs([]string{"--per-file"})
	if err != nil || !got.PerFile {
		t.Errorf("PerFile should be true (err=%v)", err)
	}
}

func TestParseArgs_PerFileNotSetByDefault(t *testing.T) {
	got, err := ParseArgs([]string{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.PerFile {
		t.Error("PerFile should be false")
	}
}

func TestParseArgs_PerFileWithOtherFlags(t *testing.T) {
	got, err := ParseArgs([]string{"--per-file", "--verbose", "--branch"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !got.PerFile || !got.Verbose || !got.BranchSet {
		t.Errorf("got=%+v", got)
	}
}

func TestParseArgs_ParallelNumericValue(t *testing.T) {
	got, err := ParseArgs([]string{"--parallel", "1"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.Parallel == nil || *got.Parallel != 1 {
		t.Errorf("Parallel = %v", got.Parallel)
	}
}

func TestParseArgs_ParallelUnsetWhenAbsent(t *testing.T) {
	got, err := ParseArgs([]string{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.Parallel != nil {
		t.Errorf("Parallel should be nil, got %v", *got.Parallel)
	}
}

func TestParseArgs_ParallelNonNumericErrors(t *testing.T) {
	_, err := ParseArgs([]string{"--parallel", "abc"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `invalid --parallel value "abc"`) {
		t.Errorf("err = %q", err.Error())
	}
}

func TestParseArgs_ParallelZeroErrors(t *testing.T) {
	_, err := ParseArgs([]string{"--parallel", "0"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `invalid --parallel value "0"`) {
		t.Errorf("err = %q", err.Error())
	}
}
