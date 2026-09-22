package responseparser

import (
	"strings"
	"testing"

	"github.com/wbern/adrian/go/internal/adr"
)

func sampleADR() adr.ADR {
	return adr.ADR{
		ID:          "0002",
		Title:       "Use Testify",
		Status:      adr.StatusAccepted,
		AppliesTo:   []string{"**/*_test.go"},
		Complexity:  adr.ComplexityUltralite,
		Decision:    "Check for gomock usage",
		FilePath:    "/test/adr.md",
		Content:     "Test content",
		DiffContext: true,
	}
}

func TestParseResponse_ValidJSON_PASS(t *testing.T) {
	a := sampleADR()
	response := `{"status":"PASS","confidence":"high","explanation":"No violations found"}`

	r := ParseResponse(a, response, nil)

	if r.Status != "PASS" {
		t.Errorf("status = %q, want PASS", r.Status)
	}
	if r.Confidence == nil || *r.Confidence != "high" {
		t.Errorf("confidence = %v, want *\"high\"", r.Confidence)
	}
	if r.Explanation != "No violations found" {
		t.Errorf("explanation = %q", r.Explanation)
	}
}

func TestParseResponse_ValidJSON_FAIL(t *testing.T) {
	a := sampleADR()
	response := `{"status":"FAIL","confidence":"high","explanation":"Found gomock usage","violation":"gomock.NewController(t)","suggestion":"Use testify mocks instead"}`

	r := ParseResponse(a, response, nil)

	if r.Status != "FAIL" {
		t.Errorf("status = %q, want FAIL", r.Status)
	}
	if r.Explanation != "Found gomock usage" {
		t.Errorf("explanation = %q", r.Explanation)
	}
	if r.Suggestion == nil || *r.Suggestion != "Use testify mocks instead" {
		t.Errorf("suggestion = %v", r.Suggestion)
	}
}

func TestParseResponse_TruncatedJSON_FAIL_DowngradesToWARN(t *testing.T) {
	a := sampleADR()
	truncated := `{
  "status": "FAIL",
  "confidence": "high",
  "explanation": "The sort() call mutates an array that is part of the cached traceData object`

	r := ParseResponse(a, truncated, nil)

	if r.Status != "WARN" {
		t.Errorf("status = %q, want WARN", r.Status)
	}
	if !strings.Contains(r.Explanation, "downgraded") {
		t.Errorf("explanation should contain 'downgraded': %q", r.Explanation)
	}
	if !strings.Contains(r.Explanation, "malformed LLM response") {
		t.Errorf("explanation should contain 'malformed LLM response': %q", r.Explanation)
	}
}

func TestParseResponse_TruncatedJSON_PASS_StaysAsPASS(t *testing.T) {
	a := sampleADR()
	truncated := `{
  "status": "PASS",
  "explanation": "All sort calls use spread copies`

	r := ParseResponse(a, truncated, nil)

	if r.Status != "PASS" {
		t.Errorf("status = %q, want PASS", r.Status)
	}
}
