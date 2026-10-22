package sharedpacket

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestSharedFragmentsPreserveWholePolicyWithoutCartesianCopies(t *testing.T) {
	inputs := []Input{{Kind: "policy", CanonicalRef: Ref{Collection: "policies", Index: 0, Field: "content"}, Content: strings.Repeat("Å shared rule <value>\n", 4000), Metadata: map[string]any{"path": "doc/adr/0040-rule.md"}},
		{Kind: "source", CanonicalRef: Ref{Collection: "content", Index: 0, Field: "content"}, Content: "export const x = true\n", Metadata: map[string]any{"path": "a.ts"}}}
	scopes := []ScopeInput{{ID: "scope-a", Paths: []string{"a.ts"}, ArtifactIndices: []int{0, 1}}, {ID: "scope-b", Paths: []string{"a.ts"}, ArtifactIndices: []int{0, 1}}}
	p, err := Build("coverage", inputs, scopes, Options{MaxFragmentInputTokens: 4096, MaxFragments: 256})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Artifacts) != 2 || len(p.Scopes) != 2 || len(p.Atoms) < 2 || p.Limits.FragmentLimitExceeded {
		t.Fatalf("bad shared inventory: %+v", p)
	}
	for index, input := range inputs {
		var reconstructed bytes.Buffer
		for _, id := range p.Artifacts[index].Atoms {
			payload, err := Materialize(p, id, inputs, p.Fingerprint)
			if err != nil {
				t.Fatal(err)
			}
			if (len(payload)+3)/4 > 4096 {
				t.Fatalf("materialized fragment over budget: %d", len(payload))
			}
			var envelope struct {
				Content string `json:"content"`
			}
			if err := json.Unmarshal(payload, &envelope); err != nil {
				t.Fatal(err)
			}
			reconstructed.WriteString(envelope.Content)
		}
		if reconstructed.String() != input.Content {
			t.Fatal("policy/source bytes omitted or repeated")
		}
	}
	if len(p.Scopes[0].ArtifactIDs) != 2 || p.Scopes[0].ArtifactIDs[0] != p.Scopes[1].ArtifactIDs[0] || !p.Scopes[0].CrossPartAssessmentRequired {
		t.Fatal("whole scope lost shared identity")
	}
	inputs[0].Content += "tamper"
	if _, err := Materialize(p, p.Atoms[0].ID, inputs, p.Fingerprint); err == nil {
		t.Fatal("changed canonical bytes accepted")
	}
}
func TestSharedCapacityRetainsEverythingAndEmptyArtifact(t *testing.T) {
	inputs := []Input{{Kind: "source", CanonicalRef: Ref{Collection: "content", Field: "content"}, Content: ""}, {Kind: "policy", CanonicalRef: Ref{Collection: "policies", Field: "content"}, Content: strings.Repeat("rule ", 1000)}}
	p, err := Build("coverage", inputs, []ScopeInput{{ID: "baseline", Paths: []string{"empty.ts"}, ArtifactIndices: []int{0, 1}}}, Options{MaxFragmentInputTokens: 128, MaxFragments: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Limits.FragmentLimitExceeded || len(p.Atoms) <= 1 || len(p.Artifacts[0].Atoms) != 1 {
		t.Fatal("budget/empty artifact silently dropped")
	}
}

func TestMaterializationRefusesOverCapacityAndRehashedForeignPlan(t *testing.T) {
	inputs := []Input{{Kind: "source", CanonicalRef: Ref{Collection: "content", Field: "content"}, Content: strings.Repeat("code ", 1000)}}
	p, err := Build("coverage", inputs, []ScopeInput{{ID: "baseline", Paths: []string{"a.ts"}, ArtifactIndices: []int{0}}}, Options{MaxFragmentInputTokens: 128, MaxFragments: 1})
	if err != nil {
		t.Fatal(err)
	}
	trusted := p.Fingerprint
	if _, err := Materialize(p, p.Atoms[0].ID, inputs, trusted); err == nil {
		t.Fatal("overcapacity plan admitted to materialization")
	}
	p.Limits.MaxFragments = 1000
	p.Limits.FragmentLimitExceeded = false
	p.Fingerprint = ""
	p.Fingerprint = hash(p)
	if _, err := Materialize(p, p.Atoms[0].ID, inputs, trusted); err == nil {
		t.Fatal("self-rehashed untrusted plan accepted")
	}
}
