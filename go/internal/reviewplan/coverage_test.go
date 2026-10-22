package reviewplan

import (
	"encoding/json"
	"testing"

	"github.com/wbern/adrian/go/internal/reviewpolicy"
)

func TestFullApplicabilityRetainsUnannotatedScopesAndRenameSides(t *testing.T) {
	policies := []reviewpolicy.ADR{
		{ID: "0001", File: "doc/adr/0001-rule.md", Status: "accepted", PolicyDigest: "digest1", Scopes: []reviewpolicy.Scope{{Paths: []string{"src/**"}}, {Paths: []string{"old/**"}, ReviewDeclared: true}}},
		{ID: "0002", File: "doc/adr/0002-rule.md", Status: "proposed", PolicyDigest: "digest2", Scopes: []reviewpolicy.Scope{{Paths: []string{"**/*"}}}},
	}
	p := BuildCoverage(Provenance{}, []Change{{Status: "R100", OldPath: "old/a.ts", Path: "src/a.ts"}, {Status: "D", Path: "src/deleted.ts"}}, policies, reviewpolicy.Registry{})
	raw, _ := json.Marshal(p)
	var decoded map[string]json.RawMessage
	_ = json.Unmarshal(raw, &decoded)
	var edges []struct {
		ADR            string   `json:"adr"`
		Scope          string   `json:"scope"`
		Paths          []string `json:"paths"`
		PolicyDigest   string   `json:"policy_digest"`
		ReviewDeclared bool     `json:"review_declared"`
	}
	if err := json.Unmarshal(decoded["applicability"], &edges); err != nil {
		t.Fatalf("complete applicability missing: %v", err)
	}
	if len(edges) != 2 || edges[0].ADR != "0001" || edges[0].Scope != "1" || len(edges[0].Paths) != 2 || edges[0].PolicyDigest != "digest1" || edges[0].ReviewDeclared || !edges[1].ReviewDeclared || edges[1].Paths[0] != "old/a.ts" {
		t.Fatalf("incomplete scopes: %+v", edges)
	}
	if len(p.Semantic.Requirements) != 0 {
		t.Fatal("fallback census must not invent capability requirements")
	}
}

func TestCoverageEvidenceRemainsLocalAndOrderIndependent(t *testing.T) {
	r := reviewpolicy.Registry{Version: 1, Capabilities: map[string]reviewpolicy.Capability{"proof": {Evidence: []string{"runtime"}}}}
	policies := []reviewpolicy.ADR{{ID: "0001", File: "doc/adr/0001-a.md", Status: "accepted", PolicyDigest: "a", Scopes: []reviewpolicy.Scope{
		{Paths: []string{"a.ts"}, ReviewDeclared: true, Review: reviewpolicy.Review{Requires: []string{"proof"}, Evidence: []string{"browser"}}},
		{Paths: []string{"b.ts"}, ReviewDeclared: true, Review: reviewpolicy.Review{Requires: []string{"proof"}}},
	}}, {ID: "0002", File: "doc/adr/0002-b.md", Status: "accepted", PolicyDigest: "b", Scopes: []reviewpolicy.Scope{{Paths: []string{"a.ts"}}}}}
	changes := []Change{{Status: "M", Path: "a.ts"}, {Status: "M", Path: "b.ts"}, {Status: "M", Path: "unmatched.txt"}}
	p := BuildCoverage(Provenance{HeadCommit: "head", MergeBase: "base"}, changes, policies, r)
	if len(p.Applicability) != 3 || len(p.Baseline) != 3 {
		t.Fatalf("lost rows: %+v", p)
	}
	if len(p.Applicability[0].EvidenceByCapability["proof"]) != 2 || len(p.Applicability[1].EvidenceByCapability["proof"]) != 1 {
		t.Fatal("scope evidence leaked")
	}
	reverse := []reviewpolicy.ADR{policies[1], policies[0]}
	reordered := []Change{changes[2], changes[1], changes[0]}
	if other := BuildCoverage(p.Semantic.Provenance, reordered, reverse, r); other.Fingerprint != p.Fingerprint {
		t.Fatal("order changed inventory hash")
	}
	policies[1].PolicyDigest = "changed rule body"
	if other := BuildCoverage(p.Semantic.Provenance, changes, policies, r); other.Fingerprint == p.Fingerprint {
		t.Fatal("policy text change invisible")
	}
}
