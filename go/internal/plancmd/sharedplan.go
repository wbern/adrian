package plancmd

import (
	"fmt"
	"sort"
	"unicode/utf8"

	"github.com/wbern/adrian/go/internal/reviewplan"
	"github.com/wbern/adrian/go/internal/sharedpacket"
)

type ChangeDiffRecord struct {
	ID     string            `json:"id"`
	Change reviewplan.Change `json:"change"`
	Paths  []string          `json:"paths"`
	Diff   string            `json:"diff"`
	SHA256 string            `json:"sha256"`
}

func (g gitReader) attachSharedDelivery(p *CodePlan) error {
	semantic := p.Coverage.Semantic
	inputs := []sharedpacket.Input{}
	sources := map[string][]int{}
	policies := map[string]int{}
	add := func(kind, content string, ref sharedpacket.Ref, meta map[string]any) int {
		index := len(inputs)
		inputs = append(inputs, sharedpacket.Input{Kind: kind, CanonicalRef: ref, Content: content, Encoding: "utf-8", Metadata: meta})
		return index
	}
	for index, policy := range p.Policies {
		policies[policy.Path] = add("policy", policy.Content, sharedpacket.Ref{Collection: "policies", Index: index, Field: "content"}, map[string]any{"path": policy.Path, "policy": semantic.PolicyCommit, "adr": policy.ADR, "raw_sha256": policy.SHA256})
	}
	baselineArtifacts := []int{}
	baselinePaths := []string{}
	seenPaths := map[string]bool{}
	for index, source := range p.Content {
		artifact := add("source", source.Content, sharedpacket.Ref{Collection: "content", Index: index, Field: "content"}, map[string]any{"path": source.Path, "side": source.Side, "commit": source.Commit, "kind": source.Kind, "stored_encoding": source.Encoding, "raw_sha256": source.SHA256, "object_id": source.ObjectID, "mode": source.Mode})
		sources[source.Path] = append(sources[source.Path], artifact)
		baselineArtifacts = append(baselineArtifacts, artifact)
		if !seenPaths[source.Path] {
			baselinePaths = append(baselinePaths, source.Path)
			seenPaths[source.Path] = true
		}
	}
	if len(baselinePaths) > 0 {
		artifact := add("global-diff", p.Diff, sharedpacket.Ref{Collection: "root", Field: "diff"}, map[string]any{"base": semantic.MergeBase, "head": semantic.HeadCommit})
		baselineArtifacts = append(baselineArtifacts, artifact)
	}
	type changeArtifact struct {
		paths    []string
		artifact int
	}
	changes := []changeArtifact{}
	p.ChangeDiffs = []ChangeDiffRecord{}
	for index, change := range semantic.Changes {
		paths := []string{change.Path}
		if change.OldPath != "" && change.OldPath != change.Path {
			paths = append(paths, change.OldPath)
		}
		sort.Strings(paths)
		args := []string{"diff", "--no-relative", "--no-color", "--no-ext-diff", "--no-textconv", "--ignore-submodules=none", "--binary", "--find-renames", semantic.MergeBase, semantic.HeadCommit, "--"}
		for _, path := range paths {
			args = append(args, ":(literal)"+path)
		}
		raw, err := g.runBounded(args...)
		if err != nil {
			return err
		}
		if !utf8.Valid(raw) {
			return fmt.Errorf("change diff is not UTF-8")
		}
		id := digest([]byte(fmt.Sprintf("%s:%s:%d:%s", semantic.MergeBase, semantic.HeadCommit, index, digest(raw))))
		p.ChangeDiffs = append(p.ChangeDiffs, ChangeDiffRecord{ID: id, Change: change, Paths: paths, Diff: string(raw), SHA256: digest(raw)})
		artifact := add("change-diff", string(raw), sharedpacket.Ref{Collection: "change_diffs", Index: index, Field: "diff"}, map[string]any{"change_id": id, "paths": paths, "base": semantic.MergeBase, "head": semantic.HeadCommit})
		changes = append(changes, changeArtifact{paths: paths, artifact: artifact})
	}
	scopes := []sharedpacket.ScopeInput{}
	if len(baselinePaths) > 0 {
		// Baseline always consumes every per-change artifact as well as the global
		// boundary view, including changes outside any accepted policy scope.
		for _, change := range changes {
			baselineArtifacts = append(baselineArtifacts, change.artifact)
		}
		scopes = append(scopes, sharedpacket.ScopeInput{ID: "baseline", Paths: baselinePaths, ArtifactIndices: baselineArtifacts})
	}
	for _, row := range p.Coverage.Applicability {
		policy, ok := policies[row.File]
		if !ok {
			return fmt.Errorf("shared scope lacks complete policy")
		}
		artifacts := []int{policy}
		paths := map[string]bool{}
		for _, path := range row.Paths {
			paths[path] = true
			artifacts = append(artifacts, sources[path]...)
		}
		for _, change := range changes {
			for _, path := range change.paths {
				if paths[path] {
					artifacts = append(artifacts, change.artifact)
					break
				}
			}
		}
		scopes = append(scopes, sharedpacket.ScopeInput{ID: row.ID, Paths: row.Paths, ArtifactIndices: artifacts})
	}
	native, err := sharedpacket.Build(p.Coverage.Fingerprint, inputs, scopes, sharedpacket.Options{MaxFragmentInputTokens: 4096, MaxFragments: 256})
	if err != nil {
		return err
	}
	p.SharedDelivery = &native
	return nil
}
