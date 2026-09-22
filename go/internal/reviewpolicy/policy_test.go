package reviewpolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

const registryFixture = `version: 1
capabilities:
  visual-product-judgment:
    evidence: [rendered-preview]
  test-quality:
    evidence: []
`

func TestStrictPolicyAndScopes(t *testing.T) {
	r, err := ParseRegistry([]byte(registryFixture))
	if err != nil {
		t.Fatal(err)
	}
	p, err := ParseADR("doc/adr/0014-mobile.md", []byte(`---
status: accepted
tags: [ux]
applies_to:
  - paths: ["src/**/*.tsx", "!src/**/*.test.tsx"]
    review:
      requires: [visual-product-judgment]
  - paths: ["src/**/*.test.tsx"]
    review:
      requires: [test-quality]
---
# 0014. Mobile
`), r)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Scopes) != 2 || p.Scopes[0].Review.Requires[0] != "visual-product-judgment" {
		t.Fatalf("wrong scopes: %#v", p)
	}
	if !p.Scopes[0].Matches("src/routes/login.tsx") || p.Scopes[0].Matches("src/routes/login.test.tsx") {
		t.Fatal("scope exclusions ignored")
	}
}

func TestPolicyRejectsInvalidMetadata(t *testing.T) {
	r, err := ParseRegistry([]byte(registryFixture))
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ name, yaml string }{
		{"status", "status: acceptde\napplies_to: ['**']"},
		{"glob", "status: accepted\napplies_to: ['[bad']"},
		{"capability", "status: accepted\napplies_to: ['**']\nreview: {requires: [missing]}"},
		{"field", "status: accepted\napplies_to: ['**']\nreview: {require: [test-quality]}"},
		{"duplicate", "status: accepted\nstatus: proposed\napplies_to: ['**']"},
		{"missing scope", "status: accepted\nreview: {requires: [test-quality]}"},
		{"ambiguous", "status: accepted\napplies_to:\n  - paths: ['**']\n    review: {requires: [test-quality]}\nreview: {requires: [test-quality]}"},
		{"evidence", "status: accepted\napplies_to: ['**']\nreview: {requires: [test-quality], evidence: [imaginary]}"},
		{"empty glob", "status: accepted\napplies_to: ['']"},
		{"absolute glob", "status: accepted\napplies_to: ['/src/**']"},
		{"traversal glob", "status: accepted\napplies_to: ['../**']"},
		{"numeric typed path", "status: accepted\napplies_to: [{paths: [123]}]"},
		{"null requirements", "status: accepted\napplies_to: ['**']\nreview: {requires: null}"},
		{"null evidence", "status: accepted\napplies_to: ['**']\nreview: {requires: [test-quality], evidence: null}"},
		{"implicit empty waiver", "status: accepted\napplies_to: ['**']\nreview: {}"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseADR("doc/adr/0014-mobile.md", []byte("---\n"+tt.yaml+"\n---\n# 0014. Mobile\n"), r); err == nil {
				t.Fatal("accepted invalid metadata")
			}
		})
	}
	for _, raw := range []string{"version: 2\ncapabilities: {}", "version: 1\ncapabilities:\n  x: {evidnce: []}", strings.Replace(registryFixture, "version: 1", "version: 1\nversion: 1", 1)} {
		if _, err := ParseRegistry([]byte(raw)); err == nil {
			t.Fatal("accepted invalid registry")
		}
	}
}

func TestRetiredADRsAndLifecycleIntegrity(t *testing.T) {
	r, err := ParseRegistry([]byte(registryFixture))
	if err != nil {
		t.Fatal(err)
	}
	old, err := ParseADR("doc/adr/0010-old.md", []byte("---\nstatus: superseded\nsuperseded_by: ['0014']\napplies_to: []\n---\n"), r)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSet([]ADR{old}); err == nil {
		t.Fatal("accepted dangling successor")
	}
	current, err := ParseADR("doc/adr/0014-mobile.md", []byte("---\nstatus: accepted\napplies_to: ['**']\n---\n"), r)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSet([]ADR{old, current}); err != nil {
		t.Fatal(err)
	}
	current.SupersededBy = []string{"0010"}
	if err := ValidateSet([]ADR{old, current}); err == nil {
		t.Fatal("accepted successor cycle")
	}
}

func TestPolicyDigestCoversOriginalBlob(t *testing.T) {
	r, err := ParseRegistry([]byte(registryFixture))
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("---\r\nstatus: accepted\r\napplies_to: ['**']\r\n---\r\n# Normative decision\r\n")
	a, err := ParseADR("doc/adr/0001-record.md", raw, r)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if a.PolicyDigest != hex.EncodeToString(sum[:]) {
		t.Fatal("digest did not bind complete original blob")
	}
	lf, err := ParseADR("doc/adr/0001-record.md", []byte(strings.ReplaceAll(string(raw), "\r\n", "\n")), r)
	if err != nil {
		t.Fatal(err)
	}
	if a.PolicyDigest == lf.PolicyDigest {
		t.Fatal("digest was computed after normalization")
	}
}
