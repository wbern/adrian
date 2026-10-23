package resolvecmd

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Plural formats a count with a singular or plural word (e.g. "1 PR" or "3 PRs").
func Plural(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}

// Citation formats a markdown link for an ADR or lens ID if a reference URL exists.
func Citation(id string, refs map[string]string) string {
	if refs == nil {
		return id
	}
	target, ok := refs[id]
	if !ok || target == "" {
		return id
	}
	return fmt.Sprintf("[`%s`](%s)", id, target)
}

// RenderFrontloadMarkdown formats the GitHub-ready collapsible front-load PR block.
func RenderFrontloadMarkdown(record FrontloadRecord, refs map[string]string) string {
	var rows []string
	for _, r := range record.FrontLoaded {
		row := fmt.Sprintf("| %d | %s | %s | %s / %s on `%s` |",
			r.Rank,
			Citation(r.ID, refs),
			r.Title,
			Plural(r.PRs, "PR", "PRs"),
			Plural(r.Findings, "finding", "findings"),
			r.Bucket,
		)
		rows = append(rows, row)
	}

	unreachable := record.WeightsWindow == nil
	var corpus int
	if record.WeightsWindow != nil {
		corpus = record.WeightsWindow.CorpusFindings
	}

	var table string
	if len(rows) > 0 {
		table = "| # | Rule | What it is | Why it was surfaced |\n| --- | --- | --- | --- |\n" + strings.Join(rows, "\n")
	} else if unreachable {
		table = "> **RANKING UNAVAILABLE HERE** — this ran somewhere without the review-finding\n" +
			"> store (a CI runner has none), so the rules below are unranked. This says\n" +
			"> nothing about the diff, and nothing about what the builder was shown."
	} else if corpus == 0 {
		table = "> **NOT SEEDED** — this city holds no review findings at all, so nothing could be\n" +
			"> ranked. Run `pnpm sync:review-findings` there. This is not a statement about\n" +
			"> the diff."
	} else {
		table = "_No rule has yet been flagged on paths like these._"
	}

	windowStr := "unavailable"
	if record.WeightsWindow != nil {
		windowStr = fmt.Sprintf("%s..%s", record.WeightsWindow.From, record.WeightsWindow.To)
	}

	summary := fmt.Sprintf("<summary>Rules front-loaded to the builder before writing this change (%d)</summary>", len(record.FrontLoaded))
	if unreachable {
		summary = "<summary>Applicable rules for this change (unranked — no store here)</summary>"
	}

	var lines []string
	lines = append(lines, FrontloadMarker, "<details>", summary, "", table, "")
	lines = append(lines, fmt.Sprintf("Ranked by what has actually blocked PRs on these paths (evidence window %s).", windowStr))
	lines = append(lines,
		"Every rule that applies to this diff is listed below, ranked or not — this is a",
		"reading order, not a filter, and it does not narrow what review covers.",
	)

	if corpus > 0 && corpus < ThinCorpusThreshold {
		lines = append(lines,
			fmt.Sprintf("> **THIN DATA** — ranked from only %d findings, so this order is closer to", corpus),
			"> noise than signal. Run `pnpm sync:review-findings`.",
		)
	}
	if record.CorpusPartial {
		lines = append(lines,
			"> **PARTIAL CORPUS** — this window predates a fix to the ingest: findings",
			"> sourced from a named check or a file:line precedent were deleted as they",
			"> were written and can never be recovered. ADR- and lens-sourced findings",
			"> are unaffected.",
		)
	}
	lines = append(lines, "</details>", "")

	jsonBytes, err := json.MarshalIndent(record, "", "  ")
	if err == nil {
		lines = append(lines, "```json", string(jsonBytes), "```")
	}

	return strings.Join(lines, "\n")
}
