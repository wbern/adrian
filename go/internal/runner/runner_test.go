package runner

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/wbern/adr-lint/go/internal/adr"
	"github.com/wbern/adr-lint/go/internal/cache"
	"github.com/wbern/adr-lint/go/internal/gitcontext"
	"github.com/wbern/adr-lint/go/internal/reviewpacket"
	"github.com/wbern/adr-lint/go/internal/types"
)

// fakeGit returns a Git client that uses gitRoot as the resolved root
// and serves prerecorded responses from gitOut keyed by joined argv.
func fakeGit(gitRoot string, gitOut map[string]string) *gitcontext.Client {
	c := gitcontext.NewClient(func(args []string) (string, error) {
		return gitOut[strings.Join(args, " ")], nil
	})
	c.SetGitRoot(gitRoot)
	return c
}

func mockADR() adr.ADR {
	return adr.ADR{
		ID:       "0001",
		Title:    "ADR Structure",
		Status:   adr.StatusAccepted,
		FilePath: "doc/adr/0001-adr-structure.md",
	}
}

func TestSummarize_CountsByStatus(t *testing.T) {
	results := []types.LintResult{
		{Status: types.StatusPASS},
		{Status: types.StatusPASS},
		{Status: types.StatusFAIL},
		{Status: types.StatusWARN},
		{Status: types.StatusSKIPPED},
		{Status: types.StatusERROR},
	}
	got := Summarize(results)
	want := Summary{Passed: 2, Failed: 1, Warned: 1, Skipped: 1, Errors: 1}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestPrintResults_SinglePass(t *testing.T) {
	var buf bytes.Buffer
	results := []types.LintResult{
		{ADR: mockADR(), Status: types.StatusPASS, Explanation: "All good"},
	}
	PrintResults(&buf, results, types.LintOptions{})
	out := buf.String()
	if !strings.Contains(out, "=== ADR Lint Results ===") {
		t.Errorf("missing results header: %q", out)
	}
	if !strings.Contains(out, "✅ ADR Structure") {
		t.Errorf("missing PASS heading: %q", out)
	}
	if !strings.Contains(out, "All good") {
		t.Errorf("missing explanation: %q", out)
	}
	if !strings.Contains(out, "=== Summary ===") {
		t.Errorf("missing summary header: %q", out)
	}
	if !strings.Contains(out, "Passed: 1") {
		t.Errorf("missing Passed count: %q", out)
	}
	if !strings.Contains(out, "Failed: 0") {
		t.Errorf("missing Failed count: %q", out)
	}
	if strings.Contains(out, "Warnings:") {
		t.Errorf("Warnings line should be omitted when count is 0: %q", out)
	}
}

func TestPrintResults_ShowsLocationsAndSuggestion(t *testing.T) {
	var buf bytes.Buffer
	sug := "Use Z instead"
	results := []types.LintResult{
		{
			ADR: mockADR(), Status: types.StatusFAIL,
			Explanation: "Violation in foo.go",
			Locations:   []string{"foo.go:10", "foo.go:20"},
			Suggestion:  &sug,
		},
	}
	PrintResults(&buf, results, types.LintOptions{})
	out := buf.String()
	if !strings.Contains(out, "Location: foo.go:10, foo.go:20") {
		t.Errorf("missing locations: %q", out)
	}
	if !strings.Contains(out, "Fix: Use Z instead") {
		t.Errorf("missing suggestion: %q", out)
	}
}

func TestPrintResults_ShowsWarningsWhenPresent(t *testing.T) {
	var buf bytes.Buffer
	results := []types.LintResult{
		{ADR: mockADR(), Status: types.StatusWARN, Explanation: "maybe"},
	}
	PrintResults(&buf, results, types.LintOptions{})
	if !strings.Contains(buf.String(), "Warnings: 1") {
		t.Errorf("missing Warnings count: %q", buf.String())
	}
}

func TestPrintResults_ShowsCachedCount(t *testing.T) {
	var buf bytes.Buffer
	results := []types.LintResult{
		{ADR: mockADR(), Status: types.StatusPASS, Explanation: "OK", Cached: true},
		{ADR: mockADR(), Status: types.StatusPASS, Explanation: "OK", Cached: true},
		{ADR: mockADR(), Status: types.StatusFAIL, Explanation: "V"},
		{ADR: mockADR(), Status: types.StatusPASS, Explanation: "OK"},
	}
	PrintResults(&buf, results, types.LintOptions{})
	out := buf.String()
	if !strings.Contains(out, "Cached: 2/4 (50%)") {
		t.Errorf("missing cached line: %q", out)
	}
}

func TestPrintResults_ShowsTokenStatsWhenTokensPresent(t *testing.T) {
	var buf bytes.Buffer
	results := []types.LintResult{
		{ADR: mockADR(), Status: types.StatusPASS, Explanation: "OK", TokenUsage: &types.TokenUsage{
			PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500, Model: "m",
		}},
	}
	PrintResults(&buf, results, types.LintOptions{})
	out := buf.String()
	if !strings.Contains(out, "Tokens: 1,500 total (1,000 input, 500 output)") {
		t.Errorf("missing token stats: %q", out)
	}
}

func TestGenerateMarkdownReport_HeaderAndSummary(t *testing.T) {
	results := []types.LintResult{
		{ADR: mockADR(), Status: types.StatusPASS, Explanation: "OK"},
		{ADR: mockADR(), Status: types.StatusFAIL, Explanation: "V"},
	}
	got := GenerateMarkdownReport(results)
	if !strings.HasPrefix(got, "# ADR Lint Report") {
		t.Errorf("missing top header: %q", got)
	}
	if !strings.Contains(got, "**Summary:** 1 passed · 1 failed") {
		t.Errorf("missing summary line: %q", got)
	}
}

func TestGenerateMarkdownReport_TableRowsSortedFailuresFirst(t *testing.T) {
	results := []types.LintResult{
		{ADR: adr.ADR{ID: "0001", Title: "First", FilePath: "doc/adr/0001.md"}, Status: types.StatusPASS, Explanation: "OK"},
		{ADR: adr.ADR{ID: "0002", Title: "Second", FilePath: "doc/adr/0002.md"}, Status: types.StatusFAIL, Explanation: "Violation"},
	}
	got := GenerateMarkdownReport(results)
	failIdx := strings.Index(got, "0002. Second")
	passIdx := strings.Index(got, "0001. First")
	if failIdx < 0 || passIdx < 0 {
		t.Fatalf("missing rows: %q", got)
	}
	if failIdx > passIdx {
		t.Errorf("FAIL row should precede PASS row, got fail@%d pass@%d", failIdx, passIdx)
	}
	if !strings.Contains(got, "(doc/adr/0001.md)") {
		t.Errorf("missing file link: %q", got)
	}
}

func TestGenerateMarkdownReport_TruncatesLongExplanations(t *testing.T) {
	long := strings.Repeat("x", 300)
	results := []types.LintResult{
		{ADR: mockADR(), Status: types.StatusFAIL, Explanation: long},
	}
	got := GenerateMarkdownReport(results)
	if !strings.Contains(got, strings.Repeat("x", 197)+"...") {
		t.Errorf("expected truncated to 200 chars with ellipsis: %q", got)
	}
}

func TestGenerateMarkdownReport_EscapesPipes(t *testing.T) {
	results := []types.LintResult{
		{ADR: mockADR(), Status: types.StatusFAIL, Explanation: "a | b"},
	}
	got := GenerateMarkdownReport(results)
	if !strings.Contains(got, `a \| b`) {
		t.Errorf("pipe should be escaped: %q", got)
	}
}

func TestGenerateMarkdownReport_TotalTokensRow(t *testing.T) {
	results := []types.LintResult{
		{ADR: mockADR(), Status: types.StatusPASS, Explanation: "OK", TokenUsage: &types.TokenUsage{TotalTokens: 1500, Model: "m"}},
		{ADR: mockADR(), Status: types.StatusFAIL, Explanation: "V", TokenUsage: &types.TokenUsage{TotalTokens: 2500, Model: "m"}},
	}
	got := GenerateMarkdownReport(results)
	if !strings.Contains(got, "| **Total** | **4,000** |") {
		t.Errorf("missing total tokens row: %q", got)
	}
	if !strings.Contains(got, "1,500 (m)") {
		t.Errorf("missing per-row tokens: %q", got)
	}
}

func TestGenerateMarkdownReport_FailureCautionBlock(t *testing.T) {
	results := []types.LintResult{
		{ADR: mockADR(), Status: types.StatusFAIL, Explanation: "Violation"},
	}
	got := GenerateMarkdownReport(results)
	if !strings.Contains(got, "> [!CAUTION]") {
		t.Errorf("missing CAUTION block: %q", got)
	}
	if !strings.Contains(got, "1 ADR violation detected") {
		t.Errorf("expected singular phrasing: %q", got)
	}
	if !strings.Contains(got, "<details>") {
		t.Errorf("missing details block: %q", got)
	}
	if !strings.Contains(got, "<summary>❌ <strong>ADR 0001: ADR Structure</strong></summary>") {
		t.Errorf("missing summary line: %q", got)
	}
	if !strings.Contains(got, "**Analysis:** Violation") {
		t.Errorf("missing analysis line: %q", got)
	}
}

func TestGenerateMarkdownReport_WarningBlock(t *testing.T) {
	results := []types.LintResult{
		{ADR: mockADR(), Status: types.StatusWARN, Explanation: "maybe"},
	}
	got := GenerateMarkdownReport(results)
	if !strings.Contains(got, "> [!WARNING]") {
		t.Errorf("missing WARNING block: %q", got)
	}
	if !strings.Contains(got, "1 item needs review") {
		t.Errorf("expected singular WARN phrasing: %q", got)
	}
}

func TestGenerateMarkdownReport_DetailsIncludeLocationsFilesAndFix(t *testing.T) {
	sug := "Use X"
	results := []types.LintResult{
		{
			ADR: mockADR(), Status: types.StatusFAIL, Explanation: "V",
			Locations:    []string{"foo.go:1", "bar.go:2"},
			CheckedFiles: []string{"foo.go", "bar.go"},
			Suggestion:   &sug,
		},
	}
	got := GenerateMarkdownReport(results)
	if !strings.Contains(got, "**Locations:** `foo.go:1`, `bar.go:2`") {
		t.Errorf("missing locations: %q", got)
	}
	if !strings.Contains(got, "**Files checked:** `foo.go`, `bar.go`") {
		t.Errorf("missing files checked: %q", got)
	}
	if !strings.Contains(got, "**Suggested fix:** Use X") {
		t.Errorf("missing suggested fix: %q", got)
	}
	if !strings.Contains(got, "[View ADR](doc/adr/0001-adr-structure.md)") {
		t.Errorf("missing View ADR link: %q", got)
	}
}

func TestRun_NoStagedFiles(t *testing.T) {
	gitRoot := t.TempDir()
	git := fakeGit(gitRoot, map[string]string{"diff --cached --name-only": ""})
	var buf bytes.Buffer
	code, err := Run(types.LintOptions{Provider: types.ProviderClaude}, RunDeps{
		Out: &buf, Err: &buf, Git: git,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if !strings.Contains(buf.String(), "No staged files to lint.") {
		t.Errorf("missing message: %q", buf.String())
	}
}

func TestRun_BranchModeNoChangedFiles(t *testing.T) {
	gitRoot := t.TempDir()
	git := fakeGit(gitRoot, map[string]string{
		"merge-base HEAD main":          "abc123\n",
		"diff --name-only abc123..HEAD": "",
	})
	var buf bytes.Buffer
	code, err := Run(types.LintOptions{Provider: types.ProviderClaude, BranchSet: true},
		RunDeps{Out: &buf, Err: &buf, Git: git})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 0 {
		t.Errorf("exit = %d", code)
	}
	if !strings.Contains(buf.String(), "No files changed compared to main.") {
		t.Errorf("missing message: %q", buf.String())
	}
}

func writeADR(t *testing.T, dir, id, title, complexity, appliesTo string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nstatus: accepted\ncomplexity: " + complexity +
		"\napplies_to:\n  - \"" + appliesTo + "\"" +
		"\ndiff_context: false\n---\n\n# " + id + ". " + title + "\n\n## Decision\n\nDo the thing.\n"
	if err := os.WriteFile(filepath.Join(dir, id+"-"+strings.ReplaceAll(strings.ToLower(title), " ", "-")+".md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeADRWithPreFilter(t *testing.T, dir, id, title, complexity, appliesTo, preFilter string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nstatus: accepted\ncomplexity: " + complexity +
		"\napplies_to:\n  - \"" + appliesTo + "\"" +
		"\npre_filter:\n  - \"" + preFilter + "\"" +
		"\ndiff_context: false\n---\n\n# " + id + ". " + title + "\n\n## Decision\n\nDo the thing.\n"
	if err := os.WriteFile(filepath.Join(dir, id+"-"+strings.ReplaceAll(strings.ToLower(title), " ", "-")+".md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRun_DryRunSkipsLintInvocation(t *testing.T) {
	gitRoot := t.TempDir()
	writeADR(t, filepath.Join(gitRoot, "doc/adr"), "0001", "Always", "lite", "pkg/**/*.go")
	git := fakeGit(gitRoot, map[string]string{
		"diff --cached --name-only": "pkg/foo.go\n",
	})

	lintCalled := false
	fakeLint := func(a adr.ADR, diff string) (types.LintResult, error) {
		lintCalled = true
		return types.LintResult{ADR: a, Status: types.StatusPASS, Explanation: "OK"}, nil
	}

	var buf bytes.Buffer
	code, err := Run(types.LintOptions{Provider: types.ProviderClaude, DryRun: true},
		RunDeps{
			Out: &buf, Err: &buf, Git: git,
			LintFns:  map[types.Provider]cache.LintFn{types.ProviderClaude: fakeLint},
			CacheDir: t.TempDir(),
		})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if lintCalled {
		t.Error("dry-run should not invoke lintFn")
	}
	if !strings.Contains(buf.String(), "Dry run - would check 1 file(s)") {
		t.Errorf("missing dry-run explanation: %q", buf.String())
	}
}

func TestRun_ReviewPlanWritesPacketsWithoutModel(t *testing.T) {
	gitRoot := t.TempDir()
	writeADRWithPreFilter(t, filepath.Join(gitRoot, "doc/adr"), "0001", "Brand", "lite", "src/**/*.css", "brand")
	git := fakeGit(gitRoot, map[string]string{
		"diff --cached --name-only": "src/theme.css\n",
		"diff --cached -U3 -- src/theme.css": "diff --git a/src/theme.css b/src/theme.css\n" +
			"index 1111111..2222222 100644\n--- a/src/theme.css\n+++ b/src/theme.css\n" +
			"@@ -1 +1 @@\n-color: old;\n+color: brand;\n",
	})

	var buf bytes.Buffer
	code, err := Run(types.LintOptions{ReviewPlan: true, MaxTokensPerChunk: 128}, RunDeps{
		Out: &buf, Err: &buf, Git: git,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	var plan reviewpacket.Plan
	if err := json.Unmarshal(buf.Bytes(), &plan); err != nil {
		t.Fatalf("review-plan output is not JSON: %v\n%s", err, buf.String())
	}
	if len(plan.Packets) != 1 {
		t.Errorf("packets = %d, want 1", len(plan.Packets))
	}
}

func TestRun_ReviewPlanWritesEmptyJSONWhenNoADRsApply(t *testing.T) {
	gitRoot := t.TempDir()
	git := fakeGit(gitRoot, map[string]string{
		"diff --cached --name-only": "src/theme.css\n",
	})

	var buf bytes.Buffer
	code, err := Run(types.LintOptions{ReviewPlan: true, MaxTokensPerChunk: 2048}, RunDeps{
		Out: &buf, Err: &buf, Git: git,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	var plan reviewpacket.Plan
	if err := json.Unmarshal(buf.Bytes(), &plan); err != nil {
		t.Fatalf("review-plan output is not JSON: %v\n%s", err, buf.String())
	}
	if plan.SchemaVersion != reviewpacket.SchemaVersion || len(plan.Packets) != 0 || plan.Packets == nil || plan.Skipped == nil {
		t.Errorf("plan = %#v, want an empty review plan", plan)
	}
}

func TestRun_NoADRsApplicable(t *testing.T) {
	gitRoot := t.TempDir()
	writeADR(t, filepath.Join(gitRoot, "doc/adr"), "0001", "Frontend", "lite", "frontend/**/*.vue")
	git := fakeGit(gitRoot, map[string]string{
		"diff --cached --name-only": "backend/server.go\n",
	})
	var buf bytes.Buffer
	code, err := Run(types.LintOptions{Provider: types.ProviderClaude},
		RunDeps{Out: &buf, Err: &buf, Git: git})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 0 {
		t.Errorf("exit = %d", code)
	}
	if !strings.Contains(buf.String(), "No ADRs applicable to changed files.") {
		t.Errorf("missing message: %q", buf.String())
	}
}

func TestRun_FailingResultProducesExitCode1(t *testing.T) {
	gitRoot := t.TempDir()
	writeADR(t, filepath.Join(gitRoot, "doc/adr"), "0001", "Catchall", "lite", "**/*.go")
	git := fakeGit(gitRoot, map[string]string{
		"diff --cached --name-only": "pkg/foo.go\n",
		"diff --cached -U0 -- pkg/foo.go": "diff --git a/pkg/foo.go b/pkg/foo.go\n" +
			"index e69de29..d670460 100644\n--- a/pkg/foo.go\n+++ b/pkg/foo.go\n" +
			"@@ -0,0 +1,1 @@\n+const x: any = 5;\n",
	})

	fakeLint := func(a adr.ADR, diff string) (types.LintResult, error) {
		return types.LintResult{ADR: a, Status: types.StatusFAIL, Explanation: "violation"}, nil
	}

	var buf bytes.Buffer
	code, err := Run(types.LintOptions{Provider: types.ProviderClaude},
		RunDeps{
			Out: &buf, Err: &buf, Git: git,
			LintFns:  map[types.Provider]cache.LintFn{types.ProviderClaude: fakeLint},
			CacheDir: t.TempDir(),
		})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(buf.String(), "Failed: 1") {
		t.Errorf("missing failure summary: %q", buf.String())
	}
}

func TestRun_ADRsFilterMessageWhenNoneMatch(t *testing.T) {
	gitRoot := t.TempDir()
	writeADR(t, filepath.Join(gitRoot, "doc/adr"), "0001", "Catchall", "lite", "**/*.go")
	git := fakeGit(gitRoot, map[string]string{
		"diff --cached --name-only": "pkg/foo.go\n",
	})
	var buf bytes.Buffer
	code, err := Run(types.LintOptions{Provider: types.ProviderClaude, ADRs: []string{"9999"}},
		RunDeps{Out: &buf, Err: &buf, Git: git})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 0 {
		t.Errorf("exit = %d", code)
	}
	if !strings.Contains(buf.String(), "No ADRs matching IDs: 9999 are applicable to changed files.") {
		t.Errorf("missing filter message: %q", buf.String())
	}
}

func TestProviderModels_LookupForClaude(t *testing.T) {
	cfg, ok := ProviderModelFor(types.ProviderClaude, adr.ComplexityLite)
	if !ok {
		t.Fatal("expected lookup to succeed for claude/lite")
	}
	if cfg.Model == "" {
		t.Errorf("model name should be non-empty: %+v", cfg)
	}
	if cfg.MaxTokensPerChunk <= 0 {
		t.Errorf("maxTokensPerChunk should be positive: %+v", cfg)
	}
}

func TestProviderModels_UnknownComplexityReturnsFalse(t *testing.T) {
	if _, ok := ProviderModelFor(types.ProviderClaude, adr.Complexity("nonsense")); ok {
		t.Error("expected lookup to fail for unknown complexity")
	}
}

func TestGenerateSyntheticDiffForFiles_ReadsAndJoins(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.go"), []byte("beta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := GenerateSyntheticDiffForFiles([]string{"a.go", "b.go"}, root)
	if !strings.Contains(got, "alpha") || !strings.Contains(got, "beta") {
		t.Errorf("missing file contents in synthetic diff: %q", got)
	}
	if !strings.Contains(got, "a.go") || !strings.Contains(got, "b.go") {
		t.Errorf("missing file paths in synthetic diff: %q", got)
	}
}

func TestGenerateSyntheticDiffForFiles_SkipsMissingFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "exists.go"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := GenerateSyntheticDiffForFiles([]string{"exists.go", "missing.go"}, root)
	if !strings.Contains(got, "exists.go") {
		t.Errorf("missing existing file: %q", got)
	}
	if strings.Contains(got, "missing.go") {
		t.Errorf("missing file should be skipped: %q", got)
	}
}

func TestWriteCIArtifacts_WritesJSONAndMarkdown(t *testing.T) {
	dir := t.TempDir()
	results := []types.LintResult{
		{ADR: mockADR(), Status: types.StatusFAIL, Explanation: "V"},
	}
	if err := WriteCIArtifacts(dir, results); err != nil {
		t.Fatalf("WriteCIArtifacts: %v", err)
	}

	jsonBytes, err := os.ReadFile(filepath.Join(dir, "results.json"))
	if err != nil {
		t.Fatalf("read results.json: %v", err)
	}
	var decoded []types.LintResult
	if err := json.Unmarshal(jsonBytes, &decoded); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(decoded) != 1 || decoded[0].Status != types.StatusFAIL {
		t.Errorf("decoded = %+v", decoded)
	}

	mdBytes, err := os.ReadFile(filepath.Join(dir, "summary.md"))
	if err != nil {
		t.Fatalf("read summary.md: %v", err)
	}
	if !strings.Contains(string(mdBytes), "# ADR Lint Report") {
		t.Errorf("markdown missing header: %q", mdBytes)
	}
}

func TestWriteCIArtifacts_CreatesArtifactDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "report")
	if err := WriteCIArtifacts(dir, nil); err != nil {
		t.Fatalf("WriteCIArtifacts: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "results.json")); err != nil {
		t.Errorf("results.json not created: %v", err)
	}
}

func TestPrintResults_VerboseListsCheckedFiles(t *testing.T) {
	var buf bytes.Buffer
	results := []types.LintResult{
		{
			ADR: mockADR(), Status: types.StatusPASS, Explanation: "OK",
			CheckedFiles: []string{"pkg/foo.go", "pkg/bar.go"},
		},
	}
	PrintResults(&buf, results, types.LintOptions{Verbose: true})
	out := buf.String()
	if !strings.Contains(out, "🔍 Changes from files:") {
		t.Errorf("missing files header: %q", out)
	}
	if !strings.Contains(out, "- pkg/foo.go") {
		t.Errorf("missing foo.go: %q", out)
	}
	if !strings.Contains(out, "- pkg/bar.go") {
		t.Errorf("missing bar.go: %q", out)
	}
}

func TestPrintResults_VerboseShowsFileStats(t *testing.T) {
	var buf bytes.Buffer
	results := []types.LintResult{
		{
			ADR: mockADR(), Status: types.StatusPASS, Explanation: "OK",
			CheckedFiles: []string{"pkg/foo.go"},
			FileStats:    []types.FileStats{{Path: "pkg/foo.go", Added: 5, Removed: 2, Context: 10}},
		},
	}
	PrintResults(&buf, results, types.LintOptions{Verbose: true})
	out := buf.String()
	if !strings.Contains(out, "- pkg/foo.go (+5 -2, 17 lines)") {
		t.Errorf("missing file stats: %q", out)
	}
}

func TestPrintResults_NoFilesListingWhenNotVerbose(t *testing.T) {
	var buf bytes.Buffer
	results := []types.LintResult{
		{
			ADR: mockADR(), Status: types.StatusPASS, Explanation: "OK",
			CheckedFiles: []string{"pkg/foo.go"},
		},
	}
	PrintResults(&buf, results, types.LintOptions{})
	if strings.Contains(buf.String(), "Changes from files") {
		t.Errorf("files listing should be hidden when not verbose: %q", buf.String())
	}
}

func TestPrintResults_NoTokenStatsLineWhenAbsent(t *testing.T) {
	var buf bytes.Buffer
	results := []types.LintResult{
		{ADR: mockADR(), Status: types.StatusPASS, Explanation: "OK"},
	}
	PrintResults(&buf, results, types.LintOptions{})
	if strings.Contains(buf.String(), "Tokens:") {
		t.Errorf("token stats should be absent: %q", buf.String())
	}
}

func TestComplexityModelsJSON_RunnerAndClaudeclientMatch(t *testing.T) {
	runnerCopy, err := os.ReadFile("complexity-models.json")
	if err != nil {
		t.Fatalf("read runner copy: %v", err)
	}
	claudeCopy, err := os.ReadFile("../claudeclient/complexity-models.json")
	if err != nil {
		t.Fatalf("read claudeclient copy: %v", err)
	}
	if string(runnerCopy) != string(claudeCopy) {
		t.Errorf("runner and claudeclient copies of complexity-models.json have drifted")
	}
}

func TestRun_VerboseLogsCacheHitOnSecondInvocation(t *testing.T) {
	gitRoot := t.TempDir()
	writeADR(t, filepath.Join(gitRoot, "doc/adr"), "0001", "Catchall", "lite", "**/*.go")
	git := fakeGit(gitRoot, map[string]string{
		"diff --cached --name-only": "pkg/foo.go\n",
		"diff --cached -U0 -- pkg/foo.go": "diff --git a/pkg/foo.go b/pkg/foo.go\n" +
			"index e69de29..d670460 100644\n--- a/pkg/foo.go\n+++ b/pkg/foo.go\n" +
			"@@ -0,0 +1,1 @@\n+const x: any = 5;\n",
	})

	calls := 0
	fakeLint := func(a adr.ADR, diff string) (types.LintResult, error) {
		calls++
		return types.LintResult{ADR: a, Status: types.StatusPASS, Explanation: "OK"}, nil
	}
	cacheDir := t.TempDir()
	deps := RunDeps{
		Err: new(bytes.Buffer), Git: git,
		LintFns:  map[types.Provider]cache.LintFn{types.ProviderClaude: fakeLint},
		CacheDir: cacheDir,
	}

	var first bytes.Buffer
	deps.Out = &first
	if _, err := Run(types.LintOptions{Provider: types.ProviderClaude, Verbose: true}, deps); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected lintFn called once on first run, got %d", calls)
	}

	var second bytes.Buffer
	deps.Out = &second
	if _, err := Run(types.LintOptions{Provider: types.ProviderClaude, Verbose: true}, deps); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected cache hit on second run (calls still 1), got %d", calls)
	}
	if !strings.Contains(second.String(), "Cache hit for ADR 0001") {
		t.Errorf("missing cache-hit log: %q", second.String())
	}
}

func TestRun_UnregisteredProviderReturnsError(t *testing.T) {
	gitRoot := t.TempDir()
	writeADR(t, filepath.Join(gitRoot, "doc/adr"), "0001", "Always", "lite", "pkg/**/*.go")
	git := fakeGit(gitRoot, map[string]string{
		"diff --cached --name-only": "pkg/foo.go\n",
	})
	var buf bytes.Buffer
	unknown := types.Provider("unknown-provider")
	code, err := Run(
		types.LintOptions{Provider: unknown},
		RunDeps{
			Out: &buf, Err: &buf, Git: git,
			LintFns:  map[types.Provider]cache.LintFn{types.ProviderClaude: nil},
			CacheDir: t.TempDir(),
		},
	)
	if err == nil {
		t.Fatal("expected error for unregistered provider, got nil")
	}
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(err.Error(), "unknown-provider") {
		t.Errorf("error should mention provider name: %v", err)
	}
}

func TestRun_PerFileSplitsAndAggregatesChunks(t *testing.T) {
	gitRoot := t.TempDir()
	writeADR(t, filepath.Join(gitRoot, "doc/adr"), "0001", "Catchall", "lite", "**/*.go")
	diff := "diff --git a/pkg/a.go b/pkg/a.go\n" +
		"index e69de29..d670460 100644\n--- a/pkg/a.go\n+++ b/pkg/a.go\n" +
		"@@ -0,0 +1,1 @@\n+const a = 1;\n" +
		"diff --git a/pkg/b.go b/pkg/b.go\n" +
		"index e69de29..d670460 100644\n--- a/pkg/b.go\n+++ b/pkg/b.go\n" +
		"@@ -0,0 +1,1 @@\n+const b = 2;\n"
	git := fakeGit(gitRoot, map[string]string{
		"diff --cached --name-only":              "pkg/a.go\npkg/b.go\n",
		"diff --cached -U0 -- pkg/a.go pkg/b.go": diff,
	})

	var mu sync.Mutex
	var seenChunks []string
	fakeLint := func(a adr.ADR, chunk string) (types.LintResult, error) {
		mu.Lock()
		seenChunks = append(seenChunks, chunk)
		mu.Unlock()
		return types.LintResult{ADR: a, Status: types.StatusPASS, Explanation: "OK"}, nil
	}
	var buf bytes.Buffer
	code, err := Run(types.LintOptions{Provider: types.ProviderClaude, PerFile: true},
		RunDeps{
			Out: &buf, Err: &buf, Git: git,
			LintFns:  map[types.Provider]cache.LintFn{types.ProviderClaude: fakeLint},
			CacheDir: t.TempDir(),
		})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if len(seenChunks) != 2 {
		t.Errorf("expected 2 chunks via --per-file, got %d", len(seenChunks))
	}
	if !strings.Contains(buf.String(), "Passed: 1") {
		t.Errorf("aggregated result not surfaced as single Passed entry: %q", buf.String())
	}
}

func TestRun_ExplicitFilesUsesSyntheticDiff(t *testing.T) {
	gitRoot := t.TempDir()
	writeADR(t, filepath.Join(gitRoot, "doc/adr"), "0001", "Catchall", "lite", "**/*.go")
	if err := os.MkdirAll(filepath.Join(gitRoot, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitRoot, "pkg/foo.go"), []byte("x := 5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git := fakeGit(gitRoot, nil)

	var receivedDiff string
	fakeLint := func(a adr.ADR, diff string) (types.LintResult, error) {
		receivedDiff = diff
		return types.LintResult{ADR: a, Status: types.StatusPASS, Explanation: "OK"}, nil
	}
	var buf bytes.Buffer
	code, err := Run(
		types.LintOptions{Provider: types.ProviderClaude, Files: []string{"pkg/foo.go"}},
		RunDeps{
			Out: &buf, Err: &buf, Git: git,
			LintFns:  map[types.Provider]cache.LintFn{types.ProviderClaude: fakeLint},
			CacheDir: t.TempDir(),
		})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if !strings.Contains(receivedDiff, "new file mode 100644") {
		t.Errorf("lintFn did not receive synthetic diff: %q", receivedDiff)
	}
	if !strings.Contains(receivedDiff, "+x := 5") {
		t.Errorf("synthetic diff missing file content: %q", receivedDiff)
	}
}

func TestRun_BranchModeInvokesLintWithBranchDiff(t *testing.T) {
	t.Setenv("BASE_SHA", "")
	t.Setenv("HEAD_SHA", "")
	os.Unsetenv("BASE_SHA")
	os.Unsetenv("HEAD_SHA")

	gitRoot := t.TempDir()
	writeADR(t, filepath.Join(gitRoot, "doc/adr"), "0001", "Catchall", "lite", "**/*.go")
	diff := "diff --git a/pkg/foo.go b/pkg/foo.go\n" +
		"index e69de29..d670460 100644\n--- a/pkg/foo.go\n+++ b/pkg/foo.go\n" +
		"@@ -0,0 +1,1 @@\n+const x = 5;\n"
	git := fakeGit(gitRoot, map[string]string{
		"merge-base HEAD main":                "abc123\n",
		"diff --name-only abc123..HEAD":       "pkg/foo.go\n",
		"diff -U0 abc123..HEAD -- pkg/foo.go": diff,
	})

	var receivedDiff string
	fakeLint := func(a adr.ADR, diff string) (types.LintResult, error) {
		receivedDiff = diff
		return types.LintResult{ADR: a, Status: types.StatusPASS, Explanation: "OK"}, nil
	}
	var buf bytes.Buffer
	code, err := Run(types.LintOptions{Provider: types.ProviderClaude, BranchSet: true},
		RunDeps{
			Out: &buf, Err: &buf, Git: git,
			LintFns:  map[types.Provider]cache.LintFn{types.ProviderClaude: fakeLint},
			CacheDir: t.TempDir(),
		})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if !strings.Contains(receivedDiff, "+const x = 5;") {
		t.Errorf("lintFn did not receive branch diff: %q", receivedDiff)
	}
}

func TestRun_PreFilterSkipsLintFn(t *testing.T) {
	gitRoot := t.TempDir()
	writeADRWithPreFilter(t, filepath.Join(gitRoot, "doc/adr"), "0001", "NoPrintln", "lite", "**/*.go", "fmt.Println")
	git := fakeGit(gitRoot, map[string]string{
		"diff --cached --name-only": "pkg/foo.go\n",
		"diff --cached -U0 -- pkg/foo.go": "diff --git a/pkg/foo.go b/pkg/foo.go\n" +
			"index e69de29..d670460 100644\n--- a/pkg/foo.go\n+++ b/pkg/foo.go\n" +
			"@@ -0,0 +1,1 @@\n+greeting := \"hi\"\n",
	})
	lintCalled := false
	fakeLint := func(a adr.ADR, diff string) (types.LintResult, error) {
		lintCalled = true
		return types.LintResult{ADR: a, Status: types.StatusFAIL, Explanation: "should not run"}, nil
	}
	var buf bytes.Buffer
	code, err := Run(types.LintOptions{Provider: types.ProviderClaude},
		RunDeps{
			Out: &buf, Err: &buf, Git: git,
			LintFns:  map[types.Provider]cache.LintFn{types.ProviderClaude: fakeLint},
			CacheDir: t.TempDir(),
		})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if lintCalled {
		t.Error("pre-filter miss should skip lintFn")
	}
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if !strings.Contains(buf.String(), "pre-filter") {
		t.Errorf("missing pre-filter explanation: %q", buf.String())
	}
}

func TestRun_CIWritesArtifacts(t *testing.T) {
	gitRoot := t.TempDir()
	writeADR(t, filepath.Join(gitRoot, "doc/adr"), "0001", "Catchall", "lite", "**/*.go")
	git := fakeGit(gitRoot, map[string]string{
		"diff --cached --name-only": "pkg/foo.go\n",
		"diff --cached -U0 -- pkg/foo.go": "diff --git a/pkg/foo.go b/pkg/foo.go\n" +
			"index e69de29..d670460 100644\n--- a/pkg/foo.go\n+++ b/pkg/foo.go\n" +
			"@@ -0,0 +1,1 @@\n+const x = 5;\n",
	})
	fakeLint := func(a adr.ADR, diff string) (types.LintResult, error) {
		return types.LintResult{ADR: a, Status: types.StatusPASS, Explanation: "OK"}, nil
	}
	var buf bytes.Buffer
	_, err := Run(types.LintOptions{Provider: types.ProviderClaude, CI: true},
		RunDeps{
			Out: &buf, Err: &buf, Git: git,
			LintFns:  map[types.Provider]cache.LintFn{types.ProviderClaude: fakeLint},
			CacheDir: t.TempDir(),
		})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, name := range []string{"results.json", "summary.md"} {
		path := filepath.Join(gitRoot, "adr-lint-report", name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
}
