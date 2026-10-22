// Package auditcmd implements `adrian audit` (and `adrian doctor`),
// a comprehensive repository-level auditor that validates ADR health against
// the actual codebase: detecting dormant applies_to globs, dead pre-filters,
// and unbounded complex rules.
package auditcmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wbern/adrian/go/internal/adr"
	"github.com/wbern/adrian/go/internal/gitcontext"
	"github.com/wbern/adrian/go/internal/patternmatcher"
	"github.com/wbern/adrian/go/internal/prefilter"
)

// AuditReport summarizes the results of an ADR audit.
type AuditReport struct {
	Summary AuditSummary `json:"summary"`
	Issues  []AuditIssue `json:"issues"`
}

// AuditSummary contains aggregate counts for the audit run.
type AuditSummary struct {
	TotalADRs    int            `json:"total_adrs"`
	ActiveADRs   int            `json:"active_adrs"`
	TrackedFiles int            `json:"tracked_files"`
	StatusCounts map[string]int `json:"status_counts"`
	ErrorCount   int            `json:"error_count"`
	WarningCount int            `json:"warning_count"`
	InfoCount    int            `json:"info_count"`
	Passed       bool           `json:"passed"`
}

// AuditIssue represents a single rule or configuration issue detected during audit.
type AuditIssue struct {
	ADRID    string `json:"adr_id"`
	ADRTitle string `json:"adr_title"`
	Code     string `json:"code"`
	Severity string `json:"severity"` // "error", "warning", "info"
	Message  string `json:"message"`
}

// Options configures the audit run.
type Options struct {
	Format string // "text" or "json"
	Strict bool   // fail on warnings if true
	All    bool   // include deprecated/superseded ADRs if true
}

// Run executes the audit command.
func Run(args []string, dir string, out io.Writer) error {
	opts, err := parseArgs(args)
	if err != nil {
		return err
	}

	report, err := AuditRepository(dir, opts)
	if err != nil {
		return err
	}

	if opts.Format == "json" {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		renderText(report, out)
	}

	if report.Summary.ErrorCount > 0 {
		return fmt.Errorf("audit failed with %d error(s)", report.Summary.ErrorCount)
	}
	if opts.Strict && report.Summary.WarningCount > 0 {
		return fmt.Errorf("audit failed in strict mode with %d warning(s)", report.Summary.WarningCount)
	}
	return nil
}

func parseArgs(args []string) (Options, error) {
	opts := Options{Format: "text"}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json":
			opts.Format = "json"
		case arg == "--format" && i+1 < len(args):
			i++
			fmtVal := strings.ToLower(args[i])
			if fmtVal != "text" && fmtVal != "json" {
				return opts, fmt.Errorf("unsupported format %q: expected text or json", args[i])
			}
			opts.Format = fmtVal
		case strings.HasPrefix(arg, "--format="):
			fmtVal := strings.ToLower(strings.TrimPrefix(arg, "--format="))
			if fmtVal != "text" && fmtVal != "json" {
				return opts, fmt.Errorf("unsupported format %q: expected text or json", fmtVal)
			}
			opts.Format = fmtVal
		case arg == "--strict":
			opts.Strict = true
		case arg == "--all":
			opts.All = true
		case arg == "--help" || arg == "-h":
			return opts, fmt.Errorf("usage: adrian audit [--strict] [--format text|json] [--all]")
		default:
			return opts, fmt.Errorf("unexpected argument %q", arg)
		}
	}
	return opts, nil
}

// AuditRepository performs codebase and rule validation.
func AuditRepository(adrDir string, opts Options) (*AuditReport, error) {
	adrs, err := adr.LoadADRs(adrDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load ADRs from %s: %w", adrDir, err)
	}

	gitRoot := resolveRepoRoot(adrDir)
	git := gitcontext.NewDefaultClient()
	git.SetGitRoot(gitRoot)

	trackedFiles := git.GetTrackedFiles()
	if len(trackedFiles) == 0 {
		trackedFiles = scanDirFallback(gitRoot)
	}

	report := &AuditReport{
		Summary: AuditSummary{
			TotalADRs:    len(adrs),
			TrackedFiles: len(trackedFiles),
			StatusCounts: make(map[string]int),
		},
	}

	fileContentCache := make(map[string]string)
	readFile := func(relPath string) string {
		if content, ok := fileContentCache[relPath]; ok {
			return content
		}
		absPath := filepath.Join(gitRoot, relPath)
		info, statErr := os.Lstat(absPath)
		if statErr != nil || info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 512*1024 {
			fileContentCache[relPath] = ""
			return ""
		}
		data, readErr := os.ReadFile(absPath)
		if readErr != nil {
			fileContentCache[relPath] = ""
			return ""
		}
		s := string(data)
		fileContentCache[relPath] = s
		return s
	}

	var issues []AuditIssue
	ids := make(map[string]bool)

	for _, a := range adrs {
		statusStr := string(a.Status)
		if statusStr == "" {
			statusStr = "unknown"
		}
		report.Summary.StatusCounts[statusStr]++

		normID := adr.NormalizeID(a.ID)
		if ids[normID] {
			issues = append(issues, AuditIssue{
				ADRID:    normID,
				ADRTitle: a.Title,
				Code:     "DUPLICATE_ID",
				Severity: "error",
				Message:  fmt.Sprintf("duplicate ADR ID %s", normID),
			})
		}
		ids[normID] = true

		// Check raw frontmatter validation
		if raw, err := os.ReadFile(a.FilePath); err == nil {
			if ferr := adr.ValidateFrontmatter(string(raw)); ferr != nil {
				issues = append(issues, AuditIssue{
					ADRID:    normID,
					ADRTitle: a.Title,
					Code:     "MALFORMED_FRONTMATTER",
					Severity: "error",
					Message:  ferr.Error(),
				})
			}
		}

		// By default only audit active rules (Accepted or Proposed) unless --all is specified
		isActive := a.Status == adr.StatusAccepted || a.Status == adr.StatusProposed
		if !opts.All && !isActive {
			continue
		}
		report.Summary.ActiveADRs++

		// 1. Evaluate applies_to against tracked files
		var matchedFiles []string
		for _, f := range trackedFiles {
			if patternmatcher.MatchesADR(f, a) {
				matchedFiles = append(matchedFiles, f)
			}
		}

		// Check for dormant applies_to
		if len(matchedFiles) == 0 {
			patternsDesc := strings.Join(a.AppliesTo, ", ")
			issues = append(issues, AuditIssue{
				ADRID:    normID,
				ADRTitle: a.Title,
				Code:     "DORMANT_APPLIES_TO",
				Severity: "warning",
				Message:  fmt.Sprintf("matches 0 tracked files in repository with applies_to: [%s]", patternsDesc),
			})
			continue
		}

		// 2. Check dead pre-filter tokens
		if len(a.PreFilter) > 0 {
			anyFileMatchedPrefilter := false
			for _, mf := range matchedFiles {
				content := readFile(mf)
				if content == "" {
					continue
				}
				if prefilter.MatchesAny(a.PreFilter, content) {
					anyFileMatchedPrefilter = true
					break
				}
			}
			if !anyFileMatchedPrefilter {
				patternsDesc := strings.Join(a.PreFilter, ", ")
				issues = append(issues, AuditIssue{
					ADRID:    normID,
					ADRTitle: a.Title,
					Code:     "DEAD_PRE_FILTER",
					Severity: "warning",
					Message: fmt.Sprintf("pre_filter [%s] does not match any of the %d files targeted by applies_to (ADR will never trigger in review)",
						patternsDesc, len(matchedFiles)),
				})
			}
		}

		// 3. Check for complex ADRs with broad file targeting but no pre_filter
		if a.Complexity == adr.ComplexityComplex && len(a.PreFilter) == 0 && len(matchedFiles) >= 25 {
			issues = append(issues, AuditIssue{
				ADRID:    normID,
				ADRTitle: a.Title,
				Code:     "MISSING_PRE_FILTER",
				Severity: "info",
				Message: fmt.Sprintf("rule is marked complex and targets %d files without a pre_filter (will trigger heavy model calls on every PR touching these files)",
					len(matchedFiles)),
			})
		}

		// 4. Check for unbounded whole-file context
		if a.DiffContextMode == adr.DiffContextFull && len(a.PreFilter) == 0 && len(matchedFiles) >= 50 {
			issues = append(issues, AuditIssue{
				ADRID:    normID,
				ADRTitle: a.Title,
				Code:     "UNBOUNDED_FULL_CONTEXT",
				Severity: "warning",
				Message: fmt.Sprintf("rule requests whole-file context across %d files without a pre_filter (risk of high token usage)",
					len(matchedFiles)),
			})
		}
	}

	for _, issue := range issues {
		switch issue.Severity {
		case "error":
			report.Summary.ErrorCount++
		case "warning":
			report.Summary.WarningCount++
		case "info":
			report.Summary.InfoCount++
		}
	}
	report.Issues = issues
	report.Summary.Passed = report.Summary.ErrorCount == 0 && (!opts.Strict || report.Summary.WarningCount == 0)

	return report, nil
}

func resolveRepoRoot(dir string) string {
	d := dir
	for {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return filepath.Dir(dir)
}

func scanDirFallback(root string) []string {
	var files []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			base := info.Name()
			if base == ".git" || base == "node_modules" || base == "vendor" || base == "dist" || base == "build" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr == nil {
			files = append(files, rel)
		}
		return nil
	})
	return files
}

func renderText(report *AuditReport, out io.Writer) {
	fmt.Fprintln(out, "ADRian Repository Audit")
	fmt.Fprintln(out, "=======================")
	fmt.Fprintf(out, "Total ADRs: %d (Active: %d) | Tracked Files: %d\n",
		report.Summary.TotalADRs, report.Summary.ActiveADRs, report.Summary.TrackedFiles)

	var statuses []string
	for s := range report.Summary.StatusCounts {
		statuses = append(statuses, s)
	}
	sort.Strings(statuses)
	var statusParts []string
	for _, s := range statuses {
		statusParts = append(statusParts, fmt.Sprintf("%s: %d", s, report.Summary.StatusCounts[s]))
	}
	fmt.Fprintf(out, "Status breakdown: %s\n\n", strings.Join(statusParts, ", "))

	if len(report.Issues) == 0 {
		fmt.Fprintln(out, "✅ All ADRs healthy! No dormant rules, dead pre-filters, or unbounded contexts found.")
	} else {
		fmt.Fprintln(out, "Findings:")
		for _, issue := range report.Issues {
			icon := "ℹ️ "
			switch issue.Severity {
			case "error":
				icon = "❌"
			case "warning":
				icon = "⚠️ "
			}
			fmt.Fprintf(out, " %s [%s] ADR-%s (%s): %s\n",
				icon, issue.Code, issue.ADRID, issue.ADRTitle, issue.Message)
		}
	}

	fmt.Fprintln(out)
	fmt.Fprintf(out, "Summary: %d error(s), %d warning(s), %d advisory(ies)\n",
		report.Summary.ErrorCount, report.Summary.WarningCount, report.Summary.InfoCount)
	if report.Summary.Passed {
		fmt.Fprintln(out, "Result: PASSED")
	} else {
		fmt.Fprintln(out, "Result: FAILED")
	}
}
