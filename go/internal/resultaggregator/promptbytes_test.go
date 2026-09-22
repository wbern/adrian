package resultaggregator

import (
	"testing"

	"github.com/wbern/adrian/go/internal/adr"
	"github.com/wbern/adrian/go/internal/types"
)

// A large diff is chunked and each chunk is a separate paid call. If only one
// chunk's cost survives the merge, the reported cost of an ADR falls as the
// diff it was checked against grows — backwards, and in the direction that
// flatters the tool.
func TestAggregate_SumsPromptBytesAcrossChunks(t *testing.T) {
	a := adr.ADR{ID: "0001", Title: "Rule"}
	chunks := []types.LintResult{
		{ADR: a, Status: types.StatusPASS, Explanation: "ok", PromptBytes: 1200},
		{ADR: a, Status: types.StatusPASS, Explanation: "ok", PromptBytes: 800},
		{ADR: a, Status: types.StatusPASS, Explanation: "ok", PromptBytes: 450},
	}
	got := Aggregate(chunks, a)
	if got.PromptBytes != 2450 {
		t.Errorf("PromptBytes = %d, want 2450 (sum of every chunk actually sent)", got.PromptBytes)
	}
}

func TestAggregate_PromptBytesZeroWhenNothingSent(t *testing.T) {
	a := adr.ADR{ID: "0001", Title: "Rule"}
	chunks := []types.LintResult{
		{ADR: a, Status: types.StatusSKIPPED, Explanation: "no changes"},
	}
	if got := Aggregate(chunks, a); got.PromptBytes != 0 {
		t.Errorf("PromptBytes = %d, want 0", got.PromptBytes)
	}
}
