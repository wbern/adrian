package reviewplan

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/wbern/adrian/go/internal/reviewpolicy"
)

func TestProductionTestsAndMixed(t *testing.T) {
	r, err := reviewpolicy.ParseRegistry([]byte("version: 1\ncapabilities:\n  visual-product-judgment: {evidence: [rendered-preview]}\n  test-quality: {evidence: []}\n"))
	if err != nil {
		t.Fatal(err)
	}
	var policies []reviewpolicy.ADR
	for path, content := range map[string]string{
		"doc/adr/0014-mobile.md":   "status: accepted\napplies_to: ['src/**/*.tsx', '!src/**/*.test.tsx']\nreview: {requires: [visual-product-judgment]}",
		"doc/adr/0080-tests.md":    "status: accepted\napplies_to: ['src/**/*.test.tsx']\nenforced_by: review lens\nreview: {requires: [test-quality]}",
		"doc/adr/0081-proposed.md": "status: proposed\napplies_to: ['**']\nreview: {requires: [visual-product-judgment]}",
	} {
		a, err := reviewpolicy.ParseADR(path, []byte("---\n"+content+"\n---\n"), r)
		if err != nil {
			t.Fatal(err)
		}
		policies = append(policies, a)
	}
	for _, tt := range []struct {
		name     string
		changes  []Change
		expected []string
	}{
		{"test", []Change{{Status: "M", Path: "src/routes/login.test.tsx"}}, []string{"test-quality"}},
		{"production", []Change{{Status: "M", Path: "src/routes/login.tsx"}}, []string{"visual-product-judgment"}},
		{"mixed", []Change{{Status: "M", Path: "src/routes/login.tsx"}, {Status: "M", Path: "src/routes/login.test.tsx"}}, []string{"test-quality", "visual-product-judgment"}},
		{"rename escapes scope", []Change{{Status: "R100", Path: "archive/login.txt", OldPath: "src/routes/login.tsx"}}, []string{"visual-product-judgment"}},
		{"deletion", []Change{{Status: "D", Path: "src/routes/login.tsx"}}, []string{"visual-product-judgment"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := Build(Provenance{HeadCommit: "head", PolicyCommit: "policy"}, tt.changes, policies, r)
			got := []string{}
			for _, requirement := range p.Requirements {
				got = append(got, requirement.Capability)
			}
			if !reflect.DeepEqual(got, tt.expected) {
				t.Fatalf("got %v want %v", got, tt.expected)
			}
			for _, requirement := range p.Requirements {
				if requirement.Capability == "visual-product-judgment" && !reflect.DeepEqual(requirement.Evidence, []string{"rendered-preview"}) {
					t.Fatalf("missing contract evidence: %#v", requirement)
				}
			}
			a, _ := json.Marshal(p)
			for i := 0; i < 10; i++ {
				policies[0], policies[1] = policies[1], policies[0]
				b, _ := json.Marshal(Build(p.Provenance, tt.changes, policies, r))
				if string(a) != string(b) {
					t.Fatal("nondeterministic plan")
				}
			}
		})
	}
}

func TestLegacyCoverageDoesNotWaiveReviewClassification(t *testing.T) {
	r, err := reviewpolicy.ParseRegistry([]byte("version: 1\ncapabilities:\n  security: {evidence: []}\n"))
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := reviewpolicy.ParseADR("doc/adr/0001-broad.md", []byte("---\nstatus: accepted\napplies_to: ['**']\n---\n"), r)
	if err != nil {
		t.Fatal(err)
	}
	changes := []Change{{Status: "R100", OldPath: "src/auth.ts", Path: "server/auth.ts"}}
	p := Build(Provenance{}, changes, []reviewpolicy.ADR{legacy}, r)
	if len(p.UnmatchedADRPaths) != 0 {
		t.Fatal("broad ADR does apply")
	}
	if !reflect.DeepEqual(p.UnclassifiedPaths, []string{"server/auth.ts", "src/auth.ts"}) {
		t.Fatalf("unannotated ADR waived classification: %#v", p)
	}
	classified, err := reviewpolicy.ParseADR("doc/adr/0002-routing.md", []byte("---\nstatus: accepted\napplies_to:\n  - paths: ['src/**']\n    review: {requires: []}\n---\n"), r)
	if err != nil {
		t.Fatal(err)
	}
	p = Build(Provenance{}, changes, []reviewpolicy.ADR{legacy, classified}, r)
	if !reflect.DeepEqual(p.UnclassifiedPaths, []string{"server/auth.ts"}) {
		t.Fatalf("explicit empty requirement didn't classify only its scope: %#v", p)
	}
}

func TestObligationFingerprintIgnoresUnrelatedPolicyProvenance(t *testing.T) {
	r, err := reviewpolicy.ParseRegistry([]byte("version: 1\ncapabilities:\n  security: {evidence: [authorization-proof]}\n"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := reviewpolicy.ParseADR("doc/adr/0001-auth.md", []byte("---\nstatus: accepted\napplies_to: ['src/auth.ts']\nreview: {requires: [security]}\n---\n"), r)
	if err != nil {
		t.Fatal(err)
	}
	changes := []Change{{Status: "M", Path: "src/auth.ts"}}
	p := Build(Provenance{BaseCommit: "base1", PolicyCommit: "policy1", HeadCommit: "head"}, changes, []reviewpolicy.ADR{a}, r)
	q := Build(Provenance{BaseCommit: "base2", PolicyCommit: "policy2", HeadCommit: "head"}, changes, []reviewpolicy.ADR{a}, r)
	if p.Fingerprint == q.Fingerprint {
		t.Fatal("full plan identity should change with pinned provenance")
	}
	if p.ObligationFingerprint == "" || p.ObligationFingerprint != q.ObligationFingerprint {
		t.Fatal("unrelated policy provenance invalidated obligations")
	}
	a.Scopes[0].Review.Requires = []string{}
	q = Build(p.Provenance, changes, []reviewpolicy.ADR{a}, r)
	if p.ObligationFingerprint == q.ObligationFingerprint {
		t.Fatal("removed obligation didn't change obligation fingerprint")
	}
}

func TestObligationFingerprintBindsMatchedADRBlob(t *testing.T) {
	r, err := reviewpolicy.ParseRegistry([]byte("version: 1\ncapabilities:\n  security: {evidence: [authorization-proof]}\n"))
	if err != nil {
		t.Fatal(err)
	}
	parse := func(file, scope, body string) reviewpolicy.ADR {
		t.Helper()
		a, err := reviewpolicy.ParseADR(file, []byte("---\nstatus: accepted\napplies_to: ['"+scope+"']\nreview: {requires: [security]}\n---\n"+body), r)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	matched := parse("doc/adr/0001-auth.md", "src/auth.ts", "# Auth\n\n## Decision\nRequire owner authorization.\n")
	unrelated := parse("doc/adr/0002-unrelated.md", "elsewhere/**", "# Elsewhere\n\n## Decision\nOld rule.\n")
	changes := []Change{{Status: "M", Path: "src/auth.ts"}}
	p := Build(Provenance{PolicyCommit: "old"}, changes, []reviewpolicy.ADR{matched, unrelated}, r)
	if len(p.Requirements[0].Sources[0].PolicyDigest) != 64 {
		t.Fatal("missing matched ADR digest")
	}
	unrelated = parse("doc/adr/0002-unrelated.md", "elsewhere/**", "# Elsewhere\n\n## Decision\nChanged unrelated rule.\n")
	q := Build(Provenance{PolicyCommit: "new"}, changes, []reviewpolicy.ADR{matched, unrelated}, r)
	if p.ObligationFingerprint != q.ObligationFingerprint {
		t.Fatal("unrelated ADR change invalidated evidence")
	}
	matched = parse("doc/adr/0001-auth.md", "src/auth.ts", "# Auth\n\n## Decision\nRequire owner authorization and tenant isolation.\n")
	q = Build(p.Provenance, changes, []reviewpolicy.ADR{matched, unrelated}, r)
	if p.ObligationFingerprint == q.ObligationFingerprint {
		t.Fatal("matched normative body changed without invalidating evidence")
	}
}
