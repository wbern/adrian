// Package runner provides the orchestration helpers used by the
// adr-lint CLI entrypoint: status summarization, human-readable
// result printing, and CI artifact generation.
package runner

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/wbern/adr-lint/go/internal/adr"
	"github.com/wbern/adr-lint/go/internal/cache"
	"github.com/wbern/adr-lint/go/internal/diffchunker"
	"github.com/wbern/adr-lint/go/internal/diffstats"
	"github.com/wbern/adr-lint/go/internal/filefilter"
	"github.com/wbern/adr-lint/go/internal/formatter"
	"github.com/wbern/adr-lint/go/internal/gitcontext"
	"github.com/wbern/adr-lint/go/internal/logger"
	"github.com/wbern/adr-lint/go/internal/patternmatcher"
	"github.com/wbern/adr-lint/go/internal/resultaggregator"
	"github.com/wbern/adr-lint/go/internal/reviewpacket"
	"github.com/wbern/adr-lint/go/internal/syntheticdiff"
	"github.com/wbern/adr-lint/go/internal/types"
)

// DefaultMaxParallel is the default upper bound on concurrent
// (ADR, files) lint goroutines.
const DefaultMaxParallel = 5

// RunDeps carries the dependencies Run needs. Real runtime wiring lives
// in cmd/adr-lint/main.go; tests inject fakes.
type RunDeps struct {
	Out      io.Writer
	Err      io.Writer
	Git      *gitcontext.Client
	LintFns  map[types.Provider]cache.LintFn
	CacheDir string // empty = cache.GetCacheDir()
}

// Run is the orchestration entrypoint. Returns the desired process
// exit code and any non-recoverable error.
func Run(opts types.LintOptions, deps RunDeps) (int, error) {
	if deps.Out == nil {
		deps.Out = os.Stdout
	}
	if deps.Err == nil {
		deps.Err = os.Stderr
	}
	log := logger.New(deps.Out, deps.Err)

	maxParallel := DefaultMaxParallel
	if opts.Parallel != nil {
		maxParallel = *opts.Parallel
	}

	var targetRef string
	if opts.BranchSet {
		targetRef = opts.BranchRef
	}

	if opts.Verbose && !opts.ReviewPlan {
		log.Log("ADR Lint starting...")
		log.Log(fmt.Sprintf("Provider: %s", opts.Provider))
		log.Log(fmt.Sprintf("Max parallel: %d", maxParallel))
		switch {
		case opts.BranchSet:
			ref := targetRef
			if ref == "" {
				ref = "HEAD"
			}
			filesNote := ""
			if len(opts.Files) > 0 {
				filesNote = " (filtered files)"
			}
			log.Log(fmt.Sprintf("Mode: branch diff (comparing %s against main)%s", ref, filesNote))
		case len(opts.Files) > 0:
			log.Log("Mode: explicit files")
		default:
			log.Log("Mode: staged files")
		}
	}

	gitRoot := deps.Git.GitRoot()
	adrDir := filepath.Join(gitRoot, adr.DirName)

	allChangedFiles, exit, err := selectChangedFiles(opts, deps.Git, gitRoot, targetRef, log)
	if exit != nil || err != nil {
		return derefExit(exit), err
	}

	if len(allChangedFiles) == 0 {
		if opts.ReviewPlan {
			return writeReviewPlan(deps.Out, reviewpacket.Build(nil, reviewpacket.Options{}))
		}
		if opts.BranchSet {
			log.Log("No files changed compared to main.")
		} else {
			log.Log("No staged files to lint.")
		}
		return 0, nil
	}

	changedFiles := filefilter.FilterExcludedFiles(allChangedFiles)

	if opts.Verbose {
		excluded := len(allChangedFiles) - len(changedFiles)
		if excluded > 0 {
			log.Log(fmt.Sprintf("Excluded %d file(s) via global excludes", excluded))
		}
		log.Log(fmt.Sprintf("Changed files: %s", strings.Join(changedFiles, ", ")))
	}

	if len(changedFiles) == 0 {
		if opts.ReviewPlan {
			return writeReviewPlan(deps.Out, reviewpacket.Build(nil, reviewpacket.Options{}))
		}
		log.Log("All changed files are globally excluded from ADR checks.")
		return 0, nil
	}

	adrs, err := adr.ParseADRs(adrDir)
	if err != nil {
		return 1, err
	}

	if len(adrs) == 0 {
		if opts.ReviewPlan {
			return writeReviewPlan(deps.Out, reviewpacket.Build(nil, reviewpacket.Options{}))
		}
		log.Log("No ADRs with Decision sections found.")
		return 0, nil
	}

	applicableADRs := filterApplicableADRs(adrs, changedFiles, opts.ADRs)

	if len(applicableADRs) == 0 {
		if opts.ReviewPlan {
			return writeReviewPlan(deps.Out, reviewpacket.Build(nil, reviewpacket.Options{}))
		}
		if len(opts.ADRs) > 0 {
			log.Log(fmt.Sprintf("No ADRs matching IDs: %s are applicable to changed files.",
				strings.Join(opts.ADRs, ", ")))
		} else {
			log.Log("No ADRs applicable to changed files.")
		}
		return 0, nil
	}

	if opts.Verbose {
		if len(opts.ADRs) > 0 {
			log.Log(fmt.Sprintf("Filtering to ADRs: %s", strings.Join(opts.ADRs, ", ")))
		}
		titles := make([]string, len(applicableADRs))
		for i, a := range applicableADRs {
			titles[i] = a.Title
		}
		log.Log(fmt.Sprintf("Applicable ADRs: %s", strings.Join(titles, ", ")))
	}

	type adrFiles struct {
		adr   adr.ADR
		files []string
	}
	pairs := make([]adrFiles, len(applicableADRs))
	for i, a := range applicableADRs {
		var matching []string
		for _, f := range changedFiles {
			if patternmatcher.MatchesADR(f, a) {
				matching = append(matching, f)
			}
		}
		pairs[i] = adrFiles{adr: a, files: matching}
	}

	getDiff := func(files []string, includeContext bool) string {
		switch {
		case opts.BranchSet:
			return deps.Git.GetDiffAgainstMainForFiles(files, targetRef, includeContext)
		case len(opts.Files) > 0:
			return GenerateSyntheticDiffForFiles(files, gitRoot)
		default:
			return deps.Git.GetStagedDiffForFiles(files, includeContext)
		}
	}
	if opts.ReviewPlan {
		getReviewPlanDiff := func(files []string) string {
			switch {
			case opts.BranchSet:
				return deps.Git.GetDiffAgainstMainForFilesWithContextLines(files, targetRef, 3)
			case len(opts.Files) > 0:
				return GenerateSyntheticDiffForFiles(files, gitRoot)
			default:
				return deps.Git.GetStagedDiffForFilesWithContextLines(files, 3)
			}
		}
		inputs := make([]reviewpacket.Input, 0, len(pairs))
		for _, pair := range pairs {
			inputs = append(inputs, reviewpacket.Input{
				ADR:   pair.adr,
				Files: pair.files,
				Diff:  getReviewPlanDiff(pair.files),
			})
		}
		plan := reviewpacket.Build(inputs, reviewpacket.Options{
			MaxTokensPerChunk: opts.MaxTokensPerChunk,
			RequirePreFilter:  true,
			MaxPackets:        opts.MaxPackets,
		})
		return writeReviewPlan(deps.Out, plan)
	}

	lintFn, ok := deps.LintFns[opts.Provider]
	if !ok || lintFn == nil {
		return 1, fmt.Errorf("provider %q is not available in this build", opts.Provider)
	}
	cacheDir := deps.CacheDir
	if cacheDir == "" {
		cacheDir = cache.GetCacheDir()
	}

	results := make([]types.LintResult, 0, len(pairs))
	for batchStart := 0; batchStart < len(pairs); batchStart += maxParallel {
		end := batchStart + maxParallel
		if end > len(pairs) {
			end = len(pairs)
		}
		batch := pairs[batchStart:end]
		batchResults := make([]types.LintResult, len(batch))
		var wg sync.WaitGroup
		for i, p := range batch {
			wg.Add(1)
			go func(i int, p adrFiles) {
				defer wg.Done()
				batchResults[i] = runOnePair(p.adr, p.files, opts, getDiff, lintFn, cacheDir, log)
			}(i, p)
		}
		wg.Wait()
		results = append(results, batchResults...)
	}

	PrintResults(deps.Out, results, opts)

	if opts.CI {
		if err := WriteCIArtifacts(filepath.Join(gitRoot, "adr-lint-report"), results); err != nil {
			return 1, err
		}
	}

	for _, r := range results {
		if r.Status == types.StatusFAIL || r.Status == types.StatusERROR {
			return 1, nil
		}
	}
	return 0, nil
}

func writeReviewPlan(w io.Writer, plan reviewpacket.Plan) (int, error) {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return 0, encoder.Encode(plan)
}

func derefExit(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func selectChangedFiles(opts types.LintOptions, git *gitcontext.Client, gitRoot, targetRef string, log *logger.Logger) ([]string, *int, error) {
	switch {
	case opts.BranchSet:
		branchFiles := git.GetFilesChangedAgainstMain(targetRef)
		if len(opts.Files) > 0 {
			requested, err := expandGlobs(opts.Files, gitRoot)
			if err != nil {
				return nil, nil, err
			}
			requestedSet := make(map[string]struct{}, len(requested))
			for _, f := range requested {
				requestedSet[f] = struct{}{}
			}
			var filtered []string
			for _, f := range branchFiles {
				if _, ok := requestedSet[f]; ok {
					filtered = append(filtered, f)
				}
			}
			if len(filtered) == 0 {
				log.Log("Specified files have no changes in branch mode.")
				zero := 0
				return nil, &zero, nil
			}
			return filtered, nil, nil
		}
		return branchFiles, nil, nil
	case len(opts.Files) > 0:
		expanded, err := expandGlobs(opts.Files, gitRoot)
		if err != nil {
			return nil, nil, err
		}
		seen := make(map[string]struct{}, len(expanded))
		var deduped []string
		for _, f := range expanded {
			if _, ok := seen[f]; ok {
				continue
			}
			seen[f] = struct{}{}
			deduped = append(deduped, f)
		}
		return deduped, nil, nil
	default:
		return git.GetStagedFiles(), nil, nil
	}
}

func expandGlobs(patterns []string, root string) ([]string, error) {
	var out []string
	rootFS := os.DirFS(root)
	for _, p := range patterns {
		matches, err := doublestar.Glob(rootFS, p)
		if err != nil {
			return nil, fmt.Errorf("glob %q: %w", p, err)
		}
		out = append(out, matches...)
	}
	return out, nil
}

func filterApplicableADRs(adrs []adr.ADR, changedFiles, adrIDs []string) []adr.ADR {
	var out []adr.ADR
	idSet := make(map[string]struct{}, len(adrIDs))
	for _, id := range adrIDs {
		idSet[id] = struct{}{}
	}
	for _, a := range adrs {
		applies := false
		for _, f := range changedFiles {
			if patternmatcher.MatchesADR(f, a) {
				applies = true
				break
			}
		}
		if !applies {
			continue
		}
		if len(idSet) > 0 {
			if _, ok := idSet[a.ID]; !ok {
				continue
			}
		}
		out = append(out, a)
	}
	return out
}

func runOnePair(
	a adr.ADR,
	files []string,
	opts types.LintOptions,
	getDiff func([]string, bool) string,
	lintFn cache.LintFn,
	cacheDir string,
	log *logger.Logger,
) types.LintResult {
	if opts.DryRun {
		return types.LintResult{
			ADR:          a,
			Status:       types.StatusSKIPPED,
			Explanation:  fmt.Sprintf("Dry run - would check %d file(s)", len(files)),
			CheckedFiles: files,
		}
	}
	diff := getDiff(files, a.DiffContext)

	if pre := formatter.CheckPreFilter(a, diff); pre != nil {
		pre.CheckedFiles = files
		return *pre
	}

	cfg, ok := ProviderModelFor(opts.Provider, a.Complexity)
	if !ok {
		return types.LintResult{
			ADR:         a,
			Status:      types.StatusERROR,
			Explanation: fmt.Sprintf("No model configured for provider %s / complexity %s", opts.Provider, a.Complexity),
		}
	}
	maxTokensPerChunk := cfg.MaxTokensPerChunk
	if opts.PerFile {
		maxTokensPerChunk = 1
	}
	chunks := diffchunker.ChunkDiffByFile(diff, maxTokensPerChunk, true)

	cacheOpts := cache.Options{NoCache: opts.NoCache, Verbose: opts.Verbose, Logger: log}

	if len(chunks) == 1 {
		fileStats := convertFileStats(diffstats.ParseDiffStats(diff))
		result, _ := cache.LintWithCache(a, diff, cfg.Model, cache.Provider(opts.Provider), lintFn, cacheDir, cacheOpts)
		result.CheckedFiles = files
		result.FileStats = fileStats
		return result
	}

	if opts.Verbose {
		log.Log(fmt.Sprintf("  📦 Chunked into %d parts for processing", len(chunks)))
	}

	chunkResults := make([]types.LintResult, len(chunks))
	var wg sync.WaitGroup
	for i, chunk := range chunks {
		wg.Add(1)
		go func(i int, chunk string) {
			defer wg.Done()
			r, _ := cache.LintWithCache(a, chunk, cfg.Model, cache.Provider(opts.Provider), lintFn, cacheDir, cacheOpts)
			chunkResults[i] = r
		}(i, chunk)
	}
	wg.Wait()

	aggregated := resultaggregator.Aggregate(chunkResults, a)
	aggregated.CheckedFiles = files
	aggregated.FileStats = convertFileStats(diffstats.ParseDiffStats(diff))
	return aggregated
}

func convertFileStats(stats []diffstats.FileStats) []types.FileStats {
	if len(stats) == 0 {
		return nil
	}
	out := make([]types.FileStats, len(stats))
	for i, s := range stats {
		out[i] = types.FileStats{Path: s.Path, Added: s.Added, Removed: s.Removed, Context: s.Context}
	}
	return out
}

// ComplexityConfig is the model configuration for a (provider,
// complexity) pair, decoded from complexity-models.json.
type ComplexityConfig struct {
	Model             string `json:"model"`
	MaxTokensPerChunk int    `json:"maxTokensPerChunk"`
	MaxOutputTokens   int    `json:"maxOutputTokens"`
}

//go:embed complexity-models.json
var complexityModelsJSON []byte

var providerModels map[types.Provider]map[adr.Complexity]ComplexityConfig

func init() {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(complexityModelsJSON, &raw); err != nil {
		panic("runner: cannot parse complexity-models.json: " + err.Error())
	}
	providerModels = make(map[types.Provider]map[adr.Complexity]ComplexityConfig, len(raw))
	for prov, rawInner := range raw {
		if strings.HasPrefix(prov, "$") {
			continue
		}
		var inner map[string]ComplexityConfig
		if err := json.Unmarshal(rawInner, &inner); err != nil {
			panic("runner: cannot parse complexity-models.json provider " + prov + ": " + err.Error())
		}
		m := make(map[adr.Complexity]ComplexityConfig, len(inner))
		for c, cfg := range inner {
			m[adr.Complexity(c)] = cfg
		}
		providerModels[types.Provider(prov)] = m
	}
}

// ProviderModelFor returns the model configuration for a (provider,
// complexity) pair. Returns ok=false when either key is unknown.
func ProviderModelFor(p types.Provider, c adr.Complexity) (ComplexityConfig, bool) {
	inner, ok := providerModels[p]
	if !ok {
		return ComplexityConfig{}, false
	}
	cfg, ok := inner[c]
	return cfg, ok
}

// ExplanationTruncateLength caps the per-row explanation length in
// the markdown report.
const ExplanationTruncateLength = 200

// Summary holds the per-status totals produced by Summarize.
type Summary struct {
	Passed  int
	Failed  int
	Warned  int
	Skipped int
	Errors  int
}

// PrintResults renders LintResults and a summary to w.
func PrintResults(w io.Writer, results []types.LintResult, opts types.LintOptions) {
	fmt.Fprint(w, "\n=== ADR Lint Results ===\n\n")

	for _, r := range results {
		fmt.Fprintln(w, formatter.FormatResultHeading(r))
		if len(r.Locations) > 0 {
			fmt.Fprintf(w, "   Location: %s\n", strings.Join(r.Locations, ", "))
		}
		fmt.Fprintf(w, "   %s\n", r.Explanation)
		if r.Suggestion != nil && *r.Suggestion != "" {
			fmt.Fprintf(w, "   Fix: %s\n", *r.Suggestion)
		}
		if opts.Verbose && len(r.CheckedFiles) > 0 {
			fmt.Fprintln(w, "   🔍 Changes from files:")
			for _, file := range r.CheckedFiles {
				if stats := findFileStats(r.FileStats, file); stats != nil {
					total := stats.Added + stats.Removed + stats.Context
					fmt.Fprintf(w, "     - %s (+%d -%d, %d lines)\n",
						file, stats.Added, stats.Removed, total)
				} else {
					fmt.Fprintf(w, "     - %s\n", file)
				}
			}
		}
		fmt.Fprintln(w)
	}

	s := Summarize(results)
	fmt.Fprintln(w, "=== Summary ===")
	fmt.Fprintf(w, "Passed: %d\n", s.Passed)
	fmt.Fprintf(w, "Failed: %d\n", s.Failed)
	if s.Warned > 0 {
		fmt.Fprintf(w, "Warnings: %d\n", s.Warned)
	}
	fmt.Fprintf(w, "Skipped: %d\n", s.Skipped)
	fmt.Fprintf(w, "Errors: %d\n", s.Errors)

	cached := 0
	totalTokens := 0
	for _, r := range results {
		if r.Cached {
			cached++
		}
		if r.TokenUsage != nil {
			totalTokens += r.TokenUsage.TotalTokens
		}
	}
	if cached > 0 {
		pct := int(float64(cached)/float64(len(results))*100 + 0.5)
		fmt.Fprintf(w, "Cached: %d/%d (%d%%)\n", cached, len(results), pct)
	}
	if totalTokens > 0 {
		fmt.Fprintln(w, formatter.FormatTokenStats(results))
	}
}

// GenerateSyntheticDiffForFiles reads each file relative to root and
// renders a synthetic unified diff. Missing/unreadable files are
// silently skipped.
func GenerateSyntheticDiffForFiles(files []string, root string) string {
	var parts []string
	for _, file := range files {
		content, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			continue
		}
		diff := syntheticdiff.GenerateSyntheticDiff(file, string(content))
		if diff == "" {
			continue
		}
		parts = append(parts, diff)
	}
	return strings.Join(parts, "\n")
}

// WriteCIArtifacts writes results.json and summary.md under
// artifactDir, creating the directory if it doesn't exist.
func WriteCIArtifacts(artifactDir string, results []types.LintResult) error {
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return err
	}
	jsonBytes, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(artifactDir, "results.json"), jsonBytes, 0o644); err != nil {
		return err
	}
	markdown := GenerateMarkdownReport(results)
	return os.WriteFile(filepath.Join(artifactDir, "summary.md"), []byte(markdown), 0o644)
}

// GenerateMarkdownReport renders a CI-friendly markdown report of the
// LintResults.
func GenerateMarkdownReport(results []types.LintResult) string {
	s := Summarize(results)

	var lines []string
	lines = append(lines, "# ADR Lint Report\n")

	var summaryParts []string
	if s.Passed > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("%d passed", s.Passed))
	}
	if s.Failed > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("%d failed", s.Failed))
	}
	if s.Warned > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("%d warnings", s.Warned))
	}
	if s.Skipped > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("%d skipped", s.Skipped))
	}
	if s.Errors > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("%d errors", s.Errors))
	}
	lines = append(lines, fmt.Sprintf("**Summary:** %s\n", strings.Join(summaryParts, " · ")))

	lines = append(lines, "| | ADR | Result | Tokens Used |")
	lines = append(lines, "|:---:|-----|--------|------------:|")

	sorted := make([]types.LintResult, len(results))
	copy(sorted, results)
	order := map[types.ResultStatus]int{
		types.StatusFAIL:    0,
		types.StatusERROR:   1,
		types.StatusWARN:    2,
		types.StatusPASS:    3,
		types.StatusSKIPPED: 4,
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		return order[sorted[i].Status] < order[sorted[j].Status]
	})

	totalTokens := 0
	for _, r := range sorted {
		icon := formatter.StatusIcons[r.Status]
		adrFile := filepath.Base(r.ADR.FilePath)
		explanation := strings.ReplaceAll(r.Explanation, "\n", " ")
		explanation = strings.ReplaceAll(explanation, "|", `\|`)
		if len(explanation) > ExplanationTruncateLength {
			explanation = explanation[:ExplanationTruncateLength-3] + "..."
		}
		tokens := "-"
		if r.TokenUsage != nil {
			tokens = fmt.Sprintf("%s (%s)", formatThousands(r.TokenUsage.TotalTokens), r.TokenUsage.Model)
			totalTokens += r.TokenUsage.TotalTokens
		}
		lines = append(lines, fmt.Sprintf(
			"| %s | [%s. %s](doc/adr/%s) | %s | %s |",
			icon, r.ADR.ID, r.ADR.Title, adrFile, explanation, tokens,
		))
	}

	if totalTokens > 0 {
		lines = append(lines, fmt.Sprintf("| | | **Total** | **%s** |", formatThousands(totalTokens)))
	}

	lines = append(lines, "")

	var failures, warnings []types.LintResult
	for _, r := range results {
		switch r.Status {
		case types.StatusFAIL, types.StatusERROR:
			failures = append(failures, r)
		case types.StatusWARN:
			warnings = append(warnings, r)
		}
	}

	if len(failures) > 0 {
		lines = append(lines, "> [!CAUTION]")
		plural := ""
		if len(failures) > 1 {
			plural = "s"
		}
		lines = append(lines, fmt.Sprintf(
			"> **%d ADR violation%s detected** - These must be addressed before merging.\n",
			len(failures), plural))
		renderResultDetails(failures, &lines)
	}

	if len(warnings) > 0 {
		lines = append(lines, "> [!WARNING]")
		itemPlural := ""
		needsVerb := "s"
		if len(warnings) > 1 {
			itemPlural = "s"
			needsVerb = ""
		}
		lines = append(lines, fmt.Sprintf(
			"> **%d item%s need%s review** - Low confidence findings that may be false positives.\n",
			len(warnings), itemPlural, needsVerb))
		renderResultDetails(warnings, &lines)
	}

	return strings.Join(lines, "\n")
}

func renderResultDetails(results []types.LintResult, lines *[]string) {
	for _, r := range results {
		icon := formatter.StatusIcons[r.Status]
		adrFile := filepath.Base(r.ADR.FilePath)
		*lines = append(*lines, "<details>")
		*lines = append(*lines, fmt.Sprintf("<summary>%s <strong>ADR %s: %s</strong></summary>\n", icon, r.ADR.ID, r.ADR.Title))
		if len(r.Locations) > 0 {
			label := "Location"
			if len(r.Locations) > 1 {
				label = "Locations"
			}
			quoted := make([]string, len(r.Locations))
			for i, loc := range r.Locations {
				quoted[i] = "`" + loc + "`"
			}
			*lines = append(*lines, fmt.Sprintf("**%s:** %s\n", label, strings.Join(quoted, ", ")))
		}
		if len(r.CheckedFiles) > 0 {
			quoted := make([]string, len(r.CheckedFiles))
			for i, f := range r.CheckedFiles {
				quoted[i] = "`" + f + "`"
			}
			*lines = append(*lines, fmt.Sprintf("**Files checked:** %s\n", strings.Join(quoted, ", ")))
		}
		*lines = append(*lines, fmt.Sprintf("**Analysis:** %s\n", r.Explanation))
		if r.Suggestion != nil && *r.Suggestion != "" {
			*lines = append(*lines, fmt.Sprintf("**Suggested fix:** %s\n", *r.Suggestion))
		}
		*lines = append(*lines, fmt.Sprintf("[View ADR](doc/adr/%s)\n", adrFile))
		*lines = append(*lines, "</details>\n")
	}
}

func formatThousands(n int) string {
	negative := n < 0
	if negative {
		n = -n
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		if negative {
			return "-" + s
		}
		return s
	}
	var b strings.Builder
	first := len(s) % 3
	if first == 0 {
		first = 3
	}
	b.WriteString(s[:first])
	for i := first; i < len(s); i += 3 {
		b.WriteByte(',')
		b.WriteString(s[i : i+3])
	}
	if negative {
		return "-" + b.String()
	}
	return b.String()
}

func findFileStats(stats []types.FileStats, path string) *types.FileStats {
	for i := range stats {
		if stats[i].Path == path {
			return &stats[i]
		}
	}
	return nil
}

// Summarize tallies LintResults by status.
func Summarize(results []types.LintResult) Summary {
	var s Summary
	for _, r := range results {
		switch r.Status {
		case types.StatusPASS:
			s.Passed++
		case types.StatusFAIL:
			s.Failed++
		case types.StatusWARN:
			s.Warned++
		case types.StatusSKIPPED:
			s.Skipped++
		case types.StatusERROR:
			s.Errors++
		}
	}
	return s
}
