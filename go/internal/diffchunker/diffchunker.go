package diffchunker

import (
	"strings"

	"github.com/wbern/adrian/go/internal/diffstats"
)

const criticalReminder = "\n⚠️ REMINDER: Only check ADDED lines for violations ⚠️\n" +
	"- Lines prefixed with `+` (e.g., `+ const x = 1`) → ADDED code — check these\n" +
	"- Lines prefixed with `+++` or `---` → file headers — IGNORE\n" +
	"- Lines with no prefix → existing context — IGNORE\n" +
	"- Each file must independently comply. If ANY single file violates the requirement, the overall result is FAIL.\n"

func ChunkDiffByFile(diff string, maxTokensPerChunk int, insertReminders bool) []string {
	fileDiffs := splitByFile(diff)
	if insertReminders {
		for i, fd := range fileDiffs {
			fileDiffs[i] = criticalReminder + fd
		}
	}
	if maxTokensPerChunk <= 0 {
		return fileDiffs
	}
	return chunkByTokenLimit(fileDiffs, maxTokensPerChunk)
}

func estimateTokenCount(text string) int {
	n := len(text) / 4
	if len(text)%4 != 0 {
		n++
	}
	return n
}

func chunkByTokenLimit(fileDiffs []string, maxTokensPerChunk int) []string {
	var chunks []string
	var current []string
	currentTokens := 0
	for _, fd := range fileDiffs {
		ft := estimateTokenCount(fd)
		if currentTokens+ft > maxTokensPerChunk && len(current) > 0 {
			chunks = append(chunks, strings.Join(current, "\n"))
			current = nil
			currentTokens = 0
		}
		current = append(current, fd)
		currentTokens += ft
	}
	if len(current) > 0 {
		chunks = append(chunks, strings.Join(current, "\n"))
	}
	return chunks
}

// splitByFile delegates to diffstats.Split so there is ONE unified-diff parser
// in this codebase. The local line-rejoining version it replaces could not tell
// a missing trailing newline from a present one, which is tolerable when the
// output only ever becomes prompt text but not when the same cut is used to
// slice a supplied diff per ADR.
func splitByFile(diff string) []string {
	files := diffstats.Split(diff)
	if len(files) == 0 {
		return nil
	}
	chunks := make([]string, 0, len(files))
	for _, f := range files {
		chunks = append(chunks, f.Text)
	}
	return chunks
}
