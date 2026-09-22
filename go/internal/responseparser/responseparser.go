// Package responseparser converts an LLM provider's raw response text
// into a LintResult.
package responseparser

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/wbern/adrian/go/internal/adr"
	"github.com/wbern/adrian/go/internal/types"
)

var statusRe = regexp.MustCompile(`(?i)"status"\s*:\s*"(PASS|FAIL)"`)
var simpleExplanationRe = regexp.MustCompile(`"explanation"\s*:\s*"([^"]+)"`)

// Re-exports of canonical types from internal/types so callers can
// reach them via responseparser without an extra import.
type (
	TokenUsage   = types.TokenUsage
	ResultStatus = types.ResultStatus
	Confidence   = types.Confidence
	LintResult   = types.LintResult
)

const (
	StatusPASS    = types.StatusPASS
	StatusFAIL    = types.StatusFAIL
	StatusWARN    = types.StatusWARN
	StatusERROR   = types.StatusERROR
	StatusSKIPPED = types.StatusSKIPPED
)

// structuredResponse is the shape the LLM is asked to return.
type structuredResponse struct {
	Status      string   `json:"status"`
	Reasoning   string   `json:"reasoning"`
	Confidence  string   `json:"confidence"`
	Explanation string   `json:"explanation"`
	Violation   string   `json:"violation"`
	Suggestion  string   `json:"suggestion"`
	Locations   []string `json:"locations"`
}

// ParseResponse converts responseText into a LintResult bound to adr.
// tokenUsage may be nil.
func ParseResponse(a adr.ADR, responseText string, tokenUsage *TokenUsage) LintResult {
	var parsed structuredResponse
	parseErr := json.Unmarshal([]byte(responseText), &parsed)
	if parseErr == nil && parsed.Status != "" && parsed.Explanation != "" {
		return fromStructured(a, parsed, tokenUsage)
	}

	if m := statusRe.FindStringSubmatch(responseText); m != nil {
		rawStatus := strings.ToUpper(m[1])
		explanation := extractExplanation(responseText)
		errMsg := ""
		if parseErr != nil {
			errMsg = parseErr.Error()
		}
		status := StatusERROR
		switch rawStatus {
		case "PASS":
			status = StatusPASS
		case "FAIL":
			status = StatusWARN
		}
		return LintResult{
			ADR:         a,
			Status:      status,
			Explanation: fmt.Sprintf("%s (downgraded: malformed LLM response — %s)", explanation, errMsg),
			TokenUsage:  tokenUsage,
		}
	}

	return LintResult{ADR: a, Status: StatusERROR, Explanation: "Unable to parse response", TokenUsage: tokenUsage}
}

func extractExplanation(responseText string) string {
	if m := simpleExplanationRe.FindStringSubmatch(responseText); m != nil {
		return m[1]
	}
	// Manual unescape: find "explanation": " and read until an unescaped "
	idx := strings.Index(responseText, `"explanation"`)
	if idx == -1 {
		return "Unable to parse full response"
	}
	colon := strings.Index(responseText[idx:], ":")
	if colon == -1 {
		return "Unable to parse full response"
	}
	qstart := strings.Index(responseText[idx+colon+1:], `"`)
	if qstart == -1 {
		return "Unable to parse full response"
	}
	start := idx + colon + 1 + qstart + 1
	var b strings.Builder
	for i := start; i < len(responseText); i++ {
		c := responseText[i]
		if c == '"' && responseText[i-1] != '\\' {
			break
		}
		b.WriteByte(c)
	}
	content := b.String()
	if content == "" {
		return "Unable to parse full response"
	}
	content = strings.ReplaceAll(content, `\"`, `"`)
	content = strings.ReplaceAll(content, `\n`, " ")
	return content
}

func fromStructured(a adr.ADR, p structuredResponse, tu *TokenUsage) LintResult {
	status := StatusERROR
	switch p.Status {
	case "PASS":
		status = StatusPASS
	case "FAIL":
		if p.Confidence == "low" {
			status = StatusWARN
		} else {
			status = StatusFAIL
		}
	}

	r := LintResult{
		ADR:         a,
		Status:      status,
		Explanation: p.Explanation,
		TokenUsage:  tu,
	}
	if p.Suggestion != "" {
		s := p.Suggestion
		r.Suggestion = &s
	}
	if p.Confidence != "" {
		c := Confidence(p.Confidence)
		r.Confidence = &c
	}
	if len(p.Locations) > 0 {
		r.Locations = p.Locations
	}
	return r
}
