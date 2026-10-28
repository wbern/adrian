package resolvecmd

import (
	"sort"
	"strings"
)

// BucketFor maps a file path to its mechanical review bucket.
// Stylesheets map to **/*.css; other files map to the first two directory segments.
func BucketFor(file string) string {
	f := strings.ReplaceAll(file, "\\", "/")
	if strings.HasSuffix(f, ".css") {
		return "**/*.css"
	}
	parts := strings.Split(f, "/")
	var dirs []string
	for i := 0; i < len(parts)-1; i++ {
		if parts[i] != "" {
			dirs = append(dirs, parts[i])
		}
	}
	if len(dirs) == 0 {
		return "(repo root)"
	}
	if len(dirs) == 1 {
		return dirs[0] + "/**"
	}
	return dirs[0] + "/" + dirs[1] + "/**"
}

// BucketsFor collects unique, sorted bucket patterns for a list of files.
func BucketsFor(files []string) []string {
	seen := make(map[string]bool)
	for _, f := range files {
		b := BucketFor(f)
		if b != "" {
			seen[b] = true
		}
	}
	out := make([]string, 0, len(seen))
	for b := range seen {
		out = append(out, b)
	}
	sort.Strings(out)
	return out
}

type mergedObservation struct {
	FindingWeightsEntry
	daysActive int
}

func isStrongerObservation(candidate, previous *FindingWeightsEntry) bool {
	if previous == nil {
		return true
	}
	if candidate.PRs != previous.PRs {
		return candidate.PRs > previous.PRs
	}
	return candidate.Findings > previous.Findings
}

func observedRules(weights *FindingWeightsData, buckets []string) map[string]*mergedObservation {
	merged := make(map[string]*mergedObservation)
	if weights == nil || weights.Buckets == nil {
		return merged
	}

	for _, b := range buckets {
		bucketData, ok := weights.Buckets[b]
		if !ok {
			continue
		}
		for _, r := range bucketData.Rules {
			if r.ID == "" {
				continue
			}
			r.ID = rankingRuleID(r.ID)
			prev, exists := merged[r.ID]
			daysActive := r.DaysActive
			if exists && prev.daysActive > daysActive {
				daysActive = prev.daysActive
			}

			ruleCopy := r
			ruleCopy.Bucket = b
			if !exists || isStrongerObservation(&r, &prev.FindingWeightsEntry) {
				merged[r.ID] = &mergedObservation{
					FindingWeightsEntry: ruleCopy,
					daysActive:          daysActive,
				}
			} else {
				prev.daysActive = daysActive
			}
		}
	}
	return merged
}

// Ranking joins the resolver's bare ADR IDs with finding records' citation IDs.
// Other rule namespaces, including legacy lenses, retain their own identity.
func rankingRuleID(id string) string {
	if len(id) != 4 {
		return id
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return id
		}
	}
	return "ADR-" + id
}

func isBurst(obs *mergedObservation) bool {
	return obs.daysActive <= BurstDaysThreshold
}

type RankResult struct {
	Ranked      []RankedRule
	Untriggered []Untriggered
	Buckets     []string
	Weighted    bool
}

// RankRules orders candidate ADRs and observed lenses by empirical blocking frequency.
func RankRules(adrCandidates []ResolvedEntry, weights *FindingWeightsData, files []string, lensTitles map[string]string) RankResult {
	buckets := BucketsFor(files)
	candidateMap := make(map[string]ResolvedEntry, len(adrCandidates))
	for _, a := range adrCandidates {
		candidateMap[rankingRuleID(a.ID)] = a
	}

	if weights == nil {
		untriggered := make([]Untriggered, len(adrCandidates))
		for i, a := range adrCandidates {
			untriggered[i] = Untriggered{ID: a.ID, Title: a.Title}
		}
		return RankResult{
			Ranked:      nil,
			Untriggered: untriggered,
			Buckets:     buckets,
			Weighted:    false,
		}
	}

	observed := observedRules(weights, buckets)
	var ranked []RankedRule

	for id, obs := range observed {
		isLens := strings.HasPrefix(id, "lens#")
		if !isLens {
			// An ADR only competes if its own globs put it on this diff
			if _, ok := candidateMap[id]; !ok {
				continue
			}
		}

		title := id
		outputID := id
		var ruleFiles []string
		kind := "adr"
		if isLens {
			kind = "lens"
			if t, ok := lensTitles[id]; ok && t != "" {
				title = t
			}
		} else {
			cand := candidateMap[id]
			outputID = cand.ID
			title = cand.Title
			ruleFiles = cand.Files
		}

		ranked = append(ranked, RankedRule{
			ID:         outputID,
			Title:      title,
			Kind:       kind,
			Bucket:     obs.Bucket,
			PRs:        obs.PRs,
			Findings:   obs.Findings,
			DaysActive: obs.daysActive,
			LastSeen:   obs.LastSeen,
			Files:      ruleFiles,
		})
	}

	// Sort rules:
	// 1. Non-bursts come first (isBurst = daysActive <= 2)
	// 2. PRs descending
	// 3. Findings descending
	// 4. ID ascending
	sort.Slice(ranked, func(i, j int) bool {
		a := ranked[i]
		b := ranked[j]
		obsA := observed[rankingRuleID(a.ID)]
		obsB := observed[rankingRuleID(b.ID)]

		burstA := isBurst(obsA)
		burstB := isBurst(obsB)
		if burstA != burstB {
			return !burstA // non-burst (false) comes before burst (true)
		}
		if a.PRs != b.PRs {
			return a.PRs > b.PRs
		}
		if a.Findings != b.Findings {
			return a.Findings > b.Findings
		}
		return rankingRuleID(a.ID) < rankingRuleID(b.ID)
	})

	for i := range ranked {
		ranked[i].Rank = i + 1
	}

	rankedIDs := make(map[string]bool, len(ranked))
	for _, r := range ranked {
		rankedIDs[rankingRuleID(r.ID)] = true
	}

	var untriggered []Untriggered
	for _, a := range adrCandidates {
		if !rankedIDs[rankingRuleID(a.ID)] {
			untriggered = append(untriggered, Untriggered{
				ID:    a.ID,
				Title: a.Title,
			})
		}
	}

	return RankResult{
		Ranked:      ranked,
		Untriggered: untriggered,
		Buckets:     buckets,
		Weighted:    true,
	}
}
