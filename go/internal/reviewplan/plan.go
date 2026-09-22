// Package reviewplan computes semantic review requirements without executing
// reviewers, invoking models, or depending on any orchestration system.
package reviewplan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/wbern/adrian/go/internal/reviewpolicy"
)

type Change struct {
	Status  string `json:"status"`
	Path    string `json:"path"`
	OldPath string `json:"old_path,omitempty"`
}

type Provenance struct {
	BaseCommit   string `json:"base_commit"`
	MergeBase    string `json:"merge_base"`
	HeadCommit   string `json:"head_commit"`
	PolicyCommit string `json:"policy_commit"`
}

type Source struct {
	ADR          string   `json:"adr"`
	File         string   `json:"file"`
	Scope        string   `json:"scope"`
	Paths        []string `json:"paths"`
	PolicyDigest string   `json:"policy_digest"`
}

type Requirement struct {
	Capability string   `json:"capability"`
	Evidence   []string `json:"evidence"`
	Sources    []Source `json:"sources"`
}

type Plan struct {
	Schema int `json:"schema"`
	Provenance
	Changes               []Change      `json:"changes"`
	Requirements          []Requirement `json:"requirements"`
	ApplicableADRs        []string      `json:"applicable_adrs"`
	UnmatchedADRPaths     []string      `json:"unmatched_adr_paths"`
	UnclassifiedPaths     []string      `json:"unclassified_paths"`
	Fingerprint           string        `json:"fingerprint"`
	ObligationFingerprint string        `json:"obligation_fingerprint"`
}

// Build requires validated policy and a complete Git change manifest. It sorts
// all set-valued output so repeated runs produce identical JSON.
func Build(provenance Provenance, changes []Change, policies []reviewpolicy.ADR, registry reviewpolicy.Registry) Plan {
	p := Plan{Schema: 1, Provenance: provenance, Changes: append([]Change{}, changes...), Requirements: []Requirement{}, ApplicableADRs: []string{}, UnmatchedADRPaths: []string{}, UnclassifiedPaths: []string{}}
	sort.Slice(p.Changes, func(i, j int) bool {
		a, b := p.Changes[i], p.Changes[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.OldPath != b.OldPath {
			return a.OldPath < b.OldPath
		}
		return a.Status < b.Status
	})
	paths := map[string]bool{}
	for _, c := range changes {
		paths[c.Path] = true
		if c.OldPath != "" {
			paths[c.OldPath] = true
		}
	}
	covered := map[string]bool{}
	classified := map[string]bool{}
	applicable := map[string]bool{}
	requirements := map[string]*Requirement{}
	for _, policy := range policies {
		if policy.Status != "accepted" {
			continue
		}
		for i, scope := range policy.Scopes {
			matched := map[string]bool{}
			for file := range paths {
				if scope.Matches(file) {
					matched[file] = true
					covered[file] = true
					if scope.ReviewDeclared {
						classified[file] = true
					}
				}
			}
			if len(matched) == 0 {
				continue
			}
			applicable[policy.ID] = true
			for _, capability := range scope.Review.Requires {
				r := requirements[capability]
				if r == nil {
					r = &Requirement{Capability: capability, Evidence: []string{}, Sources: []Source{}}
					requirements[capability] = r
				}
				r.Evidence = union(r.Evidence, registry.Capabilities[capability].Evidence, scope.Review.Evidence)
				r.Sources = append(r.Sources, Source{ADR: policy.ID, File: policy.File, Scope: fmt.Sprintf("%d", i+1), Paths: keys(matched), PolicyDigest: policy.PolicyDigest})
			}
		}
	}
	for _, r := range requirements {
		sort.Slice(r.Sources, func(i, j int) bool {
			a, b := r.Sources[i], r.Sources[j]
			if a.ADR != b.ADR {
				return a.ADR < b.ADR
			}
			return a.Scope < b.Scope
		})
		p.Requirements = append(p.Requirements, *r)
	}
	sort.Slice(p.Requirements, func(i, j int) bool { return p.Requirements[i].Capability < p.Requirements[j].Capability })
	p.ApplicableADRs = keys(applicable)
	for file := range paths {
		if !covered[file] {
			p.UnmatchedADRPaths = append(p.UnmatchedADRPaths, file)
		}
		if !classified[file] {
			p.UnclassifiedPaths = append(p.UnclassifiedPaths, file)
		}
	}
	sort.Strings(p.UnmatchedADRPaths)
	sort.Strings(p.UnclassifiedPaths)
	// Sources already carry every path with an actual requirement. Paths with
	// an explicit empty obligation need no specialist evidence; unclassified
	// paths remain part of this contract so a coverage loss cannot reuse a waiver.
	obligations := struct {
		Schema            int           `json:"schema"`
		Requirements      []Requirement `json:"requirements"`
		UnclassifiedPaths []string      `json:"unclassified_paths"`
	}{Schema: p.Schema, Requirements: p.Requirements, UnclassifiedPaths: p.UnclassifiedPaths}
	encoded, _ := json.Marshal(obligations)
	sum := sha256.Sum256(encoded)
	p.ObligationFingerprint = hex.EncodeToString(sum[:])
	encoded, _ = json.Marshal(p)
	sum = sha256.Sum256(encoded)
	p.Fingerprint = hex.EncodeToString(sum[:])
	return p
}

func keys(m map[string]bool) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func union(parts ...[]string) []string {
	m := map[string]bool{}
	for _, part := range parts {
		for _, s := range part {
			m[s] = true
		}
	}
	return keys(m)
}
