// Package reviewpacket builds model-neutral, bounded review input from
// applicable ADRs and a unified diff. It never calls a model.
package reviewpacket

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wbern/adr-lint/go/internal/adr"
)

const SchemaVersion = "review-packet-v1"

// Options controls mechanical packet construction.
type Options struct {
	MaxTokensPerChunk int
	RequirePreFilter  bool
	MaxPackets        int
}

// Input pairs one applicable ADR with the diff for the files it governs.
type Input struct {
	ADR   adr.ADR
	Files []string
	Diff  string
}

// Plan records both reviewable packets and deterministic exclusions.
type Plan struct {
	SchemaVersion string    `json:"schemaVersion"`
	PlanSHA256    string    `json:"planSha256"`
	Packets       []Packet  `json:"packets"`
	Skipped       []Skipped `json:"skipped"`
	Limits        Limits    `json:"limits"`
}

// Limits records the caller's declared capacity boundary. The planner never
// drops packets to satisfy it; a consumer must decline an over-limit plan.
type Limits struct {
	MaxTokensPerChunk   int  `json:"maxTokensPerChunk"`
	MaxPackets          int  `json:"maxPackets,omitempty"`
	PacketLimitExceeded bool `json:"packetLimitExceeded"`
}

// Packet is the complete bounded input for one ADR review unit.
type Packet struct {
	ADR             Policy   `json:"adr"`
	Files           []string `json:"files"`
	Diff            string   `json:"diff"`
	PreFilterHits   []string `json:"preFilterHits,omitempty"`
	ChunkIndex      int      `json:"chunkIndex"`
	ChunkCount      int      `json:"chunkCount"`
	EstimatedTokens int      `json:"estimatedTokens"`
}

// Policy contains only the policy material a reviewer needs.
type Policy struct {
	ID         string         `json:"id"`
	Title      string         `json:"title"`
	Complexity adr.Complexity `json:"complexity"`
	Decision   string         `json:"decision"`
}

// Skipped records why an otherwise applicable ADR was not sent for review.
type Skipped struct {
	ADR    Policy   `json:"adr"`
	Files  []string `json:"files"`
	Reason string   `json:"reason"`
}

// Build deterministically narrows pre-filtered ADRs to matching hunks. A
// pre-filter miss is recorded, not silently discarded.
func Build(inputs []Input, options Options) Plan {
	plan := Plan{
		SchemaVersion: SchemaVersion,
		Packets:       []Packet{},
		Skipped:       []Skipped{},
		Limits:        Limits{MaxTokensPerChunk: options.MaxTokensPerChunk, MaxPackets: options.MaxPackets},
	}
	for _, input := range inputs {
		policy := policyFor(input.ADR)
		policyTokens := estimatePolicyTokens(policy)
		if options.RequirePreFilter && len(input.ADR.PreFilter) == 0 {
			plan.Skipped = append(plan.Skipped, Skipped{
				ADR: policy, Files: input.Files, Reason: "pre_filter_required",
			})
			continue
		}
		if options.MaxTokensPerChunk > 0 && policyTokens >= options.MaxTokensPerChunk {
			plan.Skipped = append(plan.Skipped, Skipped{
				ADR: policy, Files: input.Files, Reason: "policy_exceeds_token_budget",
			})
			continue
		}
		diff, hits := selectedDiff(input.Diff, input.ADR.PreFilter)
		if strings.TrimSpace(diff) == "" {
			plan.Skipped = append(plan.Skipped, Skipped{
				ADR: policy, Files: input.Files, Reason: "pre_filter_not_matched",
			})
			continue
		}
		diffBudget := options.MaxTokensPerChunk
		if diffBudget > 0 {
			diffBudget -= policyTokens
		}
		units := packetUnits(diff, input.ADR.DiffContext)
		chunks := chunkFiles(units, diffBudget)
		if !input.ADR.DiffContext {
			chunks = nil
			for _, unit := range units {
				chunks = append(chunks, splitOversizedFile(unit, diffBudget)...)
			}
		}
		if options.MaxTokensPerChunk > 0 && exceedsBudget(chunks, policyTokens, options.MaxTokensPerChunk) {
			plan.Skipped = append(plan.Skipped, Skipped{
				ADR: policy, Files: input.Files, Reason: "diff_exceeds_token_budget",
			})
			continue
		}
		for i, chunk := range chunks {
			plan.Packets = append(plan.Packets, Packet{
				ADR:             policy,
				Files:           filesForChunk(chunk, input.Files),
				Diff:            chunk,
				PreFilterHits:   hits,
				ChunkIndex:      i + 1,
				ChunkCount:      len(chunks),
				EstimatedTokens: policyTokens + estimateTokens(chunk),
			})
		}
	}
	plan.Limits.PacketLimitExceeded = options.MaxPackets > 0 && len(plan.Packets) > options.MaxPackets
	plan.PlanSHA256 = hashPlan(plan)
	return plan
}

func hashPlan(plan Plan) string {
	plan.PlanSHA256 = ""
	payload, err := json.Marshal(plan)
	if err != nil {
		panic("reviewpacket: marshal plan hash: " + err.Error())
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// ValidatePlan rejects a plan whose serialized packet inventory no longer
// matches the hash produced by Build.
func ValidatePlan(plan Plan) error {
	if plan.PlanSHA256 == "" {
		return fmt.Errorf("planSha256 is required")
	}
	if got := hashPlan(plan); got != plan.PlanSHA256 {
		return fmt.Errorf("planSha256 mismatch")
	}
	return nil
}

func filesForChunk(diff string, fallback []string) []string {
	var paths []string
	seen := make(map[string]struct{})
	for _, line := range strings.Split(diff, "\n") {
		if !strings.HasPrefix(line, "diff --git ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		for _, path := range fields[2:4] {
			path = strings.TrimPrefix(path, "a/")
			path = strings.TrimPrefix(path, "b/")
			if path == "/dev/null" {
				continue
			}
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return fallback
	}
	return paths
}

func exceedsBudget(chunks []string, policyTokens, maxTokens int) bool {
	for _, chunk := range chunks {
		if policyTokens+estimateTokens(chunk) > maxTokens {
			return true
		}
	}
	return false
}

func packetUnits(diff string, includeContext bool) []string {
	files := splitFiles(diff)
	if includeContext {
		return files
	}
	var units []string
	for _, file := range files {
		preamble, hunks := splitHunks(file)
		for _, hunk := range hunks {
			units = append(units, preamble+hunk)
		}
	}
	return units
}

func chunkFiles(fileDiffs []string, maxTokens int) []string {
	if len(fileDiffs) == 0 {
		return nil
	}
	if maxTokens <= 0 {
		return []string{strings.Join(fileDiffs, "")}
	}

	var units []string
	for _, fileDiff := range fileDiffs {
		units = append(units, splitOversizedFile(fileDiff, maxTokens)...)
	}

	var chunks []string
	var current strings.Builder
	currentTokens := 0
	for _, unit := range units {
		unitTokens := estimateTokens(unit)
		if current.Len() > 0 && currentTokens+unitTokens > maxTokens {
			chunks = append(chunks, current.String())
			current.Reset()
			currentTokens = 0
		}
		current.WriteString(unit)
		currentTokens += unitTokens
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	return chunks
}

func splitOversizedFile(fileDiff string, maxTokens int) []string {
	if estimateTokens(fileDiff) <= maxTokens {
		return []string{fileDiff}
	}
	preamble, hunks := splitHunks(fileDiff)
	if len(hunks) == 0 {
		return []string{fileDiff}
	}

	var units []string
	for _, hunk := range hunks {
		lines := strings.SplitAfter(hunk, "\n")
		if len(lines) == 0 {
			continue
		}
		current := preamble + lines[0]
		for _, line := range lines[1:] {
			if estimateTokens(current+line) > maxTokens && current != preamble+lines[0] {
				units = append(units, current)
				current = preamble + lines[0]
			}
			current += line
		}
		if current != preamble+lines[0] {
			units = append(units, current)
		}
	}
	if len(units) == 0 {
		return []string{fileDiff}
	}
	return units
}

func estimateTokens(text string) int {
	if text == "" {
		return 0
	}
	return (len(text) + 3) / 4
}

func policyFor(a adr.ADR) Policy {
	return Policy{ID: a.ID, Title: a.Title, Complexity: a.Complexity, Decision: a.Decision}
}

func estimatePolicyTokens(policy Policy) int {
	return estimateTokens(policy.ID + policy.Title + string(policy.Complexity) + policy.Decision)
}

func selectedDiff(diff string, preFilter []string) (string, []string) {
	if len(preFilter) == 0 {
		return diff, nil
	}

	var selected []string
	hitSet := make(map[string]struct{})
	for _, file := range splitFiles(diff) {
		preamble, hunks := splitHunks(file)
		var matched []string
		for _, hunk := range hunks {
			hits := matchingPatterns(hunk, preFilter)
			if len(hits) == 0 {
				continue
			}
			for _, hit := range hits {
				hitSet[hit] = struct{}{}
			}
			matched = append(matched, hunk)
		}
		if len(matched) > 0 {
			selected = append(selected, preamble+strings.Join(matched, ""))
		}
	}

	if len(selected) == 0 {
		return "", nil
	}
	hits := make([]string, 0, len(hitSet))
	for _, pattern := range preFilter {
		if _, ok := hitSet[pattern]; ok {
			hits = append(hits, pattern)
		}
	}
	return strings.Join(selected, ""), hits
}

func splitFiles(diff string) []string {
	if strings.TrimSpace(diff) == "" {
		return nil
	}
	var files []string
	var current strings.Builder
	for _, line := range strings.SplitAfter(diff, "\n") {
		if strings.HasPrefix(line, "diff --git ") && current.Len() > 0 {
			files = append(files, current.String())
			current.Reset()
		}
		current.WriteString(line)
	}
	if current.Len() > 0 {
		files = append(files, current.String())
	}
	return files
}

func splitHunks(fileDiff string) (string, []string) {
	var preamble strings.Builder
	var hunks []string
	var current strings.Builder
	inHunk := false
	for _, line := range strings.SplitAfter(fileDiff, "\n") {
		if strings.HasPrefix(line, "@@ ") {
			if inHunk {
				hunks = append(hunks, current.String())
				current.Reset()
			}
			inHunk = true
		}
		if inHunk {
			current.WriteString(line)
		} else {
			preamble.WriteString(line)
		}
	}
	if inHunk {
		hunks = append(hunks, current.String())
	}
	return preamble.String(), hunks
}

func matchingPatterns(text string, patterns []string) []string {
	var out []string
	for _, pattern := range patterns {
		if strings.Contains(text, pattern) {
			out = append(out, pattern)
		}
	}
	return out
}
