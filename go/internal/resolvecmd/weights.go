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
	"time"
)

const (
	DefaultWindowDays   = 60
	DefaultCacheFile    = "node_modules/.cache/review-finding-weights.json"
	ThinCorpusThreshold = 100
	StaleWarnDays       = 14
)

// LoadWeights tries to load weights from the cache file first, or queries Dolt/Beads if needed.
func LoadWeights(repoRoot string) (*FindingWeightsData, error) {
	cachePath := os.Getenv("REVIEW_WEIGHTS_CACHE")
	if cachePath == "" {
		cachePath = filepath.Join(repoRoot, DefaultCacheFile)
	}

	offline := os.Getenv("REVIEW_WEIGHTS_OFFLINE") == "1"

	// Try reading cache
	data, err := os.ReadFile(cachePath)
	if err == nil {
		var weights FindingWeightsData
		if jsonErr := json.Unmarshal(data, &weights); jsonErr == nil && len(weights.Buckets) > 0 {
			weights.Window.Origin = "cache"
			return &weights, nil
		}
	}

	if offline {
		return nil, fmt.Errorf("offline mode and cache unavailable at %s", cachePath)
	}

	// Try querying gc bd sql if available
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

// QueryStore queries review.finding event beads via gc bd sql.
func QueryStore(repoRoot string, windowDays int) (*FindingWeightsData, error) {
	if _, err := exec.LookPath("gc"); err != nil {
		return nil, fmt.Errorf("gc CLI not found")
	}

	since := time.Now().UTC().AddDate(0, 0, -windowDays).Format("2006-01-02")
	sql := fmt.Sprintf(`SELECT payload->>'$.rule_id' AS rule_id, payload->>'$.file' AS file, payload->>'$.pr' AS pr, payload->>'$.occurred_at' AS day, payload->>'$.visual_category' AS visual_category, payload->>'$.surface' AS surface, payload->>'$.viewport' AS viewport FROM issues WHERE issue_type='event' AND event_kind='review.finding' AND payload IS NOT NULL AND payload <> '' AND payload->>'$.occurred_at' >= '%s'`, since)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "gc", "bd", "sql", "--json", sql)
	cmd.Dir = repoRoot
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gc bd sql: %w (%s)", err, stderr.String())
	}

	var rows []RawFindingRow
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		return nil, fmt.Errorf("decode sql result: %w", err)
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
	var (
		earliestDay string
		latestDay   string
		totalCount  = len(rows)
	)

	for _, r := range rows {
		if r.RuleID == "" || r.File == "" {
			continue
		}
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
		ruleStats := buckets[b][r.RuleID]
		if ruleStats == nil {
			ruleStats = &bucketRuleStats{
				ruleID: r.RuleID,
				prs:    make(map[string]bool),
				days:   make(map[string]bool),
			}
			buckets[b][r.RuleID] = ruleStats
		}

		prStr := r.PRString()
		if prStr != "" {
			ruleStats.prs[prStr] = true
		}
		ruleStats.findings++
		if day != "" {
			ruleStats.days[day] = true
			if ruleStats.lastSeen == "" || day > ruleStats.lastSeen {
				ruleStats.lastSeen = day
			}
		}
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

	return &FindingWeightsData{
		Window: WeightsWindow{
			From:           earliestDay,
			To:             latestDay,
			CorpusFindings: totalCount,
			Origin:         origin,
		},
		Buckets: resultBuckets,
	}
}
