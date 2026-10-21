// Package sharedpacket packs immutable context once. Fragments are transport
// atoms, never independent rule assessments; a scope requires all its parts.
package sharedpacket

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"unicode/utf8"
)

const Schema = "adrian-shared-delivery-v1"

type Ref struct {
	Collection string `json:"collection"`
	Index      int    `json:"index"`
	Field      string `json:"field"`
}
type Input struct {
	Kind         string
	CanonicalRef Ref
	Encoding     string
	Content      string
	Metadata     map[string]any
}
type ScopeInput struct {
	ID              string
	Paths           []string
	ArtifactIndices []int
}
type Options struct {
	MaxFragmentInputTokens int
	MaxFragments           int
}
type Artifact struct {
	ID                   string         `json:"id"`
	Kind                 string         `json:"kind"`
	CanonicalRef         Ref            `json:"canonical_ref"`
	Encoding             string         `json:"encoding"`
	RepresentationSHA256 string         `json:"representation_sha256"`
	ByteLength           int            `json:"byte_length"`
	Metadata             map[string]any `json:"metadata"`
	Atoms                []string       `json:"atoms"`
}
type Atom struct {
	ID                   string `json:"id"`
	ArtifactID           string `json:"artifact_id"`
	Offset               int    `json:"offset"`
	Length               int    `json:"length"`
	SHA256               string `json:"sha256"`
	EstimatedInputTokens int    `json:"estimated_input_tokens"`
}
type Scope struct {
	ID                          string   `json:"id"`
	Paths                       []string `json:"paths"`
	ArtifactIDs                 []string `json:"artifact_ids"`
	AtomIDs                     []string `json:"atom_ids"`
	ContextBytes                int      `json:"context_bytes"`
	EstimatedMaterializedTokens int      `json:"estimated_materialized_tokens"`
	CrossPartAssessmentRequired bool     `json:"cross_part_assessment_required"`
}
type Limits struct {
	MaxFragmentInputTokens int  `json:"max_fragment_input_tokens"`
	MaxFragments           int  `json:"max_fragments"`
	FragmentLimitExceeded  bool `json:"fragment_limit_exceeded"`
}
type Plan struct {
	Schema              string     `json:"schema"`
	CoverageFingerprint string     `json:"coverage_fingerprint"`
	Artifacts           []Artifact `json:"artifacts"`
	Atoms               []Atom     `json:"atoms"`
	Scopes              []Scope    `json:"scopes"`
	Limits              Limits     `json:"limits"`
	Fingerprint         string     `json:"fingerprint"`
}
type envelope struct {
	ArtifactID string         `json:"artifact_id"`
	Kind       string         `json:"kind"`
	Offset     int            `json:"offset"`
	Content    string         `json:"content"`
	Metadata   map[string]any `json:"metadata"`
}

func compact(value any) ([]byte, error) {
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(out.Bytes(), []byte{'\n'}), nil
}
func hash(value any) string {
	raw, err := compact(value)
	if err != nil {
		panic(err)
	}
	return digest(raw)
}
func digest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func artifactFor(input Input) Artifact {
	metadata := input.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	encoding := input.Encoding
	if encoding == "" {
		encoding = "utf-8"
	}
	a := Artifact{Kind: input.Kind, CanonicalRef: input.CanonicalRef, Encoding: encoding, RepresentationSHA256: digest([]byte(input.Content)), ByteLength: len(input.Content), Metadata: metadata, Atoms: []string{}}
	a.ID = hash(a)
	return a
}

// Build requires a complete producer-owned artifact inventory and exact scope
// memberships. Capacity is reported without dropping a fragment or scope.
func Build(coverageFingerprint string, inputs []Input, scopeInputs []ScopeInput, options Options) (Plan, error) {
	p := Plan{Schema: Schema, CoverageFingerprint: coverageFingerprint, Artifacts: []Artifact{}, Atoms: []Atom{}, Scopes: []Scope{}, Limits: Limits{MaxFragmentInputTokens: options.MaxFragmentInputTokens, MaxFragments: options.MaxFragments}}
	if coverageFingerprint == "" || options.MaxFragmentInputTokens <= 0 || options.MaxFragments <= 0 {
		return p, fmt.Errorf("coverage identity and positive shared capacity required")
	}
	refs := map[Ref]bool{}
	ids := map[string]bool{}
	for _, input := range inputs {
		if input.Kind == "" || input.CanonicalRef.Collection == "" || input.CanonicalRef.Field == "" || input.CanonicalRef.Index < 0 || !utf8.ValidString(input.Content) || refs[input.CanonicalRef] {
			return p, fmt.Errorf("invalid/duplicate canonical artifact")
		}
		if _, err := compact(input.Metadata); err != nil {
			return p, fmt.Errorf("invalid artifact metadata: %w", err)
		}
		refs[input.CanonicalRef] = true
		a := artifactFor(input)
		if ids[a.ID] {
			return p, fmt.Errorf("duplicate artifact identity")
		}
		ids[a.ID] = true
		boundaries := []int{}
		for offset := range input.Content {
			boundaries = append(boundaries, offset)
		}
		boundaries = append(boundaries, len(input.Content))
		pos := 0
		for pos < len(boundaries)-1 || len(a.Atoms) == 0 {
			start := boundaries[pos]
			makeEnvelope := func(end int) ([]byte, error) {
				return compact(envelope{ArtifactID: a.ID, Kind: a.Kind, Offset: start, Content: input.Content[start:end], Metadata: a.Metadata})
			}
			empty, err := makeEnvelope(start)
			if err != nil {
				return p, err
			}
			if (len(empty)+3)/4 > options.MaxFragmentInputTokens {
				return p, fmt.Errorf("artifact metadata exceeds fragment budget")
			}
			best := pos
			lo, hi := pos+1, len(boundaries)-1
			for lo <= hi {
				mid := (lo + hi) / 2
				raw, err := makeEnvelope(boundaries[mid])
				if err != nil {
					return p, err
				}
				if (len(raw)+3)/4 <= options.MaxFragmentInputTokens {
					best = mid
					lo = mid + 1
				} else {
					hi = mid - 1
				}
			}
			if best == pos && start < len(input.Content) {
				return p, fmt.Errorf("artifact codepoint exceeds fragment budget")
			}
			end := boundaries[best]
			raw, err := makeEnvelope(end)
			if err != nil {
				return p, err
			}
			atom := Atom{ArtifactID: a.ID, Offset: start, Length: end - start, SHA256: digest([]byte(input.Content[start:end])), EstimatedInputTokens: (len(raw) + 3) / 4}
			atom.ID = hash(atom)
			p.Atoms = append(p.Atoms, atom)
			a.Atoms = append(a.Atoms, atom.ID)
			pos = best
			if end == len(input.Content) {
				break
			}
		}
		p.Artifacts = append(p.Artifacts, a)
	}
	atomCosts := map[string]int{}
	for _, atom := range p.Atoms {
		atomCosts[atom.ID] = atom.EstimatedInputTokens
	}
	scopeIDs := map[string]bool{}
	used := map[int]bool{}
	for _, input := range scopeInputs {
		if input.ID == "" || scopeIDs[input.ID] || len(input.Paths) == 0 || len(input.ArtifactIndices) == 0 {
			return p, fmt.Errorf("invalid/duplicate shared scope")
		}
		scopeIDs[input.ID] = true
		s := Scope{ID: input.ID, Paths: append([]string{}, input.Paths...), ArtifactIDs: []string{}, AtomIDs: []string{}, CrossPartAssessmentRequired: true}
		sort.Strings(s.Paths)
		for i, path := range s.Paths {
			if path == "" || (i > 0 && path == s.Paths[i-1]) {
				return p, fmt.Errorf("invalid scope paths")
			}
		}
		indices := append([]int{}, input.ArtifactIndices...)
		sort.Ints(indices)
		for i, index := range indices {
			if index < 0 || index >= len(p.Artifacts) || (i > 0 && index == indices[i-1]) {
				return p, fmt.Errorf("foreign/duplicate scope artifact")
			}
			used[index] = true
			a := p.Artifacts[index]
			s.ArtifactIDs = append(s.ArtifactIDs, a.ID)
			s.AtomIDs = append(s.AtomIDs, a.Atoms...)
			for _, atomID := range a.Atoms {
				s.EstimatedMaterializedTokens += atomCosts[atomID]
			}
			s.ContextBytes += a.ByteLength
		}
		p.Scopes = append(p.Scopes, s)
	}
	if len(used) != len(p.Artifacts) {
		return p, fmt.Errorf("orphan canonical artifact")
	}
	sort.Slice(p.Scopes, func(i, j int) bool { return p.Scopes[i].ID < p.Scopes[j].ID })
	p.Limits.FragmentLimitExceeded = len(p.Atoms) > options.MaxFragments
	p.Fingerprint = hash(p)
	return p, nil
}

// Materialize counts the actual canonical bytes and metadata, not pointer size.
// Callers still pin the expected producer fingerprint independently. Returning
// this envelope proves fragment integrity, never completion of a rule review.
func Materialize(plan Plan, atomID string, inputs []Input, expectedTrustedFingerprint string) ([]byte, error) {
	if plan.Schema != Schema {
		return nil, fmt.Errorf("unsupported shared plan schema")
	}
	original := plan.Fingerprint
	plan.Fingerprint = ""
	if expectedTrustedFingerprint == "" || original != expectedTrustedFingerprint || hash(plan) != original {
		return nil, fmt.Errorf("shared plan hash mismatch")
	}
	if plan.Limits.FragmentLimitExceeded || plan.Limits.MaxFragments <= 0 || len(plan.Atoms) > plan.Limits.MaxFragments || plan.Limits.MaxFragmentInputTokens <= 0 {
		return nil, fmt.Errorf("shared plan exceeds fragment admission capacity")
	}
	if len(inputs) != len(plan.Artifacts) {
		return nil, fmt.Errorf("canonical inventory mismatch")
	}
	var found *Atom
	for i := range plan.Atoms {
		if plan.Atoms[i].ID == atomID {
			if found != nil {
				return nil, fmt.Errorf("duplicate fragment")
			}
			found = &plan.Atoms[i]
		}
	}
	if found == nil {
		return nil, fmt.Errorf("unknown fragment")
	}
	for index, a := range plan.Artifacts {
		if a.ID != found.ArtifactID {
			continue
		}
		input := inputs[index]
		expected := artifactFor(input)
		if expected.ID != a.ID || expected.RepresentationSHA256 != a.RepresentationSHA256 || a.ByteLength != len(input.Content) {
			return nil, fmt.Errorf("canonical artifact changed")
		}
		start, end := found.Offset, found.Offset+found.Length
		if start < 0 || found.Length < 0 || end > len(input.Content) {
			return nil, fmt.Errorf("fragment range invalid")
		}
		piece := input.Content[start:end]
		if !utf8.ValidString(piece) || digest([]byte(piece)) != found.SHA256 {
			return nil, fmt.Errorf("fragment bytes changed")
		}
		raw, err := compact(envelope{ArtifactID: a.ID, Kind: a.Kind, Offset: start, Content: piece, Metadata: a.Metadata})
		if err != nil {
			return nil, err
		}
		if (len(raw)+3)/4 != found.EstimatedInputTokens || found.EstimatedInputTokens > plan.Limits.MaxFragmentInputTokens {
			return nil, fmt.Errorf("fragment budget mismatch")
		}
		return raw, nil
	}
	return nil, fmt.Errorf("orphan fragment")
}
