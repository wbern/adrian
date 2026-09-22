package claudeclient

import (
	"testing"
)

// What a check COST has to survive the run, not be discarded with the CLI
// envelope. A consumer that reports "0 tokens" for a paid call is worse than
// one that reports nothing: a zeroed cost reads as a free review.

func TestLint_PopulatesTokenUsageFromCLIUsage(t *testing.T) {
	body := `{"status":"PASS","confidence":"high","explanation":"No violations found"}`
	envelope := `{"type":"result","result":"","structured_output":` + body +
		`,"usage":{"input_tokens":1500,"output_tokens":100}}`
	c := NewClient(func(args []string, _ string) (string, error) { return envelope, nil })

	got, err := c.Lint(sampleADR(), "+ vi.fn()")
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	if got.TokenUsage == nil {
		t.Fatal("TokenUsage = nil; the CLI reported usage and it was discarded")
	}
	if got.TokenUsage.PromptTokens != 1500 {
		t.Errorf("PromptTokens = %d, want 1500", got.TokenUsage.PromptTokens)
	}
	if got.TokenUsage.CompletionTokens != 100 {
		t.Errorf("CompletionTokens = %d, want 100", got.TokenUsage.CompletionTokens)
	}
	if got.TokenUsage.TotalTokens != 1600 {
		t.Errorf("TotalTokens = %d, want 1600", got.TokenUsage.TotalTokens)
	}
	// Attribution matters as much as the number: cost per model is the whole
	// point of per-ADR complexity tiers.
	if got.TokenUsage.Model == "" {
		t.Error("TokenUsage.Model is empty; the cost cannot be attributed to a model")
	}
}

func TestLint_CountsCachedInputTokens(t *testing.T) {
	// Cache reads are real input the model processed. Omitting them
	// under-reports spend on exactly the runs that repeat most.
	body := `{"status":"PASS","confidence":"high","explanation":"ok"}`
	envelope := `{"type":"result","result":"","structured_output":` + body +
		`,"usage":{"input_tokens":100,"output_tokens":10,"cache_read_input_tokens":900,"cache_creation_input_tokens":50}}`
	c := NewClient(func(args []string, _ string) (string, error) { return envelope, nil })

	got, err := c.Lint(sampleADR(), "+ vi.fn()")
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	if got.TokenUsage == nil {
		t.Fatal("TokenUsage = nil")
	}
	if got.TokenUsage.PromptTokens != 1050 {
		t.Errorf("PromptTokens = %d, want 1050 (100 fresh + 900 cache-read + 50 cache-create)", got.TokenUsage.PromptTokens)
	}
	if got.TokenUsage.CachedTokens == nil || *got.TokenUsage.CachedTokens != 900 {
		t.Errorf("CachedTokens = %v, want 900", got.TokenUsage.CachedTokens)
	}
}

func TestLint_OmitsTokenUsageWhenCLIReportsNone(t *testing.T) {
	// ABSENCE OVER FABRICATION. If the CLI reported no usage, we must report
	// no usage — not a zero, which a consumer cannot distinguish from a free
	// call.
	body := `{"status":"PASS","confidence":"high","explanation":"ok"}`
	envelope := `{"type":"result","result":"","structured_output":` + body + `}`
	c := NewClient(func(args []string, _ string) (string, error) { return envelope, nil })

	got, err := c.Lint(sampleADR(), "+ vi.fn()")
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	if got.TokenUsage != nil {
		t.Errorf("TokenUsage = %+v, want nil when the CLI reported no usage", got.TokenUsage)
	}
}

func TestLint_RecordsPromptBytes(t *testing.T) {
	// The honest analogue of the reviewer's prompt_payload_bytes: what we
	// actually sent, measured rather than estimated, so "what did policy
	// enforcement cost" is answerable without a tokenizer.
	body := `{"status":"PASS","confidence":"high","explanation":"ok"}`
	envelope := `{"type":"result","result":"","structured_output":` + body + `}`
	var sentPrompt string
	c := NewClient(func(args []string, in string) (string, error) {
		// The prompt travels on STDIN, not as the operand of -p
		// (MAX_ARG_STRLEN). The assertion below is unchanged: the reported
		// PromptBytes must equal the bytes actually sent.
		sentPrompt = in
		return envelope, nil
	})

	got, err := c.Lint(sampleADR(), "+ vi.fn()")
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	if sentPrompt == "" {
		t.Fatal("no prompt was captured from the invocation")
	}
	if got.PromptBytes != len(sentPrompt) {
		t.Errorf("PromptBytes = %d, want %d (the bytes actually sent)", got.PromptBytes, len(sentPrompt))
	}
}

func TestLint_SkippedDiffCostsNothing(t *testing.T) {
	// An empty diff never reaches the model, so it must not claim any cost.
	c := NewClient(func(args []string, _ string) (string, error) {
		t.Fatal("the model was invoked for an empty diff")
		return "", nil
	})
	got, err := c.Lint(sampleADR(), "   ")
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	if got.PromptBytes != 0 {
		t.Errorf("PromptBytes = %d, want 0 for a skipped check", got.PromptBytes)
	}
	if got.TokenUsage != nil {
		t.Errorf("TokenUsage = %+v, want nil for a skipped check", got.TokenUsage)
	}
}
