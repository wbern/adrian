// Package promptbuilder constructs the LLM prompt for a single ADR check.
//
// The prompt is built static-first / dynamic-last so the provider can
// reuse the leading slab across ADR checks via prefix caching; the
// ADR-specific section is appended at the very end.
package promptbuilder

import (
	"strings"

	"github.com/wbern/adrian/go/internal/adr"
)

const staticInstructionsHeader = "You are a code lint checker. Check if the code changes below violate the requirement that will be specified at the end.\n" +
	"\n" +
	"### Code Changes (git diff):\n" +
	"```diff\n"

const staticInstructionsFooter = "```\n" +
	"\n" +
	"### Instructions:\n" +
	"IMPORTANT: Your JSON response must be under 500 characters total. Be concise.\n" +
	"\n" +
	"### How to Read the Diff\n" +
	"\n" +
	"Each line in the diff has a prefix that determines its type:\n" +
	"```\n" +
	"--- a/file.go        ← old file path (IGNORE)\n" +
	"+++ b/file.go        ← new file path (IGNORE)\n" +
	"@@ -1,3 +1,4 @@     ← line numbers (IGNORE)\n" +
	"  existing code      ← context/unchanged (IGNORE)\n" +
	"- removed code       ← deleted line (IGNORE)\n" +
	"+ new code           ← ADDED line — CHECK THIS for violations\n" +
	"```\n" +
	"\n" +
	"**ONLY lines prefixed with `+` (not `+++`) contain new code. Report violations ONLY from these lines.**\n" +
	"- Context lines (no prefix) are pre-existing code — do NOT flag them\n" +
	"- Exception: report if added code DIRECTLY calls or instantiates problematic existing code AND creates a type safety or security risk\n" +
	"\n" +
	"### Analysis Steps:\n" +
	"\n" +
	"1. Search for violations ONLY in added lines (prefixed with `+`)\n" +
	"2. Evaluate EACH file independently — check every file in the diff against the requirement\n" +
	"3. If ANY single file violates the requirement, the overall status must be FAIL — even if other files comply\n" +
	"4. Set confidence to high, medium, or low based on certainty\n" +
	"5. For PASS: Keep response minimal — status, confidence, short explanation (under 80 chars)\n" +
	"6. For FAIL: Include violation (max 100 chars), suggestion (max 150 chars), brief reasoning (max 200 chars)\n" +
	"7. For FAIL: Extract file:line locations from diff headers and include in \"locations\" array\n"

const guardrailsUltralite = "\n### SIMPLE KEYWORD CHECK:\n" +
	"- This is a simple keyword search task\n" +
	"- Search for the EXACT strings/patterns mentioned in the requirement\n" +
	"- If you find the exact pattern in an added line (prefixed with `+`), set status to FAIL\n" +
	"- If no exact pattern is found in added lines, set status to PASS\n" +
	"- Look only for literal string matches\n" +
	"- Set confidence to \"high\"\n"

const guardrailsLite = "\n### Analysis Guidelines:\n" +
	"- Focus exclusively on added lines (prefixed with `+`)\n" +
	"- To report a FAIL, quote the specific violating code from an added line\n" +
	"- If no violation can be quoted from added lines, the result is PASS\n" +
	"- Show step-by-step reasoning in the \"reasoning\" field\n" +
	"- Set confidence to \"high\" only when you have a direct quote from an added line\n" +
	"- Set confidence to \"low\" when uncertain\n"

const guardrailsStandardComplex = "\n### Analysis Guidelines:\n" +
	"- Analyze code patterns and architectural concerns in added lines (prefixed with `+`)\n" +
	"- Consider how new code interacts with existing code (context lines without prefix)\n" +
	"- Report FAIL only if the violation is introduced or activated by the new code\n" +
	"- For architectural violations, explain how the new code violates the principle\n" +
	"- When multiple files are present, evaluate each file separately — one non-compliant file means FAIL\n" +
	"- Set confidence based on how clear the violation is\n"

const criticalReminder = "\n### ⚠️ REMINDER — Only added lines matter ⚠️\n" +
	"- Only lines prefixed with `+` (not `+++`) are new code\n" +
	"- Lines without prefix are pre-existing context — do NOT flag\n" +
	"- To report FAIL, you MUST quote code from an added line (prefixed with `+`)\n" +
	"- Each file must independently comply. If ANY single file violates the requirement, the overall result is FAIL — even if other files are compliant.\n"

// BuildPrompt returns the full prompt for one ADR / diff pair. The
// returned string is intentionally long: providers' prefix caches deduplicate
// the static portion across calls, so verbosity here is essentially free.
func BuildPrompt(a adr.ADR, diff string) string {
	var b strings.Builder
	b.WriteString(staticInstructionsHeader)
	b.WriteString(diff)
	b.WriteString("\n")
	b.WriteString(staticInstructionsFooter)
	b.WriteString(guardrailsFor(a.Complexity))
	b.WriteString(criticalReminder)
	b.WriteString("\n### ADR Requirement to Check: ")
	b.WriteString(a.Title)
	b.WriteString("\n\n")
	b.WriteString(a.Decision)
	return b.String()
}

func guardrailsFor(c adr.Complexity) string {
	switch c {
	case adr.ComplexityUltralite:
		return guardrailsUltralite
	case adr.ComplexityLite:
		return guardrailsLite
	case adr.ComplexityStandard, adr.ComplexityComplex:
		return guardrailsStandardComplex
	}
	return ""
}
