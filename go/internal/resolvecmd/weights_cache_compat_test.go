package resolvecmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// This is the controlled Node-produced wire shape used by the current CRM
// wrapper qualification, not a sample of real customer finding history.
const nodeWeightsCache = `{
  "source":"review.finding event beads",
  "window":{"from":"2026-08-28","to":"2026-10-27","days":3,"newest":"2026-10-27"},
  "corpus":{"findings":120,"prs":17,"rules":3},
  "buckets":{"src/components/**":{"findings":120,"rules":[
    {"id":"ADR-0014","prs":17,"findings":40,"days_active":3,"last_seen":"2026-10-27"},
    {"id":"ADR-0024","prs":17,"findings":40,"days_active":3,"last_seen":"2026-10-27"},
    {"id":"ADR-0102","prs":17,"findings":40,"days_active":3,"last_seen":"2026-10-27"}
  ]}}
}`

func TestNodeWeightsCachePreservesSuppliedEvidenceWindow(t *testing.T) {
	repo, cache, log := scopedWeightsFixture(t, nil, "exit 91")
	if err := os.WriteFile(cache, []byte(nodeWeightsCache), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REVIEW_WEIGHTS_OFFLINE", "1")
	weights, err := LoadWeights(repo)
	if err != nil || weights == nil {
		t.Fatalf("valid Node cache refused: %#v, %v", weights, err)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("offline cache unexpectedly queried a store: %v", err)
	}
	raw, err := json.Marshal(weights.Window)
	if err != nil {
		t.Fatal(err)
	}
	var window map[string]any
	if err := json.Unmarshal(raw, &window); err != nil {
		t.Fatal(err)
	}
	for key, expected := range map[string]any{
		"from": "2026-08-28", "to": "2026-10-27", "days": float64(3),
		"newest": "2026-10-27", "corpus_findings": float64(120),
		"origin": "cache", "stale": true,
	} {
		if window[key] != expected {
			t.Errorf("cache window %s = %#v, want %#v", key, window[key], expected)
		}
	}
}

func TestRankingJoinsCacheRuleIDsToBareAcceptedCandidates(t *testing.T) {
	var weights FindingWeightsData
	if err := json.Unmarshal([]byte(nodeWeightsCache), &weights); err != nil {
		t.Fatal(err)
	}
	files := []string{"src/components/Accordion.tsx"}
	candidates := []ResolvedEntry{
		{ID: "0014", Title: "Mobile", Files: files},
		{ID: "0024", Title: "Buttons", Files: files},
		{ID: "0102", Title: "Boundary", Files: files},
	}
	result := RankRules(candidates, &weights, files, nil)
	if len(result.Ranked) != 3 || len(result.Untriggered) != 0 {
		t.Fatalf("cached evidence lost or counted twice: ranked=%#v untriggered=%#v", result.Ranked, result.Untriggered)
	}
	for i, expected := range []string{"0014", "0024", "0102"} {
		if result.Ranked[i].ID != expected || result.Ranked[i].PRs != 17 || result.Ranked[i].Findings != 40 {
			t.Errorf("rank %d = %#v, want %s/17 PRs/40 findings", i, result.Ranked[i], expected)
		}
	}
	if candidates[0].ID != "0014" {
		t.Fatal("ranking mutated raw resolver IDs used by unranked output")
	}
}

func TestEmptyNodeCacheIsKnownZeroRatherThanUnavailable(t *testing.T) {
	repo, cache, log := scopedWeightsFixture(t, nil, "exit 91")
	raw := `{"window":{"from":"2026-08-28","to":"2026-10-27","days":0,"newest":null},"corpus":{"findings":0},"buckets":{}}`
	if err := os.WriteFile(cache, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REVIEW_WEIGHTS_OFFLINE", "1")
	weights, err := LoadWeights(repo)
	if err != nil || weights == nil {
		t.Fatalf("a valid zero-finding cache became unavailable: %#v, %v", weights, err)
	}
	encoded, err := json.Marshal(weights.Window)
	if err != nil {
		t.Fatal(err)
	}
	var window map[string]any
	if err := json.Unmarshal(encoded, &window); err != nil {
		t.Fatal(err)
	}
	if window["corpus_findings"] != float64(0) || window["days"] != float64(0) || window["stale"] != true {
		t.Fatalf("lost supplied zero-finding provenance: %s", encoded)
	}
	if newest, supplied := window["newest"]; !supplied || newest != nil {
		t.Fatalf("known absence of a newest finding was changed: %s", encoded)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("offline zero-finding cache queried a store: %v", err)
	}
}

func TestConflictingCacheTotalsAreNotSilentlySelected(t *testing.T) {
	repo, cache, log := scopedWeightsFixture(t, nil, "exit 91")
	var input map[string]any
	if err := json.Unmarshal([]byte(nodeWeightsCache), &input); err != nil {
		t.Fatal(err)
	}
	input["window"].(map[string]any)["corpus_findings"] = 99
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cache, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REVIEW_WEIGHTS_OFFLINE", "1")
	if weights, err := LoadWeights(repo); weights != nil || err == nil {
		t.Fatalf("conflicting 99/120 totals became trusted: %#v, %v", weights, err)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("offline conflicting cache queried a store: %v", err)
	}
}

func TestLegacyNativeCacheDoesNotInventMissingWindowFacts(t *testing.T) {
	repo, cache, log := scopedWeightsFixture(t, nil, "exit 91")
	raw := `{"window":{"from":"2026-10-01","to":"2026-10-27","corpus_findings":7,"origin":"store"},"buckets":{"src/components/**":{"rules":[]}}}`
	if err := os.WriteFile(cache, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REVIEW_WEIGHTS_OFFLINE", "1")
	weights, err := LoadWeights(repo)
	if err != nil || weights == nil {
		t.Fatalf("existing native cache refused: %#v, %v", weights, err)
	}
	encoded, err := json.Marshal(weights.Window)
	if err != nil {
		t.Fatal(err)
	}
	var window map[string]any
	if err := json.Unmarshal(encoded, &window); err != nil {
		t.Fatal(err)
	}
	if window["corpus_findings"] != float64(7) || window["origin"] != "cache" || window["stale"] != true {
		t.Fatalf("existing cache provenance lost: %s", encoded)
	}
	for _, missing := range []string{"days", "newest"} {
		if _, supplied := window[missing]; supplied {
			t.Errorf("invented unknown %s in %s", missing, encoded)
		}
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("offline native cache queried a store: %v", err)
	}
}

func TestCacheWithoutKnownTotalIsUnavailableNotUnseeded(t *testing.T) {
	for _, raw := range []string{"{}", "null", `{"buckets":{"src/components/**":{"rules":[]}}}`} {
		t.Run(raw, func(t *testing.T) {
			repo, cache, log := scopedWeightsFixture(t, nil, "exit 91")
			if err := os.WriteFile(cache, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("REVIEW_WEIGHTS_OFFLINE", "1")
			if weights, err := LoadWeights(repo); weights != nil || err == nil {
				t.Fatalf("unknown cache became a known empty corpus: %#v, %v", weights, err)
			}
			if _, err := os.Stat(log); !os.IsNotExist(err) {
				t.Fatalf("offline unavailable cache queried a store: %v", err)
			}
		})
	}
}

func TestRankingCanonicalKeysCannotGrantApplicabilityOrCrossBuckets(t *testing.T) {
	weights := &FindingWeightsData{Buckets: map[string]FindingWeightsBucket{
		"src/components/**": {Rules: []FindingWeightsEntry{
			{ID: "ADR-0014", PRs: 17, Findings: 40, DaysActive: 3},
			{ID: "ADR-9999", PRs: 999, Findings: 999, DaysActive: 30},
		}},
		"server/api/**": {Rules: []FindingWeightsEntry{{ID: "lens#91", PRs: 999, Findings: 999, DaysActive: 30}}},
	}}
	candidates := []ResolvedEntry{{ID: "0014", Title: "Mobile"}}
	files := []string{"src/components/Accordion.tsx"}
	result := RankRules(candidates, weights, files, map[string]string{"lens#91": "A server lens"})
	if len(result.Ranked) != 1 || result.Ranked[0].ID != "0014" {
		t.Fatalf("foreign applicability or bucket leaked into ranking: %#v", result.Ranked)
	}
	bucket := weights.Buckets["src/components/**"]
	bucket.Rules = append(bucket.Rules, FindingWeightsEntry{ID: "lens#91", PRs: 3, Findings: 3, DaysActive: 3})
	weights.Buckets["src/components/**"] = bucket
	result = RankRules(candidates, weights, files, map[string]string{"lens#91": "A local lens"})
	if len(result.Ranked) != 2 || result.Ranked[1].ID != "lens#91" || result.Ranked[1].Kind != "lens" || result.Ranked[1].Title != "A local lens" {
		t.Fatalf("existing bucket-local lens contract lost: %#v", result.Ranked)
	}
}

func TestRankingCanonicalJoinRetainsMaximumObservedDays(t *testing.T) {
	weights := &FindingWeightsData{Buckets: map[string]FindingWeightsBucket{
		"src/components/**": {Rules: []FindingWeightsEntry{{ID: "ADR-0001", PRs: 10, Findings: 20, DaysActive: 1}}},
		"server/api/**": {Rules: []FindingWeightsEntry{
			{ID: "0001", PRs: 1, Findings: 1, DaysActive: 5},
			{ID: "ADR-0002", PRs: 50, Findings: 50, DaysActive: 2},
		}},
	}}
	result := RankRules([]ResolvedEntry{{ID: "0001"}, {ID: "ADR-0002"}}, weights,
		[]string{"src/components/Accordion.tsx", "server/api/route.ts"}, nil)
	if len(result.Ranked) != 2 || result.Ranked[0].ID != "0001" || result.Ranked[0].PRs != 10 || result.Ranked[0].DaysActive != 5 {
		t.Fatalf("canonical join turned a sustained observation into a burst: %#v", result.Ranked)
	}
}

func TestFrontloadWireTopNConservesAllAcceptedRules(t *testing.T) {
	repo, cache, log := scopedWeightsFixture(t, nil, "exit 91")
	adrDir := filepath.Join(repo, "doc", "adr")
	if err := os.MkdirAll(adrDir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"0005", "0014", "0024", "0102"} {
		body := fmt.Sprintf("---\nstatus: accepted\napplies_to:\n  - 'src/components/**'\n---\n\n# %s. Controlled rule\n", id)
		if err := os.WriteFile(filepath.Join(adrDir, id+"-controlled.md"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(cache, []byte(nodeWeightsCache), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REVIEW_WEIGHTS_OFFLINE", "1")
	for _, top := range []int{1, 3, 9} {
		var stdout, stderr bytes.Buffer
		err := RunWithRoot([]string{"--ranked", "--json", "--top", strconv.Itoa(top), "src/components/Accordion.tsx"}, adrDir, repo, &stdout, &stderr)
		if err != nil {
			t.Fatalf("top=%d refused: %v (%s)", top, err, stderr.String())
		}
		var record FrontloadRecord
		if err := json.Unmarshal(stdout.Bytes(), &record); err != nil {
			t.Fatal(err)
		}
		seen := make(map[string]bool)
		add := func(id string) {
			if seen[id] || !strings.HasPrefix(id, "ADR-") || strings.Contains(id, "ADR-ADR-") {
				t.Fatalf("top=%d duplicate/noncanonical ID %q in %s", top, id, stdout.String())
			}
			seen[id] = true
		}
		for _, row := range record.FrontLoaded {
			add(row.ID)
		}
		for _, id := range record.AlsoApplies {
			add(id)
		}
		for _, row := range record.NeverFlaggedHere {
			add(row.ID)
		}
		if len(seen) != 4 || !seen["ADR-0005"] || !seen["ADR-0014"] || !seen["ADR-0024"] || !seen["ADR-0102"] {
			t.Fatalf("top=%d changed the applicable universe: %s", top, stdout.String())
		}
		var wire struct {
			FrontLoaded []map[string]json.RawMessage `json:"front_loaded"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &wire); err != nil {
			t.Fatal(err)
		}
		for _, row := range wire.FrontLoaded {
			if _, exists := row["files"]; exists {
				t.Fatalf("frontload record leaked full file inventory: %s", stdout.String())
			}
		}
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("offline ranking queried a store: %v", err)
	}
}
