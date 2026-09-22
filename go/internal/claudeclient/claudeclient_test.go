package claudeclient

import (
	"strings"
	"testing"

	"github.com/wbern/adr-lint/go/internal/adr"
	"github.com/wbern/adr-lint/go/internal/types"
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

func wrapCLIResponse(structured string) string {
	return `{"type":"result","result":"","structured_output":` + structured + `}`
}

// TestLintWithClaude_DoesNotPopulateTokenUsage was here. It pinned the old
// behaviour of discarding the CLI's `usage` block, which made every check
// report no cost at all. That is now populated; the replacement cases live in
// cost_test.go (TestLint_PopulatesTokenUsageFromCLIUsage and friends), which
// keep the half of the contract still worth pinning: absent usage must stay
// absent rather than becoming a zero.

func TestLintWithClaude_InvokesClaudeWithExpectedArgs(t *testing.T) {
	var captured []string
	var sent string
	c := NewClient(func(args []string, in string) (string, error) {
		captured = append([]string(nil), args...)
		sent = in
		return wrapCLIResponse(`{"status":"PASS","confidence":"high","explanation":"OK"}`), nil
	})

	_, err := c.Lint(sampleADR(), "+ vi.fn()")
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}

	requiredPairs := [][2]string{
		{"--output-format", "json"},
		{"--tools", ""},
	}
	for _, p := range requiredPairs {
		if !hasAdjacent(captured, p[0], p[1]) {
			t.Errorf("missing adjacent args %q %q in %v", p[0], p[1], captured)
		}
	}
	// The prompt must carry the ADR title. It now travels on STDIN rather than
	// as the operand of -p (MAX_ARG_STRLEN; see
	// TestLint_PromptGoesToStdinNotArgv), so this reads the same fact from
	// where the prompt actually goes.
	if !contains(sent, "Use Testify") {
		t.Errorf("expected the prompt to contain 'Use Testify', got %.120q", sent)
	}
}

func hasAdjacent(args []string, a, b string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == a && args[i+1] == b {
			return true
		}
	}
	return false
}

func TestLintWithClaude_TimeoutReturnsERROR(t *testing.T) {
	c := NewClient(func(args []string, _ string) (string, error) {
		return "", ErrTimeout
	})

	got, err := c.Lint(sampleADR(), "+ code")
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	if got.Status != types.StatusERROR {
		t.Errorf("status = %q, want ERROR", got.Status)
	}
	if !contains(got.Explanation, "timeout") {
		t.Errorf("explanation = %q", got.Explanation)
	}
}

func TestLintWithClaude_CLINotFoundReturnsERROR(t *testing.T) {
	c := NewClient(func(args []string, _ string) (string, error) {
		return "", ErrCLINotFound
	})

	got, err := c.Lint(sampleADR(), "+ code")
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	if got.Status != types.StatusERROR {
		t.Errorf("status = %q, want ERROR", got.Status)
	}
	if !contains(got.Explanation, "Claude CLI not found") {
		t.Errorf("explanation = %q", got.Explanation)
	}
}

func TestLintWithClaude_FailResponseWithSuggestion(t *testing.T) {
	body := `{"status":"FAIL","confidence":"high","explanation":"Found gomock usage","violation":"gomock.NewController(t)","suggestion":"Use testify mocks instead"}`
	c := NewClient(func(args []string, _ string) (string, error) {
		return wrapCLIResponse(body), nil
	})

	got, err := c.Lint(sampleADR(), "+ gomock.NewController(t)")
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	if got.Status != types.StatusFAIL {
		t.Errorf("status = %q, want FAIL", got.Status)
	}
	if got.Explanation != "Found gomock usage" {
		t.Errorf("explanation = %q", got.Explanation)
	}
	if got.Suggestion == nil || *got.Suggestion != "Use testify mocks instead" {
		t.Errorf("suggestion = %v, want %q", got.Suggestion, "Use testify mocks instead")
	}
}

func TestLintWithClaude_PassResponse(t *testing.T) {
	body := `{"status":"PASS","confidence":"high","explanation":"No violations found"}`
	c := NewClient(func(args []string, _ string) (string, error) {
		return wrapCLIResponse(body), nil
	})

	got, err := c.Lint(sampleADR(), "+ vi.fn()")
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	if got.Status != types.StatusPASS {
		t.Errorf("status = %q, want PASS", got.Status)
	}
	if got.Explanation != "No violations found" {
		t.Errorf("explanation = %q", got.Explanation)
	}
}

func TestLintWithClaude_EmptyDiffReturnsSKIPPED(t *testing.T) {
	called := false
	c := NewClient(func(args []string, _ string) (string, error) {
		called = true
		return "", nil
	})

	got, err := c.Lint(sampleADR(), "")
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	if got.Status != types.StatusSKIPPED {
		t.Errorf("status = %q, want SKIPPED", got.Status)
	}
	if got.Explanation == "" || !contains(got.Explanation, "No changes") {
		t.Errorf("explanation = %q", got.Explanation)
	}
	if called {
		t.Error("runner should not be called for empty diff")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// THE PROMPT MUST NOT TRAVEL IN ARGV. Linux caps a SINGLE argv argument at
// MAX_ARG_STRLEN (128 KiB), independent of the 2 MiB ARG_MAX. A prompt carrying
// a full ADR body plus a real diff clears that wall, so the kernel rejects the
// exec before claude starts and the lint returns an error that mentions nothing
// about size.
//
// MEASURED on a Linux host 2026-08-20, against claude directly: argv of
// 100,000 B rc=0, 130,000 B rc=0, 200,000 B "Argument list too long". adr-lint
// on that host reviewed a 187-byte diff and failed a 23,301-byte real PR diff
// for this reason alone, reporting only "Claude CLI error: exit status 1".
//
// This asserts the CONTRACT rather than the symptom: the prompt reaches the
// runner as stdin, and no argv element contains it. A size-threshold test would
// pass on macOS, which is where this defect hid.
func TestLint_PromptGoesToStdinNotArgv(t *testing.T) {
	var captured []string
	var stdin string
	c := NewClient(func(args []string, in string) (string, error) {
		captured = append([]string(nil), args...)
		stdin = in
		return wrapCLIResponse(`{"status":"PASS","confidence":"high","explanation":"OK"}`), nil
	})

	const marker = "UNIQUE_PROMPT_MARKER_vi_fn_xyzzy"
	if _, err := c.Lint(sampleADR(), "+ "+marker); err != nil {
		t.Fatalf("Lint: %v", err)
	}

	if stdin == "" {
		t.Fatal("prompt was not passed on stdin")
	}
	if !strings.Contains(stdin, marker) {
		t.Fatalf("stdin does not carry the diff under review: %.120q", stdin)
	}
	for i, a := range captured {
		if strings.Contains(a, marker) {
			t.Fatalf("argv[%d] carries the prompt; it must go on stdin (MAX_ARG_STRLEN): %.80q", i, a)
		}
	}
	// `-p` must still be present and must NOT be followed by a prompt operand,
	// or claude reads the operand instead of stdin.
	var sawP bool
	for i, a := range captured {
		if a != "-p" {
			continue
		}
		sawP = true
		if i+1 < len(captured) && !strings.HasPrefix(captured[i+1], "-") {
			t.Fatalf("-p is followed by operand %q; claude would read it instead of stdin", captured[i+1])
		}
	}
	if !sawP {
		t.Fatal("-p missing; claude would not run in print mode")
	}
}
