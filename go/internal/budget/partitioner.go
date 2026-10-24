package budget

import (
	"fmt"
	"sort"
	"strings"
)

// EvaluateExecutionMode decides whether a diff executes monolithically or partitioned into fanout batches.
func EvaluateExecutionMode(estimatedTokens int, diffBytes int64, segCount int, profile Profile, cfg BudgetConfig) (string, string) {
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	switch mode {
	case ModeMonolithic:
		return ExecutionModeMonolithic, "explicit configuration mode: monolithic"
	case ModePartitioned:
		return ExecutionModePartitioned, "explicit configuration mode: partitioned"
	}

	// Auto mode heuristics
	if cfg.FanoutThresholdBytes > 0 && diffBytes > cfg.FanoutThresholdBytes {
		return ExecutionModePartitioned, fmt.Sprintf("diff bytes (%d) exceeds fanout threshold (%d)", diffBytes, cfg.FanoutThresholdBytes)
	}

	if cfg.FanoutThresholdSegments > 0 && segCount > cfg.FanoutThresholdSegments {
		return ExecutionModePartitioned, fmt.Sprintf("segment count (%d) exceeds fanout threshold (%d)", segCount, cfg.FanoutThresholdSegments)
	}

	if profile.TargetBatchBudget > 0 && estimatedTokens > profile.TargetBatchBudget {
		return ExecutionModePartitioned, fmt.Sprintf("estimated tokens (%d) exceeds %s target batch budget (%d)", estimatedTokens, profile.Name, profile.TargetBatchBudget)
	}

	return ExecutionModeMonolithic, "envelope fits within target batch budget and fanout thresholds"
}

// PartitionSegments uses best-fit decreasing with directory affinity to pack segments into subagent batches.
func PartitionSegments(segs []Segment, profile Profile, cfg BudgetConfig) []Batch {
	if len(segs) == 0 {
		return nil
	}

	// Make a copy of segments sorted by token size descending (Best-Fit Decreasing)
	sorted := make([]Segment, len(segs))
	copy(sorted, segs)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].EstimatedTokens != sorted[j].EstimatedTokens {
			return sorted[i].EstimatedTokens > sorted[j].EstimatedTokens
		}
		return sorted[i].Bytes > sorted[j].Bytes
	})

	maxTokens := profile.TargetBatchBudget
	maxBytes := cfg.BatchMaxBytes
	maxSegs := cfg.BatchMaxSegments
	if maxSegs <= 0 {
		maxSegs = 4
	}
	if maxBytes <= 0 {
		maxBytes = 200000
	}

	var batches []Batch

	for _, seg := range sorted {
		placed := false

		// 1. First pass: look for a batch with directory affinity that has capacity
		for i := range batches {
			b := &batches[i]
			if len(b.Segments) >= maxSegs {
				continue
			}
			if b.Bytes+seg.Bytes > maxBytes {
				continue
			}
			if maxTokens > 0 && b.EstimatedTokens+seg.EstimatedTokens > maxTokens {
				continue
			}

			if hasPathAffinity(b.Paths, seg.Paths) {
				b.Segments = append(b.Segments, seg)
				b.SegmentIDs = append(b.SegmentIDs, seg.ID)
				b.Bytes += seg.Bytes
				b.Paths = mergeUniquePaths(b.Paths, seg.Paths)
				b.EstimatedTokens += seg.EstimatedTokens
				placed = true
				break
			}
		}

		// 2. Second pass: place in any existing batch that has capacity
		if !placed {
			for i := range batches {
				b := &batches[i]
				if len(b.Segments) >= maxSegs {
					continue
				}
				if b.Bytes+seg.Bytes > maxBytes {
					continue
				}
				if maxTokens > 0 && b.EstimatedTokens+seg.EstimatedTokens > maxTokens {
					continue
				}

				b.Segments = append(b.Segments, seg)
				b.SegmentIDs = append(b.SegmentIDs, seg.ID)
				b.Bytes += seg.Bytes
				b.Paths = mergeUniquePaths(b.Paths, seg.Paths)
				b.EstimatedTokens += seg.EstimatedTokens
				placed = true
				break
			}
		}

		// 3. Third pass: create a new batch
		if !placed {
			batchNum := len(batches) + 1
			batches = append(batches, Batch{
				BatchID:         fmt.Sprintf("batch-%d", batchNum),
				Segments:        []Segment{seg},
				SegmentIDs:      []int{seg.ID},
				Bytes:           seg.Bytes,
				Paths:           append([]string{}, seg.Paths...),
				EstimatedTokens: seg.EstimatedTokens,
			})
		}
	}

	// Sort segment IDs within each batch for determinism
	for i := range batches {
		sort.Ints(batches[i].SegmentIDs)
	}

	maxBatches := cfg.MaxBatches
	if maxBatches > 0 && len(batches) > maxBatches {
		batches = consolidateBatches(batches, maxBatches)
	}

	return batches
}

func consolidateBatches(batches []Batch, maxBatches int) []Batch {
	if maxBatches <= 0 || len(batches) <= maxBatches {
		return batches
	}

	var allSegs []Segment
	for _, b := range batches {
		allSegs = append(allSegs, b.Segments...)
	}

	sort.SliceStable(allSegs, func(i, j int) bool {
		if allSegs[i].EstimatedTokens != allSegs[j].EstimatedTokens {
			return allSegs[i].EstimatedTokens > allSegs[j].EstimatedTokens
		}
		return allSegs[i].Bytes > allSegs[j].Bytes
	})

	bins := make([]Batch, maxBatches)
	for i := 0; i < maxBatches; i++ {
		bins[i] = Batch{
			BatchID: fmt.Sprintf("batch-%d", i+1),
		}
	}

	for _, seg := range allSegs {
		minIdx := 0
		for i := 1; i < maxBatches; i++ {
			if bins[i].EstimatedTokens < bins[minIdx].EstimatedTokens ||
				(bins[i].EstimatedTokens == bins[minIdx].EstimatedTokens && bins[i].Bytes < bins[minIdx].Bytes) {
				minIdx = i
			}
		}

		b := &bins[minIdx]
		b.Segments = append(b.Segments, seg)
		b.SegmentIDs = append(b.SegmentIDs, seg.ID)
		b.Bytes += seg.Bytes
		b.Paths = mergeUniquePaths(b.Paths, seg.Paths)
		b.EstimatedTokens += seg.EstimatedTokens
	}

	var result []Batch
	count := 1
	for _, b := range bins {
		if len(b.Segments) > 0 {
			sort.Ints(b.SegmentIDs)
			b.BatchID = fmt.Sprintf("batch-%d", count)
			count++
			result = append(result, b)
		}
	}

	return result
}

// BuildPlanSummary computes the capacity plan, evaluates the mode, and partitions if needed.
func BuildPlanSummary(segs []Segment, diffBytes, bindingADRBytes, fullContextBytes int64, complexity, diffContext string, profile Profile, cfg BudgetConfig) PlanSummary {
	totalTokens, overhead := EstimateTokens(diffBytes, bindingADRBytes, fullContextBytes, complexity, diffContext, profile)
	mode, reason := EvaluateExecutionMode(totalTokens, diffBytes, len(segs), profile, cfg)

	var batches []Batch
	if mode == ExecutionModePartitioned && len(segs) > 0 {
		batches = PartitionSegments(segs, profile, cfg)
	}

	return PlanSummary{
		ExecutionMode:      mode,
		Profile:            profile.Name,
		EstimatedTokens:    totalTokens,
		TargetBatchBudget:  profile.TargetBatchBudget,
		DiffBytes:          diffBytes,
		BindingADRBytes:    bindingADRBytes,
		FullContextBytes:   fullContextBytes,
		ComplexityOverhead: overhead,
		Reason:             reason,
		Batches:            batches,
	}
}

func hasPathAffinity(pathsA, pathsB []string) bool {
	for _, a := range pathsA {
		dirA := pathDirectoryPrefix(a)
		for _, b := range pathsB {
			dirB := pathDirectoryPrefix(b)
			if dirA != "" && dirA == dirB {
				return true
			}
		}
	}
	return false
}

func pathDirectoryPrefix(p string) string {
	clean := strings.ReplaceAll(p, "\\", "/")
	parts := strings.Split(clean, "/")
	if len(parts) > 1 {
		// e.g. "src/routes" or "supabase/migrations"
		if len(parts) >= 3 {
			return strings.Join(parts[:2], "/")
		}
		return parts[0]
	}
	return ""
}

func mergeUniquePaths(a, b []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, p := range append(a, b...) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
