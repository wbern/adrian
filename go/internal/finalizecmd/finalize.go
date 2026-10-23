// Package finalizecmd provides the audit and finalization command for review plans.
package finalizecmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	SummarySchemaVersion = "1"
)

var (
	verdictApproveRegex = regexp.MustCompile(`(?i)^[[:space:]]*verdict:[[:space:]]*approve[[:space:]]*$`)
)

type Plan struct {
	Head             string           `json:"head"`
	DiffID           string           `json:"diff_id"`
	Binding          []string         `json:"binding"`
	PlanningError    string           `json:"planning_error,omitempty"`
	ReviewScope      string           `json:"review_scope,omitempty"`
	CoverageBaseline CoverageBaseline `json:"coverage_baseline"`
	Segments         []Segment        `json:"segments"`
	Corpus           *CorpusInfo      `json:"corpus,omitempty"`
	EvidencePacket   *EvidencePacket  `json:"evidence_packet,omitempty"`
}

type CoverageBaseline struct {
	Segments int   `json:"segments"`
	Bytes    int64 `json:"bytes"`
}

type Segment struct {
	Segment int      `json:"segment"`
	Bytes   int64    `json:"bytes"`
	Paths   []string `json:"paths"`
}

type CorpusInfo struct {
	State     string `json:"state"`
	Behind    int    `json:"behind"`
	Ahead     int    `json:"ahead"`
	DirtyADRs int    `json:"dirty_adrs"`
}

type EvidencePacket struct {
	Complete         *bool    `json:"complete"`
	MissingFragments []string `json:"missing_fragments,omitempty"`
}

// Run executes the finalize audit.
func Run(args []string, out, errOut io.Writer) error {
	var (
		planFile    string
		bodyFile    string
		coveredFile string
		receiptFile string
		outBodyFile string
	)

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--plan":
			if i+1 < len(args) {
				i++
				planFile = args[i]
			}
		case "--body-file":
			if i+1 < len(args) {
				i++
				bodyFile = args[i]
			}
		case "--segments-covered":
			if i+1 < len(args) {
				i++
				coveredFile = args[i]
			}
		case "--receipts":
			if i+1 < len(args) {
				i++
				receiptFile = args[i]
			}
		case "--out-body":
			if i+1 < len(args) {
				i++
				outBodyFile = args[i]
			}
		}
	}

	if planFile == "" {
		return errors.New("finalize: plan file is missing or empty")
	}
	if bodyFile == "" {
		return errors.New("finalize: --body-file is required and must exist")
	}
	if coveredFile == "" && receiptFile == "" {
		return errors.New("finalize: --segments-covered is required (the session must state what it judged)")
	}

	// Read and parse plan file
	planBytes, err := os.ReadFile(planFile)
	if err != nil {
		return fmt.Errorf("finalize: cannot read plan file: %w", err)
	}

	var plan Plan
	if err := json.Unmarshal(planBytes, &plan); err != nil {
		return fmt.Errorf("finalize: plan file is not valid JSON: %s", planFile)
	}

	// 1. Plan Self-Consistency Guard:
	// coverage_baseline.segments == len(segments) and coverage_baseline.bytes == sum(segments[].bytes)
	var segmentBytesSum int64
	for _, seg := range plan.Segments {
		segmentBytesSum += seg.Bytes
	}
	if plan.CoverageBaseline.Segments != len(plan.Segments) || plan.CoverageBaseline.Bytes != segmentBytesSum {
		return errors.New("finalize: plan is internally inconsistent — coverage_baseline does not match its own segments; refusing to audit against it")
	}

	// 2. Read covered segments
	var claimedSegments []int
	if coveredFile != "" {
		covBytes, err := os.ReadFile(coveredFile)
		if err != nil {
			return fmt.Errorf("finalize: cannot read covered file: %w", err)
		}

		var rawElements []json.RawMessage
		if err := json.Unmarshal(covBytes, &rawElements); err != nil {
			return errors.New("finalize: --segments-covered must be a JSON array of segment NUMBERS (e.g. [1,2,3]); string ids match the ledger but no segment")
		}

		for _, raw := range rawElements {
			var num int
			if err := json.Unmarshal(raw, &num); err != nil {
				return errors.New("finalize: --segments-covered must be a JSON array of segment NUMBERS (e.g. [1,2,3]); string ids match the ledger but no segment")
			}
			claimedSegments = append(claimedSegments, num)
		}
	} else if receiptFile != "" {
		recBytes, err := os.ReadFile(receiptFile)
		if err != nil {
			return fmt.Errorf("finalize: cannot read receipts file: %w", err)
		}
		var rawList []map[string]any
		if err := json.Unmarshal(recBytes, &rawList); err == nil {
			for _, rec := range rawList {
				if seg, ok := rec["segment"].(float64); ok {
					claimedSegments = append(claimedSegments, int(seg))
				} else if pIdx, ok := rec["packet_index"].(float64); ok {
					claimedSegments = append(claimedSegments, int(pIdx))
				}
			}
		}
	}

	// Unique & sort planned segments
	plannedMap := make(map[int]Segment)
	var plannedNums []int
	for _, s := range plan.Segments {
		plannedMap[s.Segment] = s
		plannedNums = append(plannedNums, s.Segment)
	}
	sort.Ints(plannedNums)

	// Unique & sort claimed segments
	claimedSet := make(map[int]bool)
	var claimedNums []int
	for _, num := range claimedSegments {
		if !claimedSet[num] {
			claimedSet[num] = true
			claimedNums = append(claimedNums, num)
		}
	}
	sort.Ints(claimedNums)

	var plannedStrList []string
	for _, n := range plannedNums {
		plannedStrList = append(plannedStrList, strconv.Itoa(n))
	}
	finPlanned := strings.Join(plannedStrList, ",")

	var claimedStrList []string
	for _, n := range claimedNums {
		claimedStrList = append(claimedStrList, strconv.Itoa(n))
	}
	finClaimed := strings.Join(claimedStrList, ",")

	finSegTotal := plan.CoverageBaseline.Segments
	finSegDone := len(claimedNums)
	finBytesTotal := plan.CoverageBaseline.Bytes

	var finBytesDone int64
	for _, num := range claimedNums {
		if s, ok := plannedMap[num]; ok {
			finBytesDone += s.Bytes
		}
	}

	var missingPaths []string
	for _, s := range plan.Segments {
		if !claimedSet[s.Segment] {
			missingPaths = append(missingPaths, s.Paths...)
		}
	}
	finMissingPaths := strings.Join(missingPaths, ", ")

	finPlanError := plan.PlanningError
	finPacketComplete := true
	var finPacketMissing string
	if plan.EvidencePacket != nil {
		if plan.EvidencePacket.Complete != nil {
			finPacketComplete = *plan.EvidencePacket.Complete
		}
		finPacketMissing = strings.Join(plan.EvidencePacket.MissingFragments, ", ")
	}

	// Read body file
	bodyContent, err := os.ReadFile(bodyFile)
	if err != nil {
		return fmt.Errorf("finalize: cannot read body file: %w", err)
	}
	bodyStr := string(bodyContent)

	// Extract verdict: line-anchored regex
	finVerdict := "request_changes"
	for _, line := range strings.Split(bodyStr, "\n") {
		if verdictApproveRegex.MatchString(line) {
			finVerdict = "approve"
			break
		}
	}

	// Cost marker
	finAdrScope := strings.Join(plan.Binding, ",")
	if finAdrScope == "" {
		finAdrScope = "none"
	}

	var sb strings.Builder
	sb.WriteString(bodyStr)
	if !strings.HasSuffix(bodyStr, "\n") {
		sb.WriteString("\n")
	}

	costMarker := fmt.Sprintf(
		"<!-- pr-review-cost:v%s head=%s diff=%s adr_scope=%s adr_bytes=na tokens_total=na tokens_in=na tokens_out=na payload_bytes=na reviewed_bytes=%d diff_bytes=%d passes=%d schema=%s -->\n",
		SummarySchemaVersion, plan.Head, plan.DiffID, finAdrScope, finBytesDone, finBytesTotal, finSegDone, SummarySchemaVersion,
	)
	sb.WriteString(costMarker)

	// Coverage ledger
	sb.WriteString(fmt.Sprintf(
		"\n<details>\n<summary>Coverage ledger</summary>\n\ncoverage ledger: %d/%d segments; %d/%d bytes.\n\n</details>\n",
		finSegDone, finSegTotal, finBytesDone, finBytesTotal,
	))

	// Review scope (if present)
	if plan.ReviewScope != "" {
		sb.WriteString(fmt.Sprintf("\n> **Review scope:** %s.\n", plan.ReviewScope))
	}

	// Coverage Identity Check
	if finPlanned != finClaimed || finPlanError != "" || !finPacketComplete {
		finVerdict = "request_changes"
		sb.WriteString("\nverdict: request_changes (ESCALATED)\n")
		sb.WriteString(fmt.Sprintf("blocker | Test Evidence Quality | (whole diff) | source: ci-gate | evidence: review coverage identity failed. Planned segments [%s]; the reviewer accounted for [%s]; %d of %d bytes were judged.", finPlanned, finClaimed, finBytesDone, finBytesTotal))
		if finMissingPaths != "" {
			sb.WriteString(fmt.Sprintf(" Never judged: %s.", finMissingPaths))
		}
		if finPlanError != "" {
			sb.WriteString(fmt.Sprintf(" Planner refusal: %s.", finPlanError))
		}
		if !finPacketComplete {
			missingFrag := finPacketMissing
			if missingFrag == "" {
				missingFrag = "unknown"
			}
			sb.WriteString(fmt.Sprintf(" The evidence packet is incomplete; required fragments missing or truncated: %s.", missingFrag))
		}
		sb.WriteString(" | expected fix: re-review every unaccounted segment, repair the plan, or obtain complete required evidence before an approval can mean anything.\n")
		sb.WriteString(fmt.Sprintf("\n> ⚠️ **Approve refused on incomplete coverage (gci-u0huj).** A verdict is a statement about the code that was READ. This one was formed over %d of %d bytes, and the reviewer does not get to certify its own coverage — that is why this check compares its claim against the plan rather than trusting either alone.\n", finBytesDone, finBytesTotal))
	}

	// Corpus provenance
	if plan.Corpus != nil && plan.Corpus.State != "" {
		corpusMarker := fmt.Sprintf(
			"<!-- pr-review-corpus:v%s head=%s state=%s behind=%d ahead=%d dirty_adrs=%d schema=%s -->\n",
			SummarySchemaVersion, plan.Head, plan.Corpus.State, plan.Corpus.Behind, plan.Corpus.Ahead, plan.Corpus.DirtyADRs, SummarySchemaVersion,
		)
		sb.WriteString(corpusMarker)

		switch plan.Corpus.State {
		case "behind", "ahead", "diverged", "dirty", "unknown":
			if finVerdict == "approve" {
				finVerdict = "request_changes"
				sb.WriteString("\nverdict: request_changes (ESCALATED)\n")
				sb.WriteString(fmt.Sprintf("blocker | Architectural Consistency | (whole diff) | source: ADR-0004 (crm: review gates are pipeline steps, not prose) §Decision | evidence: the ADR corpus this review enforced was read from %s, which is %s", "corpus", plan.Corpus.State))
				if plan.Corpus.Behind > 0 {
					sb.WriteString(fmt.Sprintf(" by %d commit(s)", plan.Corpus.Behind))
				}
				if plan.Corpus.Ahead > 0 {
					sb.WriteString(fmt.Sprintf(" with %d local commit(s) the target does not carry", plan.Corpus.Ahead))
				}
				if plan.Corpus.DirtyADRs > 0 {
					sb.WriteString(fmt.Sprintf(" with %d locally-modified ADR file(s)", plan.Corpus.DirtyADRs))
				}
				sb.WriteString(". An approve asserts that no binding rule was violated; it cannot assert that about rules that exist on no branch. | expected fix: bring the canonical checkout to merged source (and resolve local ADR modifications), then re-review.\n")
				sb.WriteString(fmt.Sprintf("\n> ⚠️ **Approve refused: the ADR corpus does not match merged source (gci-6nst0).** State: `%s`", plan.Corpus.State))
				if plan.Corpus.Behind > 0 {
					sb.WriteString(fmt.Sprintf(", behind by %d commit(s)", plan.Corpus.Behind))
				}
				if plan.Corpus.Ahead > 0 {
					sb.WriteString(fmt.Sprintf(", ahead by %d commit(s)", plan.Corpus.Ahead))
				}
				if plan.Corpus.DirtyADRs > 0 {
					sb.WriteString(fmt.Sprintf(", %d ADR file(s) locally modified", plan.Corpus.DirtyADRs))
				}
				sb.WriteString(".\n> A blocking verdict is left to stand — a wrong block is visible and arguable. A wrong approve is silent, so this gate will not carry one.\n")
				sb.WriteString("> Original model verdict was `approve`; its rationale is retained above.\n")
			}
		}
	}

	targetOutFile := bodyFile
	if outBodyFile != "" {
		targetOutFile = outBodyFile
	}
	if err := os.WriteFile(targetOutFile, []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("finalize: failed to write updated body: %w", err)
	}

	fmt.Fprintln(out, finVerdict)
	return nil
}
