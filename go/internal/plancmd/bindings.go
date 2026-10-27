package plancmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wbern/adrian/go/internal/reviewplan"
)

const BindingsUsage = "adrian resolve --repository <owner/name|local:stable-id> --base <ref> --head <ref> --policy <trusted-base-ref> --paths0 --format bindings-json"
const BindingsSchema = "adrian-bindings-v1"
const maxBindingsScopeBytes = 4 * 1024 * 1024
const maxBindingsScopePaths = 65536

// BindingScope records matching paths within the caller's selected scope only.
type BindingScope struct {
	Scope string   `json:"scope"`
	Paths []string `json:"paths"`
}

// PolicyBinding carries the complete immutable policy, never an assessment.
type PolicyBinding struct {
	PolicyRecord
	Status  string         `json:"status"`
	Matches []BindingScope `json:"matches"`
}
type SelectedScope struct {
	Kind   string   `json:"kind"`
	Paths  []string `json:"paths"`
	Digest string   `json:"digest"`
}

// BindingsPlan identifies supplied paths separately from the complete final Git
// manifest. Intermediate fork-delta paths need not occur in that final manifest.
// No source coverage, execution evidence, or approval is asserted by this schema.
type BindingsPlan struct {
	Schema         string `json:"schema"`
	Repository     string `json:"repository"`
	RepositoryKind string `json:"repository_kind"`
	reviewplan.Provenance
	Changes        []reviewplan.Change         `json:"changes"`
	ManifestDigest string                      `json:"manifest_digest"`
	Scope          SelectedScope               `json:"scope"`
	PolicyManifest []reviewplan.PolicyIdentity `json:"policy_manifest"`
	RegistryDigest string                      `json:"registry_digest"`
	Accepted       []PolicyBinding             `json:"accepted"`
	Proposed       []PolicyBinding             `json:"proposed"`
	Fingerprint    string                      `json:"fingerprint"`
}

// IsBindingsMode routes immutable requests before environment-sensitive legacy
// repository discovery. Unknown combinations still reach the strict parser.
func IsBindingsMode(args []string) bool {
	for i, arg := range args {
		switch strings.SplitN(arg, "=", 2)[0] {
		case "--repository", "--head", "--policy", "--paths0":
			return true
		}
		if arg == "--format" && i+1 < len(args) && args[i+1] == "bindings-json" {
			return true
		}
		if arg == "--format=bindings-json" {
			return true
		}
	}
	return false
}

var repositoryIdentity = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$`)
var localRepositoryIdentity = regexp.MustCompile(`^local:[a-z0-9][a-z0-9._-]{0,127}$`)

func bindingRepositoryKind(identity string) string {
	if repositoryIdentity.MatchString(identity) {
		return "github"
	}
	if localRepositoryIdentity.MatchString(identity) {
		return "local"
	}
	return ""
}

func parseBindingsArgs(args []string) (map[string]string, error) {
	values := map[string]string{}
	for i := 0; i < len(args); i++ {
		key := args[i]
		if _, exists := values[key]; exists {
			return nil, fmt.Errorf("%s must be specified once", key)
		}
		switch key {
		case "--paths0":
			values[key] = "true"
		case "--repository", "--base", "--head", "--policy", "--format":
			i++
			if i >= len(args) || args[i] == "" || strings.HasPrefix(args[i], "-") {
				return nil, fmt.Errorf("%s requires a value", key)
			}
			values[key] = args[i]
		default:
			return nil, fmt.Errorf("unsupported immutable binding argument %q; usage: %s", key, BindingsUsage)
		}
	}
	for _, key := range []string{"--repository", "--base", "--head", "--policy", "--paths0", "--format"} {
		if values[key] == "" {
			return nil, fmt.Errorf("%s is required; usage: %s", key, BindingsUsage)
		}
	}
	if values["--format"] != "bindings-json" {
		return nil, fmt.Errorf("immutable bindings require --format bindings-json")
	}
	if bindingRepositoryKind(values["--repository"]) == "" {
		return nil, fmt.Errorf("invalid repository identity; expected owner/name or local:<lowercase-stable-id> (1-128 characters)")
	}
	return values, nil
}
func readBindingScope(in io.Reader) ([]string, error) {
	raw, err := io.ReadAll(io.LimitReader(in, maxBindingsScopeBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read selected scope: %w", err)
	}
	if len(raw) > maxBindingsScopeBytes {
		return nil, fmt.Errorf("selected scope exceeds byte budget")
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("selected scope is missing or empty; no reviewable-path proof was supplied")
	}
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("selected scope must be valid UTF-8")
	}
	if raw[len(raw)-1] != 0 {
		return nil, fmt.Errorf("selected scope must be NUL terminated")
	}
	parts := bytes.Split(raw[:len(raw)-1], []byte{0})
	if len(parts) > maxBindingsScopePaths {
		return nil, fmt.Errorf("selected scope exceeds path budget")
	}
	seen := map[string]bool{}
	for _, part := range parts {
		p := string(part)
		if p == "" || !utf8.ValidString(p) || path.IsAbs(p) || strings.Contains(p, "\\") {
			return nil, fmt.Errorf("selected scope contains a noncanonical relative path %q", p)
		}
		for _, component := range strings.Split(p, "/") {
			if component == "" || component == "." || component == ".." {
				return nil, fmt.Errorf("selected scope contains a noncanonical relative path %q", p)
			}
		}
		seen[p] = true
	}
	paths := make([]string, 0, len(seen))
	for p := range seen {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths, nil
}
func bindingsDigest(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return digest(raw), nil
}

func RunBindingsCLI(args []string, dir string, in io.Reader, out io.Writer) error {
	opts, err := parseBindingsArgs(args)
	if err != nil {
		return err
	}
	paths, err := readBindingScope(in)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	g := gitReader{dir: dir, ctx: ctx}
	root, err := g.run("rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	g.dir = strings.TrimSpace(string(root))
	base, err := g.commit(opts["--base"])
	if err != nil {
		return err
	}
	head, err := g.commit(opts["--head"])
	if err != nil {
		return err
	}
	policy, err := g.commit(opts["--policy"])
	if err != nil {
		return err
	}
	if _, err = g.run("merge-base", "--is-ancestor", policy, base); err != nil {
		return fmt.Errorf("policy must be from the trusted base history: %w", err)
	}
	mergeRaw, err := g.run("merge-base", base, head)
	if err != nil {
		return err
	}
	mergeBase := strings.TrimSpace(string(mergeRaw))
	if !commitID.MatchString(mergeBase) {
		return fmt.Errorf("invalid merge base returned by Git")
	}
	manifest, err := g.run("diff", "--no-relative", "--no-color", "--no-ext-diff", "--no-textconv", "--ignore-submodules=none", "--name-status", "-z", "--find-renames", mergeBase, head, "--")
	if err != nil {
		return err
	}
	changes, err := parseChanges(manifest)
	if err != nil {
		return err
	}
	reviewplan.SortChanges(changes)
	bodies := map[string][]byte{}
	registry, policies, err := g.readPolicyWithBodies(policy, bodies)
	if err != nil {
		return err
	}
	p := BindingsPlan{Schema: BindingsSchema, Repository: opts["--repository"], RepositoryKind: bindingRepositoryKind(opts["--repository"]), Provenance: reviewplan.Provenance{BaseCommit: base, MergeBase: mergeBase, HeadCommit: head, PolicyCommit: policy}, Changes: changes, Scope: SelectedScope{Kind: "caller-selected-paths", Paths: paths}, PolicyManifest: []reviewplan.PolicyIdentity{}, Accepted: []PolicyBinding{}, Proposed: []PolicyBinding{}}
	p.Scope.Digest, err = bindingsDigest(paths)
	if err != nil {
		return err
	}
	p.ManifestDigest, err = bindingsDigest(changes)
	if err != nil {
		return err
	}
	p.RegistryDigest, err = bindingsDigest(registry)
	if err != nil {
		return err
	}
	for _, a := range policies {
		p.PolicyManifest = append(p.PolicyManifest, reviewplan.PolicyIdentity{ADR: a.ID, File: a.File, Status: a.Status, Digest: a.PolicyDigest})
		if a.Status != "accepted" && a.Status != "proposed" {
			continue
		}
		binding := PolicyBinding{PolicyRecord: PolicyRecord{ADR: a.ID, Path: a.File, Content: string(bodies[a.File]), SHA256: a.PolicyDigest}, Status: a.Status, Matches: []BindingScope{}}
		for index, scope := range a.Scopes {
			matched := []string{}
			for _, file := range paths {
				if scope.Matches(file) {
					matched = append(matched, file)
				}
			}
			if len(matched) > 0 {
				binding.Matches = append(binding.Matches, BindingScope{Scope: fmt.Sprintf("%d", index+1), Paths: matched})
			}
		}
		if len(binding.Matches) == 0 {
			continue
		}
		if a.Status == "accepted" {
			p.Accepted = append(p.Accepted, binding)
		} else {
			p.Proposed = append(p.Proposed, binding)
		}
	}
	sort.Slice(p.PolicyManifest, func(i, j int) bool { return p.PolicyManifest[i].File < p.PolicyManifest[j].File })
	p.Fingerprint, err = bindingsDigest(p)
	if err != nil {
		return err
	}
	payload, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if int64(len(payload))+1 > g.maxPlanBytes() {
		return fmt.Errorf("encoded bindings exceed byte budget")
	}
	_, err = out.Write(append(payload, '\n'))
	return err
}
