package cache

import (
	"bytes"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/wbern/adrian/go/internal/adr"
	"github.com/wbern/adrian/go/internal/logger"
	"github.com/wbern/adrian/go/internal/types"
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

func TestBuildCacheKey_SHA256Hex(t *testing.T) {
	a := sampleADR()
	key := BuildCacheKey(a, `+ import "github.com/stretchr/testify/assert";`, "claude-sonnet-4-6", "claude")
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(key) {
		t.Errorf("key is not 64-char hex: %q", key)
	}
}

func TestBuildCacheKey_DeterministicForSameInput(t *testing.T) {
	a := sampleADR()
	diff := `+ import "github.com/stretchr/testify/assert";`
	k1 := BuildCacheKey(a, diff, "claude-sonnet-4-6", "claude")
	k2 := BuildCacheKey(a, diff, "claude-sonnet-4-6", "claude")
	if k1 != k2 {
		t.Errorf("expected identical keys, got %q vs %q", k1, k2)
	}
}

func TestBuildCacheKey_DifferentInputsProduceDifferentKeys(t *testing.T) {
	base := sampleADR()
	baseDiff := `+ import "github.com/stretchr/testify/assert";`
	baseModel := "claude-sonnet-4-6"
	var baseProvider Provider = "claude"
	baseKey := BuildCacheKey(base, baseDiff, baseModel, baseProvider)

	differentID := sampleADR()
	differentID.ID = "0003"

	differentDecision := sampleADR()
	differentDecision.Decision = "Updated check for gomock usage"

	cases := []struct {
		name string
		key  string
	}{
		{"id differs", BuildCacheKey(differentID, baseDiff, baseModel, baseProvider)},
		{"decision differs", BuildCacheKey(differentDecision, baseDiff, baseModel, baseProvider)},
		{"diff differs", BuildCacheKey(base, `+ import "github.com/golang/mock/gomock";`, baseModel, baseProvider)},
		{"provider differs", BuildCacheKey(base, baseDiff, baseModel, "other-provider")},
		{"model differs", BuildCacheKey(base, baseDiff, "claude-opus-4-6", baseProvider)},
	}
	for _, c := range cases {
		if c.key == baseKey {
			t.Errorf("%s: expected a different key than the base", c.name)
		}
	}
}

func TestGetCachedResult_MissReturnsNil(t *testing.T) {
	dir := t.TempDir()
	r, err := GetCachedResult("non-existent-key", dir)
	if err != nil {
		t.Errorf("expected no error on miss, got %v", err)
	}
	if r != nil {
		t.Errorf("expected nil on miss, got %+v", r)
	}
}

func TestSetAndGetCachedResult_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	a := sampleADR()
	c := types.Confidence("high")
	original := types.LintResult{
		ADR:         a,
		Status:      types.StatusPASS,
		Explanation: "No violations found",
		Confidence:  &c,
	}

	if err := SetCachedResult("test-cache-key", original, dir); err != nil {
		t.Fatalf("SetCachedResult: %v", err)
	}
	got, err := GetCachedResult("test-cache-key", dir)
	if err != nil {
		t.Fatalf("GetCachedResult: %v", err)
	}
	if got == nil {
		t.Fatal("expected result, got nil")
	}
	if !reflect.DeepEqual(*got, original) {
		t.Errorf("round-trip mismatch:\nwant %+v\n got %+v", original, *got)
	}
}

func TestGetCacheDir(t *testing.T) {
	if got := GetCacheDir(); got != ".cache/adr-lint" {
		t.Errorf("got %q, want .cache/adr-lint", got)
	}
}

func TestLintWithCache_HitSkipsLintFn(t *testing.T) {
	dir := t.TempDir()
	a := sampleADR()
	diff := `+ import "github.com/stretchr/testify/assert";`
	model := "claude-sonnet-4-6"

	key := BuildCacheKey(a, diff, model, "claude")
	cached := types.LintResult{
		ADR:         a,
		Status:      types.StatusPASS,
		Explanation: "Cached result - no violations",
	}
	if err := SetCachedResult(key, cached, dir); err != nil {
		t.Fatalf("seed: %v", err)
	}

	called := false
	lintFn := func(_ adr.ADR, _ string) (types.LintResult, error) {
		called = true
		return types.LintResult{
			ADR: a, Status: types.StatusFAIL, Explanation: "Fresh result - would indicate violation",
		}, nil
	}

	got, err := LintWithCache(a, diff, model, "claude", lintFn, dir, Options{})
	if err != nil {
		t.Fatalf("LintWithCache: %v", err)
	}
	if called {
		t.Error("lintFn should not be called on cache hit")
	}
	if got.Explanation != "Cached result - no violations" {
		t.Errorf("explanation = %q", got.Explanation)
	}
	if !got.Cached {
		t.Error("cache-hit result should have Cached=true")
	}
}

func TestLintWithCache_MissCallsLintFnAndStores(t *testing.T) {
	dir := t.TempDir()
	a := sampleADR()
	diff := `+ import "github.com/stretchr/testify/assert";`
	model := "claude-sonnet-4-6"

	fresh := types.LintResult{
		ADR: a, Status: types.StatusPASS, Explanation: "Fresh result from lintFn",
	}
	callCount := 0
	lintFn := func(_ adr.ADR, _ string) (types.LintResult, error) {
		callCount++
		return fresh, nil
	}

	r1, err := LintWithCache(a, diff, model, "claude", lintFn, dir, Options{})
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if callCount != 1 {
		t.Errorf("first call: lintFn called %d times, want 1", callCount)
	}
	if r1.Explanation != "Fresh result from lintFn" {
		t.Errorf("first call: explanation = %q", r1.Explanation)
	}

	r2, err := LintWithCache(a, diff, model, "claude", lintFn, dir, Options{})
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if callCount != 1 {
		t.Errorf("second call: lintFn called %d times (should still be 1)", callCount)
	}
	if r2.Explanation != "Fresh result from lintFn" {
		t.Errorf("second call: explanation = %q", r2.Explanation)
	}
}

func TestLintWithCache_VerboseLogsCacheHit(t *testing.T) {
	dir := t.TempDir()
	a := sampleADR()
	diff := `+ import "github.com/stretchr/testify/assert";`
	model := "claude-sonnet-4-6"

	key := BuildCacheKey(a, diff, model, "claude")
	if err := SetCachedResult(key, types.LintResult{
		ADR: a, Status: types.StatusPASS, Explanation: "Cached result",
	}, dir); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var out bytes.Buffer
	lintFn := func(_ adr.ADR, _ string) (types.LintResult, error) {
		t.Fatal("lintFn must not run on cache hit")
		return types.LintResult{}, nil
	}

	_, err := LintWithCache(a, diff, model, "claude", lintFn, dir, Options{
		Verbose: true,
		Logger:  logger.New(&out, nil),
	})
	if err != nil {
		t.Fatalf("LintWithCache: %v", err)
	}
	if !strings.Contains(out.String(), "Cache hit for ADR 0002") {
		t.Errorf("verbose log missing; got %q", out.String())
	}
}

func TestLintWithCache_CILogsCacheHit(t *testing.T) {
	t.Setenv("CI", "true")
	dir := t.TempDir()
	a := sampleADR()
	a.ID = "0003"
	diff := `+ import "github.com/stretchr/testify/assert";`
	model := "claude-sonnet-4-6"

	key := BuildCacheKey(a, diff, model, "claude")
	if err := SetCachedResult(key, types.LintResult{
		ADR: a, Status: types.StatusPASS, Explanation: "Cached result",
	}, dir); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var out bytes.Buffer
	lintFn := func(_ adr.ADR, _ string) (types.LintResult, error) {
		t.Fatal("lintFn must not run on cache hit")
		return types.LintResult{}, nil
	}

	_, err := LintWithCache(a, diff, model, "claude", lintFn, dir, Options{
		Logger: logger.New(&out, nil),
	})
	if err != nil {
		t.Fatalf("LintWithCache: %v", err)
	}
	if !strings.Contains(out.String(), "Cache hit for ADR 0003") {
		t.Errorf("CI log missing; got %q", out.String())
	}
}

func TestLintWithCache_NoCacheBypassesCache(t *testing.T) {
	dir := t.TempDir()
	a := sampleADR()
	diff := `+ import "github.com/stretchr/testify/assert";`
	model := "claude-sonnet-4-6"

	// Seed: old cached value
	key := BuildCacheKey(a, diff, model, "claude")
	if err := SetCachedResult(key, types.LintResult{
		ADR: a, Status: types.StatusPASS, Explanation: "Old cached result",
	}, dir); err != nil {
		t.Fatalf("seed: %v", err)
	}

	called := false
	lintFn := func(_ adr.ADR, _ string) (types.LintResult, error) {
		called = true
		return types.LintResult{
			ADR: a, Status: types.StatusFAIL, Explanation: "Fresh result bypassing cache",
		}, nil
	}

	got, err := LintWithCache(a, diff, model, "claude", lintFn, dir, Options{NoCache: true})
	if err != nil {
		t.Fatalf("LintWithCache: %v", err)
	}
	if !called {
		t.Error("noCache=true must invoke lintFn even on cache hit")
	}
	if got.Explanation != "Fresh result bypassing cache" {
		t.Errorf("explanation = %q, want bypass result", got.Explanation)
	}
}
