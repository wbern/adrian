// Package reviewpolicy parses routing policy independently of advisory linting.
// Invalid policy is an error, never a missing review obligation.
package reviewpolicy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	adrmeta "github.com/wbern/adrian/go/internal/adr"
	"gopkg.in/yaml.v3"
)

const RegistryPath = ".adrian/review-capabilities.yml"

type Capability struct {
	Evidence []string `yaml:"evidence" json:"evidence"`
}

type Registry struct {
	Version      int                   `yaml:"version"`
	Evidence     []string              `yaml:"evidence,omitempty"`
	Capabilities map[string]Capability `yaml:"capabilities"`
}

type Review struct {
	Requires []string `yaml:"requires"`
	Evidence []string `yaml:"evidence,omitempty"`
}

type Scope struct {
	Paths  []string `yaml:"paths"`
	Review Review   `yaml:"review"`
	// ReviewDeclared distinguishes deliberate classification (including an
	// empty requires list) from a legacy ADR that supplies context alone.
	ReviewDeclared bool `yaml:"-"`
}

type ADR struct {
	ID           string
	File         string
	Status       string
	SupersededBy []string
	Scopes       []Scope
	// PolicyDigest binds evidence to the complete committed ADR blob, including
	// normative prose. Compute it before normalizing line endings for parsing.
	PolicyDigest string
}

var symbol = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
var adrFile = regexp.MustCompile(`^([0-9]{4})-.+\.md$`)

// IsADRPath identifies the same numbered records used by the existing linter.
func IsADRPath(p string) bool { return path.Dir(p) == "doc/adr" && adrFile.MatchString(path.Base(p)) }

func ParseRegistry(raw []byte) (Registry, error) {
	var r Registry
	n, err := document(raw)
	if err != nil {
		return r, err
	}
	if err = fields(n, "version", "capabilities", "evidence"); err != nil {
		return r, err
	}
	if err = n.Decode(&r); err != nil {
		return r, err
	}
	if e := value(n, "evidence"); e != nil {
		if err = stringsNode(e); err != nil {
			return r, err
		}
	}
	if r.Version != 1 {
		return r, fmt.Errorf("unsupported review registry version %d", r.Version)
	}
	if len(r.Capabilities) == 0 {
		return r, fmt.Errorf("registry must declare at least one capability")
	}
	if err = names("evidence", r.Evidence); err != nil {
		return r, err
	}
	for name, capability := range r.Capabilities {
		if !symbol.MatchString(name) {
			return r, fmt.Errorf("invalid capability name %q", name)
		}
		if err = names("evidence", capability.Evidence); err != nil {
			return r, err
		}
	}
	caps := value(n, "capabilities")
	if caps == nil || caps.Kind != yaml.MappingNode {
		return r, fmt.Errorf("capabilities must be a mapping")
	}
	for i := 1; i < len(caps.Content); i += 2 {
		if err = fields(caps.Content[i], "evidence"); err != nil {
			return r, err
		}
		if e := value(caps.Content[i], "evidence"); e != nil {
			if err = stringsNode(e); err != nil {
				return r, err
			}
		}
	}
	return r, nil
}

func ParseADR(file string, raw []byte, r Registry) (ADR, error) {
	a := ADR{File: file}
	sum := sha256.Sum256(raw)
	a.PolicyDigest = hex.EncodeToString(sum[:])
	m := adrFile.FindStringSubmatch(path.Base(file))
	if m == nil {
		return a, fmt.Errorf("invalid ADR filename %q", file)
	}
	a.ID = m[1]
	raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(raw, []byte("---\n")) {
		return a, fmt.Errorf("%s: YAML frontmatter is required", file)
	}
	end := bytes.Index(raw[4:], []byte("\n---\n"))
	if end < 0 {
		return a, fmt.Errorf("%s: unterminated YAML frontmatter", file)
	}
	n, err := document(raw[4 : 4+end])
	if err != nil {
		return a, fmt.Errorf("%s: %w", file, err)
	}
	// Existing descriptive fields are accepted but do not create obligations.
	if err = fields(n, "status", "date", "applies_to", "review", "tags", "complexity", "pre_filter", "enforced_by", "diff_context", "superseded_by", "amended", "canonical_layer", "preferred_over", "superseded_date", "supersedes_title", "use_when"); err != nil {
		return a, fmt.Errorf("%s: %w", file, err)
	}
	status := value(n, "status")
	if status == nil || status.Tag != "!!str" {
		return a, fmt.Errorf("%s: explicit status is required", file)
	}
	a.Status = status.Value
	switch a.Status {
	case "accepted", "proposed", "deprecated", "superseded", "rejected", "withdrawn":
	default:
		return a, fmt.Errorf("%s: invalid status %q", file, a.Status)
	}
	if successor := value(n, "superseded_by"); successor != nil {
		var decodeErr error
		a.SupersededBy, decodeErr = adrmeta.DecodeSuccessorIDs(successor)
		if decodeErr != nil {
			return a, fmt.Errorf("%s: %w", file, decodeErr)
		}
	}
	applies := value(n, "applies_to")
	if applies == nil || applies.Kind != yaml.SequenceNode || (len(applies.Content) == 0 && (a.Status == "accepted" || a.Status == "proposed")) {
		return a, fmt.Errorf("%s: nonempty applies_to is required", file)
	}
	var topReview Review
	top := value(n, "review")
	if top != nil {
		topReview, err = parseReview(top, r)
		if err != nil {
			return a, fmt.Errorf("%s: %w", file, err)
		}
	}
	var flat []string
	for _, item := range applies.Content {
		if item.Kind == yaml.ScalarNode && item.Tag == "!!str" {
			flat = append(flat, item.Value)
			continue
		}
		if top != nil {
			return a, fmt.Errorf("%s: top-level review cannot accompany typed applies_to scopes", file)
		}
		if err = fields(item, "paths", "review"); err != nil {
			return a, fmt.Errorf("%s: scope: %w", file, err)
		}
		var s Scope
		paths := value(item, "paths")
		if paths == nil || paths.Kind != yaml.SequenceNode {
			return a, fmt.Errorf("%s: scope paths must be a sequence", file)
		}
		if err = stringsNode(paths); err != nil {
			return a, fmt.Errorf("%s: %w", file, err)
		}
		if err = paths.Decode(&s.Paths); err != nil {
			return a, err
		}
		if review := value(item, "review"); review != nil {
			s.ReviewDeclared = true
			s.Review, err = parseReview(review, r)
			if err != nil {
				return a, fmt.Errorf("%s: %w", file, err)
			}
		}
		if err = validatePaths(s.Paths); err != nil {
			return a, fmt.Errorf("%s: %w", file, err)
		}
		a.Scopes = append(a.Scopes, s)
	}
	if len(flat) > 0 {
		if err = validatePaths(flat); err != nil {
			return a, fmt.Errorf("%s: %w", file, err)
		}
		a.Scopes = append([]Scope{{Paths: flat, Review: topReview, ReviewDeclared: top != nil}}, a.Scopes...)
	}
	return a, nil
}

// ValidateSet verifies cross-record integrity after all records have parsed.
func ValidateSet(policies []ADR) error {
	byID := map[string]ADR{}
	for _, a := range policies {
		if _, ok := byID[a.ID]; ok {
			return fmt.Errorf("duplicate ADR ID %s", a.ID)
		}
		byID[a.ID] = a
	}
	for _, a := range policies {
		if a.Status == "superseded" && len(a.SupersededBy) == 0 {
			return fmt.Errorf("ADR %s is superseded without a successor", a.ID)
		}
		for _, id := range a.SupersededBy {
			if _, ok := byID[id]; !ok {
				return fmt.Errorf("ADR %s has missing successor %s", a.ID, id)
			}
		}
	}
	successors := make(map[string][]string, len(byID))
	for _, a := range policies {
		successors[a.ID] = a.SupersededBy
	}
	return adrmeta.ValidateSuccessorCycles(successors)
}

func parseReview(n *yaml.Node, r Registry) (Review, error) {
	var review Review
	if err := fields(n, "requires", "evidence"); err != nil {
		return review, err
	}
	if value(n, "requires") == nil {
		return review, fmt.Errorf("review.requires is required; use [] for deliberate classification without a specialist")
	}
	if err := n.Decode(&review); err != nil {
		return review, err
	}
	for _, key := range []string{"requires", "evidence"} {
		if node := value(n, key); node != nil {
			if err := stringsNode(node); err != nil {
				return review, fmt.Errorf("%s: %w", key, err)
			}
		}
	}
	if err := names("requires", review.Requires); err != nil {
		return review, err
	}
	if err := names("evidence", review.Evidence); err != nil {
		return review, err
	}
	known := map[string]bool{}
	for _, e := range r.Evidence {
		known[e] = true
	}
	for _, c := range r.Capabilities {
		for _, e := range c.Evidence {
			known[e] = true
		}
	}
	for _, capability := range review.Requires {
		if _, ok := r.Capabilities[capability]; !ok {
			return review, fmt.Errorf("unknown capability %q", capability)
		}
	}
	for _, e := range review.Evidence {
		if !known[e] {
			return review, fmt.Errorf("unknown evidence %q", e)
		}
	}
	if len(review.Evidence) > 0 && len(review.Requires) == 0 {
		return review, fmt.Errorf("review evidence requires at least one capability")
	}
	return review, nil
}

func names(field string, values []string) error {
	seen := map[string]bool{}
	for _, s := range values {
		if !symbol.MatchString(s) {
			return fmt.Errorf("invalid %s name %q", field, s)
		}
		if seen[s] {
			return fmt.Errorf("duplicate %s %q", field, s)
		}
		seen[s] = true
	}
	return nil
}

func stringsNode(n *yaml.Node) error {
	if n.Kind != yaml.SequenceNode {
		return fmt.Errorf("expected a sequence of strings")
	}
	for _, item := range n.Content {
		if item.Kind != yaml.ScalarNode || item.Tag != "!!str" {
			return fmt.Errorf("expected a string in sequence")
		}
	}
	return nil
}

func validatePaths(patterns []string) error {
	if len(patterns) == 0 {
		return fmt.Errorf("scope paths must not be empty")
	}
	positive := false
	for _, p := range patterns {
		negative := strings.HasPrefix(p, "!")
		p = strings.TrimPrefix(p, "!")
		if !negative {
			positive = true
		}
		if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\x00") {
			return fmt.Errorf("invalid repository-relative glob %q", p)
		}
		for _, component := range strings.Split(p, "/") {
			if component == ".." || component == "." {
				return fmt.Errorf("invalid repository-relative glob %q", p)
			}
		}
		if !doublestar.ValidatePattern(p) {
			return fmt.Errorf("invalid glob %q", p)
		}
	}
	if !positive {
		return fmt.Errorf("scope needs a positive path pattern")
	}
	return nil
}

func (s Scope) Matches(file string) bool {
	matched := false
	for _, pattern := range s.Paths {
		negated := strings.HasPrefix(pattern, "!")
		ok, _ := doublestar.Match(strings.TrimPrefix(pattern, "!"), file)
		if ok && negated {
			return false
		}
		if ok {
			matched = true
		}
	}
	return matched
}

func document(raw []byte) (*yaml.Node, error) {
	var doc yaml.Node
	d := yaml.NewDecoder(bytes.NewReader(raw))
	if err := d.Decode(&doc); err != nil {
		return nil, err
	}
	var extra yaml.Node
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("multiple YAML documents are not allowed")
	}
	if len(doc.Content) != 1 {
		return nil, fmt.Errorf("expected YAML mapping")
	}
	if err := validateNode(doc.Content[0]); err != nil {
		return nil, err
	}
	return doc.Content[0], nil
}

func validateNode(n *yaml.Node) error {
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return fmt.Errorf("YAML aliases and anchors are not allowed in policy")
	}
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Tag != "!!str" {
				return fmt.Errorf("policy keys must be strings")
			}
			if seen[k.Value] {
				return fmt.Errorf("duplicate field %q", k.Value)
			}
			seen[k.Value] = true
		}
	}
	for _, child := range n.Content {
		if err := validateNode(child); err != nil {
			return err
		}
	}
	return nil
}

func fields(n *yaml.Node, allowed ...string) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping")
	}
	set := map[string]bool{}
	for _, s := range allowed {
		set[s] = true
	}
	for i := 0; i < len(n.Content); i += 2 {
		if !set[n.Content[i].Value] {
			return fmt.Errorf("unknown field %q", n.Content[i].Value)
		}
	}
	return nil
}

func value(n *yaml.Node, key string) *yaml.Node {
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}
