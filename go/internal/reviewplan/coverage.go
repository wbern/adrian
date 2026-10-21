// Code coverage is a separate contract from capability routing. An unannotated
// accepted rule still needs assessment even when every path is classified.
package reviewplan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/wbern/adrian/go/internal/reviewpolicy"
)

const CoverageSchema = "adrian-code-coverage-v1"

type MatchedScope struct {
	Source
	ID                   string              `json:"id"`
	ReviewDeclared       bool                `json:"review_declared"`
	Capabilities         []string            `json:"capabilities"`
	EvidenceByCapability map[string][]string `json:"evidence_by_capability"`
}

type PathVersion struct {
	Path   string `json:"path"`
	Side   string `json:"side"`
	Commit string `json:"commit"`
}

type PolicyIdentity struct {
	ADR    string `json:"adr"`
	File   string `json:"file"`
	Status string `json:"status"`
	Digest string `json:"digest"`
}

type CoveragePlan struct {
	Schema         string           `json:"schema"`
	Semantic       Plan             `json:"semantic"`
	Applicability  []MatchedScope   `json:"applicability"`
	Baseline       []PathVersion    `json:"baseline"`
	PolicyManifest []PolicyIdentity `json:"policy_manifest"`
	RegistryDigest string           `json:"registry_digest"`
	ManifestDigest string           `json:"manifest_digest"`
	Fingerprint    string           `json:"fingerprint"`
}

// BuildCoverage takes the same complete, validated producer inputs as Build.
// It never narrows rules with pre_filter, enforced_by or routing annotations.
// IDs and hashes identify inventories; the consumer independently pins the
// expected producer hash and authenticates execution evidence.
func BuildCoverage(provenance Provenance, changes []Change, policies []reviewpolicy.ADR, registry reviewpolicy.Registry) CoveragePlan {
	semantic := Build(provenance, changes, policies, registry)
	p := CoveragePlan{Schema: CoverageSchema, Semantic: semantic, Applicability: []MatchedScope{}, Baseline: []PathVersion{}, PolicyManifest: []PolicyIdentity{}, RegistryDigest: coverageHash(registry), ManifestDigest: coverageHash(semantic.Changes)}
	paths := map[string]bool{}
	versions := map[string]PathVersion{}
	for _, c := range semantic.Changes {
		side, commit := "head", provenance.HeadCommit
		if c.Status == "D" {
			side, commit = "base", provenance.MergeBase
		}
		v := PathVersion{Path: c.Path, Side: side, Commit: commit}
		versions[coverageHash(v)] = v
		paths[c.Path] = true
		if c.OldPath != "" {
			v = PathVersion{Path: c.OldPath, Side: "base", Commit: provenance.MergeBase}
			versions[coverageHash(v)] = v
			paths[c.OldPath] = true
		}
	}
	for _, v := range versions {
		p.Baseline = append(p.Baseline, v)
	}
	sort.Slice(p.Baseline, func(i, j int) bool {
		a, b := p.Baseline[i], p.Baseline[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Side < b.Side
	})
	for _, a := range policies {
		p.PolicyManifest = append(p.PolicyManifest, PolicyIdentity{ADR: a.ID, File: a.File, Status: a.Status, Digest: a.PolicyDigest})
		if a.Status != "accepted" {
			continue
		}
		for i, s := range a.Scopes {
			matched := matchedPaths(s, paths)
			if len(matched) == 0 {
				continue
			}
			row := MatchedScope{Source: Source{ADR: a.ID, File: a.File, Scope: fmt.Sprintf("%d", i+1), Paths: keys(matched), PolicyDigest: a.PolicyDigest}, ReviewDeclared: s.ReviewDeclared, Capabilities: union(s.Review.Requires), EvidenceByCapability: map[string][]string{}}
			for _, capability := range row.Capabilities {
				row.EvidenceByCapability[capability] = union(registry.Capabilities[capability].Evidence, s.Review.Evidence)
			}
			row.ID = coverageHash(row)
			p.Applicability = append(p.Applicability, row)
		}
	}
	sort.Slice(p.PolicyManifest, func(i, j int) bool { return p.PolicyManifest[i].File < p.PolicyManifest[j].File })
	sort.Slice(p.Applicability, func(i, j int) bool {
		a, b := p.Applicability[i], p.Applicability[j]
		if a.ADR != b.ADR {
			return a.ADR < b.ADR
		}
		return a.Scope < b.Scope
	})
	p.Fingerprint = coverageHash(p)
	return p
}

func matchedPaths(scope reviewpolicy.Scope, paths map[string]bool) map[string]bool {
	matched := map[string]bool{}
	for file := range paths {
		if scope.Matches(file) {
			matched[file] = true
		}
	}
	return matched
}

func coverageHash(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
