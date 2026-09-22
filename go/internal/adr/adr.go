// Package adr parses Architecture Decision Records (ADRs) from markdown
// files with optional YAML frontmatter.
package adr

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Status is the lifecycle state of an ADR.
type Status string

const (
	StatusAccepted   Status = "accepted"
	StatusProposed   Status = "proposed"
	StatusDeprecated Status = "deprecated"
	StatusSuperseded Status = "superseded"
	StatusRejected   Status = "rejected"
	StatusWithdrawn  Status = "withdrawn"
)

// Complexity selects the model tier used to lint against this ADR.
type Complexity string

const (
	ComplexityUltralite Complexity = "ultralite"
	ComplexityLite      Complexity = "lite"
	ComplexityStandard  Complexity = "standard"
	ComplexityComplex   Complexity = "complex"
)

// DirName is the path (relative to the repo root) where adr-lint reads
// and writes ADR files.
const DirName = "doc/adr"

// ADR is the parsed representation of a single ADR file. PreFilter is
// always a slice; a single-string YAML value is normalized to a
// one-element slice so the "any pattern matches" semantics hold.
// EnforcedBy is *string to distinguish "missing" from "empty".
type ADR struct {
	ID        string
	Title     string
	Status    Status
	AppliesTo []string
	// AppliesToScopes keeps each typed scope's exclusions local to that scope.
	AppliesToScopes [][]string
	Complexity      Complexity
	Decision        string
	FilePath        string
	Content         string
	PreFilter       []string
	EnforcedBy      *string
	DiffContext     bool
	SupersededBy    []string
}

// frontmatter is the raw shape of the YAML block; field types accept the
// union shapes the original parser handled.
type frontmatter struct {
	Status       string            `yaml:"status"`
	Date         string            `yaml:"date"`
	AppliesTo    appliesToPatterns `yaml:"applies_to"`
	Complexity   string            `yaml:"complexity"`
	PreFilter    interface{}       `yaml:"pre_filter"`
	EnforcedBy   string            `yaml:"enforced_by"`
	DiffContext  *bool             `yaml:"diff_context"`
	SupersededBy successorIDs      `yaml:"superseded_by"`
}

// successorIDs accepts the historical scalar form and the sequence form used
// when an ADR is replaced by more than one decision. Both shapes normalize to
// four-digit ADR IDs so downstream graph checks have one representation.
type successorIDs []string

func (s *successorIDs) UnmarshalYAML(n *yaml.Node) error {
	ids, err := DecodeSuccessorIDs(n)
	if err != nil {
		return err
	}
	*s = ids
	return nil
}

// DecodeSuccessorIDs normalizes a scalar or sequence-valued superseded_by
// node. It is shared by generic ADR parsing and strict review-policy parsing.
func DecodeSuccessorIDs(n *yaml.Node) ([]string, error) {
	nodes := n.Content
	if n.Kind == yaml.ScalarNode {
		nodes = []*yaml.Node{n}
	} else if n.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("superseded_by must be an ADR ID or sequence of ADR IDs")
	}
	ids := make([]string, 0, len(nodes))
	seen := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		if node.Kind != yaml.ScalarNode || (node.Tag != "!!str" && node.Tag != "!!int") {
			return nil, fmt.Errorf("superseded_by entries must be ADR IDs")
		}
		id, err := strconv.Atoi(strings.TrimSpace(node.Value))
		if err != nil || id < 0 || id > 9999 {
			return nil, fmt.Errorf("invalid superseded_by successor %q", node.Value)
		}
		normalized := fmt.Sprintf("%04d", id)
		if seen[normalized] {
			return nil, fmt.Errorf("duplicate superseded_by successor %q", normalized)
		}
		seen[normalized] = true
		ids = append(ids, normalized)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("superseded_by must not be empty")
	}
	return ids, nil
}

// ValidateSuccessorCycles rejects self-cycles and longer cycles in an ADR
// successor graph. Reference existence is intentionally checked by callers so
// they can retain their command-specific diagnostics.
func ValidateSuccessorCycles(successors map[string][]string) error {
	visiting, done := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return fmt.Errorf("ADR successor cycle at %s", id)
		}
		if done[id] {
			return nil
		}
		visiting[id] = true
		for _, next := range successors[id] {
			if err := visit(next); err != nil {
				return err
			}
		}
		visiting[id] = false
		done[id] = true
		return nil
	}
	for id := range successors {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

// appliesToPatterns accepts both legacy glob lists and ADRian typed scopes.
// Review metadata is interpreted by reviewpolicy, not by advisory linting.
type appliesToPatterns struct {
	Patterns []string
	Scopes   [][]string
}

func (a *appliesToPatterns) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.SequenceNode {
		return fmt.Errorf("applies_to must be a sequence")
	}
	for _, entry := range n.Content {
		if entry.Kind == yaml.ScalarNode && entry.Tag == "!!str" {
			a.Patterns = append(a.Patterns, entry.Value)
			continue
		}
		var scope struct {
			Paths []string `yaml:"paths"`
		}
		if err := entry.Decode(&scope); err != nil {
			return err
		}
		if len(scope.Paths) == 0 {
			return fmt.Errorf("typed applies_to scope needs paths")
		}
		a.Scopes = append(a.Scopes, scope.Paths)
	}
	return nil
}

// titleRe matches the H1 header that carries the ADR's number and title,
// e.g. "# 12. Use Const Assertions".
var titleRe = regexp.MustCompile(`(?m)^# (\d+)\. (.+)$`)

// frontmatterRe matches a leading YAML frontmatter block delimited by "---".
var frontmatterRe = regexp.MustCompile(`(?s)\A---\n(.*?)\n---\n`)

// adrFileRe filters parseADRs to numbered ADR files (0001-foo.md).
var adrFileRe = regexp.MustCompile(`^\d{4}-.*\.md$`)

// LoadADRs reads every numbered ADR file in dir (0001-*.md ... 9999-*.md)
// and returns one ADR per file, with no filtering. Suitable for management
// commands like list/show/deprecate/supersede where deprecated, superseded,
// or scaffolded-but-empty ADRs must still be visible.
func LoadADRs(dir string) ([]ADR, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read adr dir %q: %w", dir, err)
	}

	var paths []string
	for _, e := range entries {
		if e.IsDir() || !adrFileRe.MatchString(e.Name()) {
			continue
		}
		paths = append(paths, filepath.Join(dir, e.Name()))
	}
	sort.Strings(paths)

	out := make([]ADR, 0, len(paths))
	for _, p := range paths {
		content, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read adr %q: %w", p, err)
		}
		out = append(out, ParseADR(string(content), p))
	}
	return out, nil
}

// ParseADRs reads every numbered ADR file in dir (0001-*.md ... 9999-*.md),
// parses each, and returns those eligible for AI linting:
// non-empty Decision section, status not deprecated/superseded, and no
// `enforced_by` set (those are handled by other tooling).
func ParseADRs(dir string) ([]ADR, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read adr dir %q: %w", dir, err)
	}

	var paths []string
	for _, e := range entries {
		if e.IsDir() || !adrFileRe.MatchString(e.Name()) {
			continue
		}
		paths = append(paths, filepath.Join(dir, e.Name()))
	}
	sort.Strings(paths)

	out := make([]ADR, 0, len(paths))
	for _, p := range paths {
		content, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read adr %q: %w", p, err)
		}
		adr := ParseADR(string(content), p)
		if strings.TrimSpace(adr.Decision) == "" {
			continue
		}
		if adr.Status == StatusDeprecated || adr.Status == StatusSuperseded ||
			adr.Status == StatusRejected || adr.Status == StatusWithdrawn {
			continue
		}
		if adr.EnforcedBy != nil {
			continue
		}
		out = append(out, adr)
	}
	return out, nil
}

// ParseADR parses a single ADR's raw markdown content. filePath is recorded
// on the ADR but not read; pass the path the content came from.
func ParseADR(content, filePath string) ADR {
	fm, body := extractFrontmatter(content)

	id, title := parseTitle(body, filePath)

	status := parseStatusValue(strOr(fm, func(f *frontmatter) string { return f.Status }))

	var appliesTo []string
	var scopes [][]string
	if fm != nil && (len(fm.AppliesTo.Patterns) > 0 || len(fm.AppliesTo.Scopes) > 0) {
		appliesTo = fm.AppliesTo.Patterns
		if len(fm.AppliesTo.Scopes) > 0 {
			scopes = append(scopes, fm.AppliesTo.Scopes...)
			if len(appliesTo) > 0 {
				scopes = append([][]string{appliesTo}, scopes...)
			}
		}
	} else {
		appliesTo = parseAppliesToFromSection(body)
	}

	complexity := parseComplexityValue(strOr(fm, func(f *frontmatter) string { return f.Complexity }))
	if complexity == "" {
		complexity = parseComplexityFromSection(body)
	}

	decision := ExtractSection(body, "Decision")

	preFilter := normalizePreFilter(fm)

	var enforcedBy *string
	if fm != nil && fm.EnforcedBy != "" {
		v := fm.EnforcedBy
		enforcedBy = &v
	}

	diffContext := true
	if fm != nil && fm.DiffContext != nil {
		diffContext = *fm.DiffContext
	}

	var supersededBy []string
	if fm != nil {
		supersededBy = append(supersededBy, fm.SupersededBy...)
	}

	return ADR{
		ID:              id,
		Title:           title,
		Status:          status,
		AppliesTo:       appliesTo,
		AppliesToScopes: scopes,
		Complexity:      complexity,
		Decision:        decision,
		FilePath:        filePath,
		Content:         body,
		PreFilter:       preFilter,
		EnforcedBy:      enforcedBy,
		DiffContext:     diffContext,
		SupersededBy:    supersededBy,
	}
}

func parseTitle(body, filePath string) (id, title string) {
	if m := titleRe.FindStringSubmatch(body); m != nil {
		return m[1], m[2]
	}
	base := filepath.Base(filePath)
	base = strings.TrimSuffix(base, ".md")
	if idx := strings.Index(base, "-"); idx >= 0 {
		return base[:idx], "Unknown"
	}
	return base, "Unknown"
}

func parseStatusValue(v string) Status {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "accepted", "proposed", "deprecated", "superseded", "rejected", "withdrawn":
		return Status(strings.ToLower(strings.TrimSpace(v)))
	}
	return StatusAccepted
}

func parseComplexityValue(v string) Complexity {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "ultralite", "lite", "standard", "complex":
		return Complexity(strings.ToLower(strings.TrimSpace(v)))
	}
	return ""
}

func parseAppliesToFromSection(body string) []string {
	section := ExtractSection(body, "Applies To")
	if section == "" {
		return []string{"**/*"}
	}
	var out []string
	for _, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "- ") {
			continue
		}
		pat := strings.TrimSpace(trimmed[strings.Index(trimmed, "-")+1:])
		pat = strings.TrimPrefix(pat, "`")
		pat = strings.TrimSuffix(pat, "`")
		if pat != "" {
			out = append(out, pat)
		}
	}
	return out
}

func parseComplexityFromSection(body string) Complexity {
	section := ExtractSection(body, "Complexity")
	if section == "" {
		return ComplexityStandard
	}
	if c := parseComplexityValue(section); c != "" {
		return c
	}
	return ComplexityStandard
}

// ExtractSection returns the trimmed body of the `## <name>` section.
// Lines are collected until the next `## ` header is encountered.
// Returns "" if the section is absent.
func ExtractSection(content, name string) string {
	lines := strings.Split(content, "\n")
	header := "## " + name
	headerIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == header {
			headerIdx = i
			break
		}
	}
	if headerIdx == -1 {
		return ""
	}
	var collected []string
	for i := headerIdx + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			break
		}
		collected = append(collected, lines[i])
	}
	return strings.TrimSpace(strings.Join(collected, "\n"))
}

// ValidateFrontmatter returns the YAML parse error for content's leading
// frontmatter block, or nil if the block is absent or parses cleanly.
// extractFrontmatter is intentionally tolerant of bad YAML (silently falls
// back to defaults) so management commands keep working on malformed
// files; ValidateFrontmatter exposes the underlying error for callers
// that want to surface it.
func ValidateFrontmatter(content string) error {
	m := frontmatterRe.FindStringSubmatchIndex(content)
	if m == nil {
		return nil
	}
	var fm frontmatter
	return yaml.Unmarshal([]byte(content[m[2]:m[3]]), &fm)
}

func extractFrontmatter(content string) (*frontmatter, string) {
	m := frontmatterRe.FindStringSubmatchIndex(content)
	if m == nil {
		return nil, content
	}
	yamlBlock := content[m[2]:m[3]]
	body := content[m[1]:]
	var fm frontmatter
	if err := yaml.Unmarshal([]byte(yamlBlock), &fm); err != nil {
		return nil, content
	}
	return &fm, body
}

// strOr returns the result of f(fm) when fm is non-nil, otherwise "".
// Lets parseStatusValue / parseComplexityValue stay nil-safe without
// repeated guards at every call site.
func strOr(fm *frontmatter, f func(*frontmatter) string) string {
	if fm == nil {
		return ""
	}
	return f(fm)
}

// normalizePreFilter folds the `string | []string` YAML union into a slice.
// Returns nil if the field is absent or empty so callers can distinguish
// "unset" from "set to empty list" via len().
func normalizePreFilter(fm *frontmatter) []string {
	if fm == nil || fm.PreFilter == nil {
		return nil
	}
	switch v := fm.PreFilter.(type) {
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	return nil
}

// Create writes a new ADR markdown file under dir using the given title.
// Returns the full path of the created file. Concurrent callers race-free
// each other on number allocation: we open with O_EXCL and bump on
// collision so two parallel `adr-lint create` invocations get distinct
// numbers.
func Create(dir, title string) (string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create adr dir %q: %w", dir, err)
	}
	slug := slugify(title)
	base := nextADRNumber(dir)
	for attempt := 0; attempt < 1024; attempt++ {
		n := base + attempt
		path := filepath.Join(dir, fmt.Sprintf("%04d-%s.md", n, slug))
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			return "", err
		}
		body := renderTemplate(dir, n, title)
		if _, werr := f.Write([]byte(body)); werr != nil {
			f.Close()
			return "", werr
		}
		if cerr := f.Close(); cerr != nil {
			return "", cerr
		}
		return path, nil
	}
	return "", fmt.Errorf("could not allocate an ADR number under %q after 1024 attempts", dir)
}

const defaultTemplate = `---
status: proposed
applies_to:
  - "**/*"
---

# {{number}}. {{title}}

## Context

## Decision

## Consequences
`

// renderTemplate returns the new-ADR body for number n. If
// <dir>/templates/template.md exists it's used; otherwise defaultTemplate
// applies. {{number}} and {{title}} placeholders are substituted in both.
func renderTemplate(dir string, n int, title string) string {
	tmpl := defaultTemplate
	if b, err := os.ReadFile(filepath.Join(dir, "templates", "template.md")); err == nil {
		tmpl = string(b)
	}
	tmpl = strings.ReplaceAll(tmpl, "{{number}}", strconv.Itoa(n))
	tmpl = strings.ReplaceAll(tmpl, "{{title}}", title)
	return tmpl
}

// WriteFileAtomic writes data to path via a sibling temp file followed by
// rename(2). Crashing or being killed mid-write leaves either the old
// file or the new file in place — never a half-written one.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".adr-lint-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// NormalizeID converts a numeric ID (in any width) to the canonical
// 4-digit zero-padded form used by ADR filenames and the management
// commands. Non-numeric input is returned unchanged.
func NormalizeID(s string) string {
	if n, err := strconv.Atoi(s); err == nil {
		return fmt.Sprintf("%04d", n)
	}
	return s
}

var statusRE = regexp.MustCompile(`(?m)^status:\s*\S+\s*$`)

// SetStatus rewrites the `status:` line in an ADR's YAML frontmatter,
// leaving the rest of the file untouched. The bool reports whether a
// status line was found and replaced — callers should treat false as an
// error so they don't silently no-op on malformed frontmatter.
func SetStatus(body, newStatus string) (string, bool) {
	if !statusRE.MatchString(body) {
		return body, false
	}
	return statusRE.ReplaceAllString(body, "status: "+newStatus), true
}

// InsertAfterStatus inserts line on a new line immediately after the
// `status:` line in body's YAML frontmatter. Reports false (and returns
// body unchanged) when no status line is present so callers can decide
// whether to error out.
func InsertAfterStatus(body, line string) (string, bool) {
	if !statusRE.MatchString(body) {
		return body, false
	}
	return statusRE.ReplaceAllStringFunc(body, func(match string) string {
		return match + "\n" + line
	}), true
}

var slugSepRE = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(s)
	s = slugSepRE.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

var adrNumberRE = regexp.MustCompile(`^(\d{4})-`)

func nextADRNumber(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 1
	}
	highest := 0
	for _, e := range entries {
		m := adrNumberRE.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		var n int
		fmt.Sscanf(m[1], "%d", &n)
		if n > highest {
			highest = n
		}
	}
	return highest + 1
}

// DisplayPath returns p relative to the current working directory when
// that yields a shorter, non-escaping path. Used by user-facing commands
// so success messages don't leak absolute workspace paths. Cwd and the
// directory containing p are passed through filepath.EvalSymlinks so a
// /tmp-style symlink prefix mismatch doesn't force a fallback to the
// absolute form. Resolving the parent (not p itself) keeps this working
// for files that don't exist on disk yet.
func DisplayPath(p string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return p
	}
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	target := p
	if resolvedDir, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		target = filepath.Join(resolvedDir, filepath.Base(p))
	}
	rel, err := filepath.Rel(cwd, target)
	if err != nil {
		return p
	}
	sep := string(filepath.Separator)
	if rel == ".." || strings.HasPrefix(rel, ".."+sep) || len(rel) >= len(p) {
		return p
	}
	return rel
}
