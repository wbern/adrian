package resolvecmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wbern/adrian/go/internal/adr"
	"github.com/wbern/adrian/go/internal/patternmatcher"
)

// ErrSyntax is returned when CLI arguments are malformed (exit code 2).
var ErrSyntax = errors.New("syntax error")

// Run parses CLI options and resolves ADRs for changed or given files.
func Run(args []string, adrDir string, out io.Writer, errOut io.Writer) error {
	var (
		explicitFiles []string
		base          string
		staged        bool
		tag           string
		ranked        bool
		topN          = 3
		format        = "text"
		codeReviewDoc string
		repoURL       string
		customAdrDir  = adrDir
	)

	// Parse flags
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--staged":
			staged = true
		case arg == "--ranked":
			ranked = true
		case arg == "--json":
			format = "json"
		case arg == "--pr-block":
			format = "pr-block"
		case arg == "--format":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				i++
				format = args[i]
			}
		case arg == "--base":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				i++
				base = args[i]
			} else {
				return fmt.Errorf("adrian resolve: --base requires a commit/ref")
			}
		case arg == "--tag":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
				_, _ = fmt.Fprintln(errOut, "adrian resolve: --tag requires a name, e.g. --tag ux")
				return ErrSyntax
			}
			i++
			tag = args[i]
		case arg == "--top":
			if i+1 < len(args) {
				raw := args[i+1]
				n, err := strconv.Atoi(raw)
				if err != nil || n <= 0 {
					_, _ = fmt.Fprintf(errOut, "adrian resolve: ignoring --top %s (want a positive integer); using 3.\n", raw)
				} else {
					topN = n
					i++
				}
			}
		case arg == "--adr-dir":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				i++
				customAdrDir = args[i]
			}
		case arg == "--code-review":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				i++
				codeReviewDoc = args[i]
			}
		case arg == "--repo-url":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				i++
				repoURL = args[i]
			}
		case strings.HasPrefix(arg, "--"):
			// Unknown flag
			return fmt.Errorf("unknown flag: %s", arg)
		default:
			explicitFiles = append(explicitFiles, arg)
		}
	}

	// Determine repository root
	repoRoot := "."
	if customAdrDir != "" {
		repoRoot = filepath.Dir(filepath.Dir(customAdrDir))
	}
	runner := OSGitRunner{Dir: repoRoot}

	if repoURL == "" {
		repoURL = detectRepoURL(runner)
	}

	// 1. Resolve changed files
	files, err := GetChangedFiles(explicitFiles, staged, base, runner, errOut)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		_, _ = fmt.Fprintln(out, "No files given/changed — nothing to resolve.")
		return nil
	}

	// 2. Discover ADR directory if not provided
	if customAdrDir == "" {
		for _, candidate := range []string{filepath.Join(repoRoot, "doc/adr"), filepath.Join(repoRoot, "docs/adr")} {
			if fi, sErr := os.Stat(candidate); sErr == nil && fi.IsDir() {
				customAdrDir = candidate
				break
			}
		}
	}
	if customAdrDir == "" {
		return fmt.Errorf("adrian resolve: cannot find ADR directory (doc/adr or docs/adr)")
	}

	// 3. Load ADRs
	allADRs, err := adr.LoadADRs(customAdrDir)
	if err != nil {
		return err
	}

	// 4. Resolve matching ADRs for changed files
	rulebook := resolveADRsForFiles(allADRs, files)

	// 5. Apply --tag filter if present
	if tag != "" {
		before := len(rulebook.Enforced) + len(rulebook.Advisory)
		rulebook = applyTagFilter(rulebook, tag)
		after := len(rulebook.Enforced) + len(rulebook.Advisory)
		_, _ = fmt.Fprintf(errOut, "adrian resolve: --tag %s narrowed %d applicable ADR(s) to %d.\n", tag, before, after)
	}

	// 6. Check if empty matching
	if len(rulebook.Enforced) == 0 && len(rulebook.Advisory) == 0 {
		if format == "json" {
			_, _ = fmt.Fprintln(out, "{}")
			return nil
		}
		if format == "pr-block" {
			record := FrontloadRecord{
				Schema:       FrontloadSchema,
				ChangedFiles: len(files),
				Buckets:      BucketsFor(files),
			}
			_, _ = fmt.Fprintln(out, RenderFrontloadMarkdown(record, nil))
			return nil
		}
		_, _ = fmt.Fprintln(out, "No applicable ADRs for the given files.")
		return nil
	}

	// 7. Handle format and ranking
	if ranked || format != "text" {
		// Discover code review document if not given
		if codeReviewDoc == "" {
			for _, candidate := range []string{filepath.Join(repoRoot, "docs/code-review.md"), filepath.Join(repoRoot, "doc/code-review.md")} {
				if fi, sErr := os.Stat(candidate); sErr == nil && !fi.IsDir() {
					codeReviewDoc = candidate
					break
				}
			}
		}

		var lenses []LensRule
		lensTitles := make(map[string]string)
		refs := make(map[string]string)
		if codeReviewDoc != "" {
			if parsedLenses, lErr := ParseLenses(codeReviewDoc); lErr == nil {
				lenses = parsedLenses
				for _, l := range lenses {
					lensTitles[l.ID] = l.Title
					refs[l.ID] = fmt.Sprintf("%s/docs/code-review.md#%s", repoURL, l.Anchor)
				}
			}
		}

		// Build refs for ADRs
		for _, a := range allADRs {
			canonical := "ADR-" + a.CanonicalID()
			slug := filepath.Base(a.FilePath)
			refs[canonical] = fmt.Sprintf("%s/doc/adr/%s", repoURL, slug)
		}

		// Load finding weights
		weights, wErr := LoadWeights(repoRoot)
		if wErr != nil {
			_, _ = fmt.Fprintf(errOut, "adrian resolve: ranking unavailable (%s)\n", wErr)
		}

		rankResult := RankRules(rulebook.Enforced, weights, files, lensTitles)

		headCount := topN
		if headCount > len(rankResult.Ranked) {
			headCount = len(rankResult.Ranked)
		}
		frontLoaded := make([]RankedRule, 0, headCount)
		for i := 0; i < headCount; i++ {
			frontLoaded = append(frontLoaded, rankResult.Ranked[i])
		}

		alsoApplies := make([]string, 0)
		for _, r := range rankResult.Ranked[headCount:] {
			alsoApplies = append(alsoApplies, r.ID)
		}

		advisory := make([]string, 0)
		for _, a := range rulebook.Advisory {
			advisory = append(advisory, "ADR-"+a.ID)
		}

		var neverFlaggedHere []Untriggered
		for _, u := range rankResult.Untriggered {
			id := u.ID
			if !strings.HasPrefix(id, "ADR-") && !strings.HasPrefix(id, "lens#") {
				id = "ADR-" + id
			}
			neverFlaggedHere = append(neverFlaggedHere, Untriggered{
				ID:    id,
				Title: u.Title,
			})
		}

		var weightsWindow *WeightsWindow
		if weights != nil {
			weightsWindow = &weights.Window
		}

		record := FrontloadRecord{
			Schema:           FrontloadSchema,
			CorpusPartial:    weights != nil && weights.CorpusPartial,
			WeightsWindow:    weightsWindow,
			ChangedFiles:     len(files),
			Buckets:          rankResult.Buckets,
			FrontLoaded:      frontLoaded,
			AlsoApplies:      alsoApplies,
			NeverFlaggedHere: neverFlaggedHere,
			Advisory:         advisory,
		}

		switch format {
		case "json":
			b, _ := json.MarshalIndent(record, "", "  ")
			_, _ = fmt.Fprintln(out, string(b))
			return nil
		case "pr-block":
			_, _ = fmt.Fprintln(out, RenderFrontloadMarkdown(record, refs))
			return nil
		default:
			// text ranked format
			printRankedText(out, record, rankResult.Ranked, rankResult.Untriggered, rulebook.Advisory, topN)
			return nil
		}
	}

	// 8. Plain unranked text format
	printUnrankedText(out, rulebook)
	return nil
}

func resolveADRsForFiles(allADRs []adr.ADR, files []string) ResolvedRulebook {
	var enforced, advisory []ResolvedEntry

	for _, a := range allADRs {
		if a.Status != adr.StatusAccepted && a.Status != adr.StatusProposed {
			continue
		}

		var matchedFiles []string
		for _, f := range files {
			if patternmatcher.MatchesADR(f, a) {
				matchedFiles = append(matchedFiles, f)
			}
		}

		if len(matchedFiles) > 0 {
			entry := ResolvedEntry{
				ID:         a.CanonicalID(),
				Title:      a.Title,
				Files:      matchedFiles,
				Tags:       a.Tags,
				Complexity: a.Complexity,
				Status:     a.Status,
			}
			if a.Status == adr.StatusAccepted {
				enforced = append(enforced, entry)
			} else {
				advisory = append(advisory, entry)
			}
		}
	}
	return ResolvedRulebook{Enforced: enforced, Advisory: advisory}
}

func applyTagFilter(r ResolvedRulebook, tag string) ResolvedRulebook {
	hasTag := func(e ResolvedEntry) bool {
		for _, t := range e.Tags {
			if t == tag {
				return true
			}
		}
		return false
	}

	var enforced, advisory []ResolvedEntry
	for _, e := range r.Enforced {
		if hasTag(e) {
			enforced = append(enforced, e)
		}
	}
	for _, e := range r.Advisory {
		if hasTag(e) {
			advisory = append(advisory, e)
		}
	}
	return ResolvedRulebook{Enforced: enforced, Advisory: advisory}
}

func printUnrankedText(out io.Writer, r ResolvedRulebook) {
	if len(r.Enforced) > 0 {
		_, _ = fmt.Fprintln(out, "Accepted (enforced):")
		for _, e := range r.Enforced {
			_, _ = fmt.Fprintf(out, "  ADR-%s — %s\n", e.ID, e.Title)
			for _, f := range e.Files {
				_, _ = fmt.Fprintf(out, "    matched: %s\n", f)
			}
		}
	}
	if len(r.Advisory) > 0 {
		if len(r.Enforced) > 0 {
			_, _ = fmt.Fprintln(out)
		}
		_, _ = fmt.Fprintln(out, "Proposed (advisory):")
		for _, e := range r.Advisory {
			_, _ = fmt.Fprintf(out, "  ADR-%s — %s\n", e.ID, e.Title)
			for _, f := range e.Files {
				_, _ = fmt.Fprintf(out, "    matched: %s\n", f)
			}
		}
	}
}

func printRankedText(out io.Writer, record FrontloadRecord, allRanked []RankedRule, untriggered []Untriggered, advisory []ResolvedEntry, topN int) {
	windowStr := "unavailable"
	origin := "store"
	if record.WeightsWindow != nil {
		windowStr = fmt.Sprintf("%s..%s", record.WeightsWindow.From, record.WeightsWindow.To)
		if record.WeightsWindow.Origin != "" {
			origin = record.WeightsWindow.Origin
		}
	}

	_, _ = fmt.Fprintf(out, "Ranked by what has actually blocked PRs on these paths (%s, from %s):\n\n", windowStr, origin)

	headCount := topN
	if headCount > len(allRanked) {
		headCount = len(allRanked)
	}
	for i := 0; i < headCount; i++ {
		r := allRanked[i]
		lastStr := ""
		if r.LastSeen != nil && *r.LastSeen != "" {
			lastStr = fmt.Sprintf(", last %s", *r.LastSeen)
		}
		_, _ = fmt.Fprintf(out, "  %d. %s — %s\n     %s / %s on %s%s\n",
			r.Rank,
			r.ID,
			r.Title,
			Plural(r.PRs, "PR", "PRs"),
			Plural(r.Findings, "finding", "findings"),
			r.Bucket,
			lastStr,
		)
		if r.Kind == "lens" {
			_, _ = fmt.Fprintf(out, "     -> review lens in docs/code-review.md\n")
		}
	}

	lowerRanked := allRanked[headCount:]
	if len(lowerRanked) > 0 {
		var ids []string
		for _, r := range lowerRanked {
			ids = append(ids, r.ID)
		}
		_, _ = fmt.Fprintf(out, "\n  Also applies, less often seen here: %s\n", strings.Join(ids, ", "))
	}

	if len(untriggered) > 0 {
		_, _ = fmt.Fprintln(out, "\n  Applies, but has never been flagged on these paths:")
		for _, u := range untriggered {
			_, _ = fmt.Fprintf(out, "    %s — %s\n", u.ID, u.Title)
		}
	}

	if len(advisory) > 0 {
		var advIDs []string
		for _, a := range advisory {
			advIDs = append(advIDs, "ADR-"+a.ID)
		}
		_, _ = fmt.Fprintf(out, "\n  Proposed (advisory): %s\n", strings.Join(advIDs, ", "))
	}
}

func detectRepoURL(runner GitRunner) string {
	out, err := runner.RunGit("config", "--get", "remote.origin.url")
	if err != nil {
		return "https://github.com/wbern/crm/blob/main"
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return "https://github.com/wbern/crm/blob/main"
	}
	raw = strings.TrimSuffix(raw, ".git")
	if strings.HasPrefix(raw, "git@github.com:") {
		slug := strings.TrimPrefix(raw, "git@github.com:")
		return fmt.Sprintf("https://github.com/%s/blob/main", slug)
	}
	if strings.HasPrefix(raw, "https://github.com/") {
		return fmt.Sprintf("%s/blob/main", raw)
	}
	return "https://github.com/wbern/crm/blob/main"
}
