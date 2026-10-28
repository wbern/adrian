package resolvecmd

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/wbern/adrian/go/internal/adr"
)

// ResolvedEntry represents an ADR matched against a diff or file list.
type ResolvedEntry struct {
	ID         string         `json:"id"`
	Title      string         `json:"title"`
	Files      []string       `json:"files"`
	Tags       []string       `json:"tags,omitempty"`
	Complexity adr.Complexity `json:"complexity,omitempty"`
	Status     adr.Status     `json:"status"`
}

// ResolvedRulebook separates matching ADRs into enforced and advisory tiers.
type ResolvedRulebook struct {
	Enforced []ResolvedEntry `json:"enforced"`
	Advisory []ResolvedEntry `json:"advisory"`
}

// LensRule represents an invariant heading extracted from docs/code-review.md.
type LensRule struct {
	ID     string `json:"id"`     // e.g. "lens#2"
	Number int    `json:"number"` // e.g. 2
	Title  string `json:"title"`  // e.g. "Migrations are forward-only"
	Anchor string `json:"anchor"` // GitHub heading anchor
}

// WeightsWindow describes the observation window of the review findings corpus.
type WeightsWindow struct {
	From           string          `json:"from"`
	To             string          `json:"to"`
	Days           *int            `json:"days,omitempty"`
	Newest         json.RawMessage `json:"newest,omitempty"`
	CorpusFindings int             `json:"corpus_findings"`
	Origin         string          `json:"origin"` // "store", "cache" or "file"
	Stale          *bool           `json:"stale,omitempty"`
}

// FindingWeightsEntry contains the historical frequency of a rule on a path bucket.
type FindingWeightsEntry struct {
	ID         string  `json:"id"`
	PRs        int     `json:"prs"`
	Findings   int     `json:"findings"`
	DaysActive int     `json:"days_active"`
	LastSeen   *string `json:"last_seen,omitempty"`
	Bucket     string  `json:"bucket,omitempty"`
}

// FindingWeightsBucket groups observed rules under a path bucket pattern.
type FindingWeightsBucket struct {
	Bucket string                `json:"bucket"`
	Rules  []FindingWeightsEntry `json:"rules"`
}

// FindingWeightsData is the in-memory representation of finding frequencies.
type FindingWeightsData struct {
	Window        WeightsWindow                   `json:"window"`
	Buckets       map[string]FindingWeightsBucket `json:"buckets"`
	CorpusPartial bool                            `json:"corpus_partial"`
}

// RankedRule represents an ADR or lens ordered by empirical blocking frequency.
type RankedRule struct {
	Rank       int      `json:"rank"`
	ID         string   `json:"id"`
	Kind       string   `json:"kind"` // "adr" or "lens"
	Title      string   `json:"title"`
	Bucket     string   `json:"bucket"`
	PRs        int      `json:"prs"`
	Findings   int      `json:"findings"`
	DaysActive int      `json:"days_active"`
	LastSeen   *string  `json:"last_seen,omitempty"`
	Files      []string `json:"files,omitempty"`
}

// Untriggered is an ADR that matched globs but has 0 observations on the paths.
type Untriggered struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// FrontloadRecord is the canonical review-rule-frontload/1 schema.
type FrontloadRecord struct {
	Schema           string         `json:"schema"` // "review-rule-frontload/1"
	CorpusPartial    bool           `json:"corpus_partial"`
	WeightsWindow    *WeightsWindow `json:"weights_window"`
	ChangedFiles     int            `json:"changed_files"`
	Buckets          []string       `json:"buckets"`
	FrontLoaded      []RankedRule   `json:"front_loaded"`
	AlsoApplies      []string       `json:"also_applies"`
	NeverFlaggedHere []Untriggered  `json:"never_flagged_here"`
	Advisory         []string       `json:"advisory"`
}

const (
	FrontloadMarker    = "<!-- review-rule-frontload:v1 -->"
	FrontloadSchema    = "review-rule-frontload/1"
	BurstDaysThreshold = 2
)

// RawFindingRow represents a single review.finding event payload from Dolt.
type RawFindingRow struct {
	RuleID         string `json:"rule_id"`
	File           string `json:"file"`
	PR             any    `json:"pr"`
	Day            string `json:"day"`
	OccurredAt     string `json:"occurred_at"`
	VisualCategory string `json:"visual_category,omitempty"`
	Surface        string `json:"surface,omitempty"`
	Viewport       string `json:"viewport,omitempty"`
}

func (r RawFindingRow) PRString() string {
	switch v := r.PR.(type) {
	case string:
		return v
	case float64:
		return strconv.Itoa(int(v))
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case json.Number:
		return v.String()
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

type FindingRowWithTime struct {
	RuleID     string
	File       string
	PR         string
	Day        string
	OccurredAt time.Time
}
