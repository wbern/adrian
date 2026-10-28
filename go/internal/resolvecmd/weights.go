package resolvecmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	DefaultWindowDays   = 60
	DefaultCacheFile    = ".adrian/cache/weights.json"
	LegacyCacheFile     = "node_modules/.cache/review-finding-weights.json"
	ThinCorpusThreshold = 100
	StaleWarnDays       = 14
)

// decodeWeightsCache accepts the Node cache and the existing native cache.
// A supplied total is required: missing evidence is not a zero-finding corpus.
func decodeWeightsCache(data []byte, origin string) (*FindingWeightsData, error) {
	var weights FindingWeightsData
	if err := json.Unmarshal(data, &weights); err != nil {
		return nil, err
	}
	if weights.Buckets == nil {
		return nil, fmt.Errorf("finding cache requires a buckets object")
	}
	var totals struct {
		Window struct {
			Findings *int `json:"corpus_findings"`
		} `json:"window"`
		Corpus *struct {
			Findings *int `json:"findings"`
		} `json:"corpus"`
	}
	if err := json.Unmarshal(data, &totals); err != nil {
		return nil, err
	}
	count := totals.Window.Findings
	if totals.Corpus != nil {
		if totals.Corpus.Findings == nil {
			return nil, fmt.Errorf("finding cache corpus total is missing")
		}
		if count != nil && *count != *totals.Corpus.Findings {
			return nil, fmt.Errorf("finding cache corpus totals disagree")
		}
		count = totals.Corpus.Findings
	}
	if count == nil || *count < 0 {
		return nil, fmt.Errorf("finding cache requires a nonnegative known total")
	}
	if weights.Window.Days != nil && *weights.Window.Days < 0 {
		return nil, fmt.Errorf("finding cache day count must be nonnegative")
	}
	if len(weights.Window.Newest) > 0 {
		var newest *string
		if err := json.Unmarshal(weights.Window.Newest, &newest); err != nil {
			return nil, fmt.Errorf("finding cache newest must be a string or null: %w", err)
		}
	}
	weights.Window.CorpusFindings = *count
	weights.Window.Origin = origin
	stale := true // Reading a cache/static file is not a fresh store query.
	weights.Window.Stale = &stale
	return &weights, nil
}

// LoadWeights tries to load weights from the cache file first, repo static file, or queries store if needed.
func LoadWeights(repoRoot string) (*FindingWeightsData, error) {
	cachePath := os.Getenv("REVIEW_WEIGHTS_CACHE")
	if cachePath == "" {
		candidate := filepath.Join(repoRoot, DefaultCacheFile)
		if _, err := os.Stat(candidate); err == nil {
			cachePath = candidate
		} else {
			legacyCandidate := filepath.Join(repoRoot, LegacyCacheFile)
			if _, lErr := os.Stat(legacyCandidate); lErr == nil {
				cachePath = legacyCandidate
			} else {
				cachePath = candidate
			}
		}
	}

	offline := os.Getenv("REVIEW_WEIGHTS_OFFLINE") == "1"

	// Try reading cache
	data, err := os.ReadFile(cachePath)
	if err == nil {
		if weights, decodeErr := decodeWeightsCache(data, "cache"); decodeErr == nil {
			return weights, nil
		}
	}

	// Try reading repo-level static weights file if present (.adrian/weights.json or doc/adr/weights.json)
	for _, staticPath := range []string{
		filepath.Join(repoRoot, ".adrian/weights.json"),
		filepath.Join(repoRoot, "doc/adr/weights.json"),
		filepath.Join(repoRoot, "docs/adr/weights.json"),
	} {
		if sData, sErr := os.ReadFile(staticPath); sErr == nil {
			if weights, decodeErr := decodeWeightsCache(sData, "file"); decodeErr == nil {
				return weights, nil
			}
		}
	}

	if offline {
		return nil, fmt.Errorf("offline mode and cache unavailable at %s", cachePath)
	}

	// Try querying store if available
	weights, err := QueryStore(repoRoot, DefaultWindowDays)
	if err == nil && weights != nil {
		// Attempt saving to cache
		if b, mErr := json.MarshalIndent(weights, "", "  "); mErr == nil {
			_ = os.MkdirAll(filepath.Dir(cachePath), 0755)
			_ = os.WriteFile(cachePath, b, 0644)
		}
		return weights, nil
	}

	return nil, fmt.Errorf("weights unavailable: %w", err)
}

// LegacyRuleAliases maps legacy review lens identifiers to their unified canonical ADR IDs.
var LegacyRuleAliases = map[string]string{
	"lens#1":  "ADR-0102",
	"lens#2":  "ADR-0103",
	"lens#3":  "ADR-0104",
	"lens#4":  "ADR-0105",
	"lens#5":  "ADR-0106",
	"lens#6":  "ADR-0107",
	"lens#7":  "ADR-0108",
	"lens#8":  "ADR-0109",
	"lens#9":  "ADR-0110",
	"lens#10": "ADR-0111",
	"lens#11": "ADR-0112",
	"lens#12": "ADR-0068",
	"lens#13": "ADR-0088",
}

// CanonicalRuleID maps legacy review lens tokens to canonical ADR IDs.
func CanonicalRuleID(rawID string) string {
	if canon, ok := LegacyRuleAliases[rawID]; ok {
		return canon
	}
	return rawID
}

// QueryStore queries review.finding event beads via gc bd sql.
func QueryStore(repoRoot string, windowDays int) (*FindingWeightsData, error) {
	if _, err := exec.LookPath("gc"); err != nil {
		return nil, fmt.Errorf("finding weights not configured (no local cache or finding store)")
	}

	since := time.Now().UTC().AddDate(0, 0, -windowDays).Format("2006-01-02")
	sql := fmt.Sprintf(`SELECT payload->>'$.rule_id' AS rule_id, payload->>'$.file' AS file, payload->>'$.pr' AS pr, payload->>'$.occurred_at' AS day, payload->>'$.visual_category' AS visual_category, payload->>'$.surface' AS surface, payload->>'$.viewport' AS viewport FROM issues WHERE issue_type='event' AND event_kind='review.finding' AND payload IS NOT NULL AND payload <> '' AND payload->>'$.occurred_at' >= '%s'`, since)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	rig := os.Getenv("GC_RIG")
	if rig == "" {
		if cmdOut, err := exec.Command("git", "-C", repoRoot, "remote", "get-url", "origin").Output(); err == nil {
			url := strings.TrimSpace(string(cmdOut))
			if strings.Contains(url, "/crm") || strings.HasSuffix(url, "/crm.git") {
				rig = "crm"
			} else if strings.Contains(url, "/gas-city-infra") || strings.HasSuffix(url, "/gas-city-infra.git") {
				rig = "gas-city-infra"
			}
		}
		if rig == "" {
			base := filepath.Base(repoRoot)
			if strings.HasPrefix(base, "crm") {
				rig = "crm"
			} else if strings.HasPrefix(base, "gas-city-infra") || strings.HasPrefix(base, "gci") {
				rig = "gas-city-infra"
			}
		}
	}

	if rig == "" {
		return nil, fmt.Errorf("finding weights not configured (no local cache or finding store)")
	}

	args := []string{"bd", "--rig", rig, "--allow-unbounded", "sql", "--json", sql}

	cmd := exec.CommandContext(ctx, "gc", args...)
	cmd.Dir = repoRoot
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gc bd sql for rig %s: %w (%s)", rig, err, stderr.String())
	}

	var rows []RawFindingRow
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		return nil, fmt.Errorf("decode sql result: %w", err)
	}
	if rows == nil {
		return nil, fmt.Errorf("decode sql result: expected an array of finding rows")
	}
	for i, row := range rows {
		if row.RuleID == "" || row.File == "" {
			return nil, fmt.Errorf("decode sql result: finding row %d lacks rule_id or file", i)
		}
	}

	return WeightsFromRows(rows, "store", since), nil
}

type bucketRuleStats struct {
	ruleID   string
	prs      map[string]bool
	findings int
	days     map[string]bool
	lastSeen string
}

// WeightsFromRows aggregates raw findings into path buckets.
func WeightsFromRows(rows []RawFindingRow, origin string, sinceDay string) *FindingWeightsData {
	buckets := make(map[string]map[string]*bucketRuleStats)
	observedDays := make(map[string]bool)
	var (
		earliestDay string
		latestDay   string
		totalCount  = len(rows)
	)

	for _, r := range rows {
		if r.RuleID == "" || r.File == "" {
			continue
		}
		ruleID := CanonicalRuleID(r.RuleID)
		day := r.Day
		if len(day) >= 10 {
			day = day[:10]
		}
		if earliestDay == "" || day < earliestDay {
			earliestDay = day
		}
		if latestDay == "" || day > latestDay {
			latestDay = day
		}

		b := BucketFor(r.File)
		if buckets[b] == nil {
			buckets[b] = make(map[string]*bucketRuleStats)
		}
		ruleStats := buckets[b][ruleID]
		if ruleStats == nil {
			ruleStats = &bucketRuleStats{
				ruleID: ruleID,
				prs:    make(map[string]bool),
				days:   make(map[string]bool),
			}
			buckets[b][ruleID] = ruleStats
		}

		prStr := r.PRString()
		if prStr != "" {
			ruleStats.prs[prStr] = true
		}
		ruleStats.findings++
		if day != "" {
			observedDays[day] = true
			ruleStats.days[day] = true
			if ruleStats.lastSeen == "" || day > ruleStats.lastSeen {
				ruleStats.lastSeen = day
			}
		}
	}

	newest := json.RawMessage("null")
	if latestDay != "" {
		newest, _ = json.Marshal(latestDay)
	}
	if earliestDay == "" {
		earliestDay = sinceDay
	}
	if latestDay == "" {
		latestDay = time.Now().UTC().Format("2006-01-02")
	}

	resultBuckets := make(map[string]FindingWeightsBucket)
	for b, rulesMap := range buckets {
		var rules []FindingWeightsEntry
		for ruleID, s := range rulesMap {
			var lastSeen *string
			if s.lastSeen != "" {
				ls := s.lastSeen
				lastSeen = &ls
			}
			rules = append(rules, FindingWeightsEntry{
				ID:         ruleID,
				PRs:        len(s.prs),
				Findings:   s.findings,
				DaysActive: len(s.days),
				LastSeen:   lastSeen,
			})
		}
		// Sort rules by PRs desc, findings desc, ID asc
		sort.Slice(rules, func(i, j int) bool {
			if rules[i].PRs != rules[j].PRs {
				return rules[i].PRs > rules[j].PRs
			}
			if rules[i].Findings != rules[j].Findings {
				return rules[i].Findings > rules[j].Findings
			}
			return rules[i].ID < rules[j].ID
		})

		resultBuckets[b] = FindingWeightsBucket{
			Bucket: b,
			Rules:  rules,
		}
	}

	dayCount := len(observedDays)
	stale := origin != "store"
	return &FindingWeightsData{
		Window: WeightsWindow{
			From:           earliestDay,
			To:             latestDay,
			Days:           &dayCount,
			Newest:         newest,
			CorpusFindings: totalCount,
			Origin:         origin,
			Stale:          &stale,
		},
		Buckets: resultBuckets,
	}
}
