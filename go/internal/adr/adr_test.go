package adr

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

// ---------- ExtractSection ----------

func TestExtractSection_BasicSections(t *testing.T) {
	content := "# 1. Test ADR\n" +
		"\n" +
		"## Status\n" +
		"\n" +
		"Accepted\n" +
		"\n" +
		"## Context\n" +
		"\n" +
		"Some context here.\n" +
		"\n" +
		"## Decision\n" +
		"\n" +
		"The decision text.\n" +
		"\n" +
		"## Consequences\n" +
		"\n" +
		"The consequences.\n"

	cases := map[string]string{
		"Status":   "Accepted",
		"Context":  "Some context here.",
		"Decision": "The decision text.",
	}
	for name, want := range cases {
		if got := ExtractSection(content, name); got != want {
			t.Errorf("ExtractSection(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestExtractSection_MissingReturnsEmpty(t *testing.T) {
	content := "# 1. Test ADR\n\n## Status\n\nAccepted\n"
	if got := ExtractSection(content, "Decision"); got != "" {
		t.Errorf("ExtractSection(Decision) = %q, want empty", got)
	}
}

func TestExtractSection_Multiline(t *testing.T) {
	content := "# 1. Test\n\n## Decision\n\nLine 1\nLine 2\nLine 3\n\n## Consequences\n\nDone.\n"
	want := "Line 1\nLine 2\nLine 3"
	if got := ExtractSection(content, "Decision"); got != want {
		t.Errorf("ExtractSection = %q, want %q", got, want)
	}
}

// ---------- ParseADR ----------

func TestParseADR_Complete(t *testing.T) {
	content := "# 2. Use Standard Testing Helpers\n" +
		"\n" +
		"Date: 2026-01-28\n" +
		"\n" +
		"## Status\n\nAccepted\n" +
		"\n" +
		"## Applies To\n\n- `**/*_test.go`\n- `**/*_integration_test.go`\n" +
		"\n" +
		"## Complexity\n\nlite\n" +
		"\n" +
		"## Context\n\nTesting context.\n" +
		"\n" +
		"## Decision\n\nCheck for gomock imports.\n" +
		"\n" +
		"## Consequences\n\nBetter testing.\n"

	adr := ParseADR(content, "/path/to/0002-use-testify.md")

	if adr.ID != "2" {
		t.Errorf("ID = %q, want %q", adr.ID, "2")
	}
	if adr.Title != "Use Standard Testing Helpers" {
		t.Errorf("Title = %q", adr.Title)
	}
	if !reflect.DeepEqual(adr.AppliesTo, []string{"**/*_test.go", "**/*_integration_test.go"}) {
		t.Errorf("AppliesTo = %v", adr.AppliesTo)
	}
	if adr.Complexity != ComplexityLite {
		t.Errorf("Complexity = %q", adr.Complexity)
	}
	if adr.Decision != "Check for gomock imports." {
		t.Errorf("Decision = %q", adr.Decision)
	}
	if adr.FilePath != "/path/to/0002-use-testify.md" {
		t.Errorf("FilePath = %q", adr.FilePath)
	}
}

func TestParseADR_DefaultComplexityStandard(t *testing.T) {
	content := "# 1. Test\n\n## Status\n\nAccepted\n\n## Decision\n\nDo something.\n"
	adr := ParseADR(content, "0001-test.md")
	if adr.Complexity != ComplexityStandard {
		t.Errorf("Complexity = %q, want standard", adr.Complexity)
	}
}

func TestParseADR_DiffContextFalseFromFrontmatter(t *testing.T) {
	content := "---\n" +
		"status: accepted\n" +
		"applies_to:\n  - \"**/*.go\"\n" +
		"complexity: lite\n" +
		"diff_context: false\n" +
		"---\n\n" +
		"# 1. Test ADR\n\n## Decision\n\nCheck for violations.\n"
	adr := ParseADR(content, "0001-test.md")
	if adr.DiffContext != false {
		t.Errorf("DiffContext = %v, want false", adr.DiffContext)
	}
}

func TestParseADR_DiffContextDefaultsTrue(t *testing.T) {
	content := "---\nstatus: accepted\napplies_to:\n  - \"**/*.go\"\n---\n\n" +
		"# 1. Test ADR\n\n## Decision\n\nCheck for violations.\n"
	adr := ParseADR(content, "0001-test.md")
	if adr.DiffContext != true {
		t.Errorf("DiffContext = %v, want true", adr.DiffContext)
	}
}

func TestParseADR_AppliesToDefaultsToAll(t *testing.T) {
	content := "# 1. Test\n\n## Status\n\nAccepted\n\n## Decision\n\nDo something.\n"
	adr := ParseADR(content, "0001-test.md")
	if !reflect.DeepEqual(adr.AppliesTo, []string{"**/*"}) {
		t.Errorf("AppliesTo = %v, want [**/*]", adr.AppliesTo)
	}
}

func TestParseADR_NegationPatterns(t *testing.T) {
	content := "# 1. Test\n\n## Applies To\n\n- `**/*.go`\n- `!**/*_test.go`\n\n## Decision\n\nCheck.\n"
	adr := ParseADR(content, "0001-test.md")
	if !reflect.DeepEqual(adr.AppliesTo, []string{"**/*.go", "!**/*_test.go"}) {
		t.Errorf("AppliesTo = %v", adr.AppliesTo)
	}
}

func TestParseADR_YAMLFrontmatter(t *testing.T) {
	content := "---\n" +
		"status: accepted\n" +
		"date: 2026-01-28\n" +
		"applies_to:\n" +
		"  - \"**/*.go\"\n" +
		"  - \"**/*.proto\"\n" +
		"  - \"!**/*_test.go\"\n" +
		"complexity: lite\n" +
		"---\n\n" +
		"# 2. Use Standard Testing Helpers\n\n" +
		"## Context\n\nTesting context.\n\n" +
		"## Decision\n\nCheck for gomock imports.\n\n" +
		"## Consequences\n\nBetter testing.\n"

	adr := ParseADR(content, "/path/to/0002-use-testify.md")
	if adr.ID != "2" {
		t.Errorf("ID = %q", adr.ID)
	}
	if adr.Title != "Use Standard Testing Helpers" {
		t.Errorf("Title = %q", adr.Title)
	}
	want := []string{"**/*.go", "**/*.proto", "!**/*_test.go"}
	if !reflect.DeepEqual(adr.AppliesTo, want) {
		t.Errorf("AppliesTo = %v, want %v", adr.AppliesTo, want)
	}
	if adr.Complexity != ComplexityLite {
		t.Errorf("Complexity = %q", adr.Complexity)
	}
	if adr.Decision != "Check for gomock imports." {
		t.Errorf("Decision = %q", adr.Decision)
	}
}

func TestParseADR_FrontmatterComplexityComplex(t *testing.T) {
	content := "---\nstatus: accepted\ncomplexity: complex\n---\n\n" +
		"# 5. Architecture Review\n\n## Decision\n\nDeep analysis needed.\n"
	adr := ParseADR(content, "0005-arch.md")
	if adr.Complexity != ComplexityComplex {
		t.Errorf("Complexity = %q, want complex", adr.Complexity)
	}
}

func TestParseADR_FrontmatterAppliesToOverridesSection(t *testing.T) {
	content := "---\napplies_to:\n  - \"pkg/**/*.go\"\n---\n\n" +
		"# 1. Test\n\n## Applies To\n\n- `**/*.md`\n\n## Decision\n\nCheck.\n"
	adr := ParseADR(content, "0001-test.md")
	if !reflect.DeepEqual(adr.AppliesTo, []string{"pkg/**/*.go"}) {
		t.Errorf("AppliesTo = %v, want [pkg/**/*.go]", adr.AppliesTo)
	}
}

func TestParseADR_StatusFromFrontmatter(t *testing.T) {
	content := "---\nstatus: deprecated\n---\n\n" +
		"# 9. Old Convention\n\n## Decision\n\nCheck something.\n"
	adr := ParseADR(content, "0009-old.md")
	if adr.Status != StatusDeprecated {
		t.Errorf("Status = %q, want deprecated", adr.Status)
	}
}

func TestParseADR_StatusRejectedFromFrontmatter(t *testing.T) {
	content := "---\nstatus: rejected\n---\n\n" +
		"# 7. Bad Idea\n\n## Decision\n\nx\n"
	adr := ParseADR(content, "0007-bad.md")
	if adr.Status != StatusRejected {
		t.Errorf("Status = %q, want rejected", adr.Status)
	}
}

func TestParseADR_StatusWithdrawnFromFrontmatter(t *testing.T) {
	content := "---\nstatus: withdrawn\n---\n\n" +
		"# 8. Pulled Back\n\n## Decision\n\nx\n"
	adr := ParseADR(content, "0008-pulled.md")
	if adr.Status != StatusWithdrawn {
		t.Errorf("Status = %q, want withdrawn", adr.Status)
	}
}

func TestParseADR_StatusDefaultsAccepted(t *testing.T) {
	content := "---\ncomplexity: lite\n---\n\n" +
		"# 2. Use Testify\n\n## Decision\n\nCheck for gomock.\n"
	adr := ParseADR(content, "0002-testify.md")
	if adr.Status != StatusAccepted {
		t.Errorf("Status = %q, want accepted", adr.Status)
	}
}

// normalizePreFilter folds the `string | []string` YAML union into a
// slice; this test pins the single-string case.
func TestParseADR_PreFilterStringNormalized(t *testing.T) {
	content := "---\nstatus: accepted\ncomplexity: ultralite\npre_filter: \"gomock\"\n---\n\n" +
		"# 2. Use Testify\n\n## Decision\n\nCheck for gomock imports.\n"
	adr := ParseADR(content, "0002-testify.md")
	if !reflect.DeepEqual(adr.PreFilter, []string{"gomock"}) {
		t.Errorf("PreFilter = %v, want [gomock]", adr.PreFilter)
	}
}

func TestParseADR_PreFilterAbsentIsNil(t *testing.T) {
	content := "---\ncomplexity: lite\n---\n\n" +
		"# 3. Some ADR\n\n## Decision\n\nCheck something.\n"
	adr := ParseADR(content, "0003-some.md")
	if adr.PreFilter != nil {
		t.Errorf("PreFilter = %v, want nil", adr.PreFilter)
	}
}

func TestParseADR_EnforcedByFromFrontmatter(t *testing.T) {
	content := "---\nstatus: accepted\nenforced_by: eslint\npre_filter: \"../\"\n---\n\n" +
		"# 7. No Relative Cross-Package Imports\n\n" +
		"## Decision\n\nUse package names for cross-package imports.\n"
	adr := ParseADR(content, "0007-no-relative.md")
	if adr.EnforcedBy == nil || *adr.EnforcedBy != "eslint" {
		t.Errorf("EnforcedBy = %v, want *\"eslint\"", adr.EnforcedBy)
	}
}

func TestParseADR_EnforcedByAbsentIsNil(t *testing.T) {
	content := "---\nstatus: accepted\n---\n\n" +
		"# 3. Some ADR\n\n## Decision\n\nCheck something.\n"
	adr := ParseADR(content, "0003-some.md")
	if adr.EnforcedBy != nil {
		t.Errorf("EnforcedBy = %v, want nil", *adr.EnforcedBy)
	}
}

// ---------- ParseADR — Decision extraction ----------

func TestParseADR_DecisionWithCodeBlocks(t *testing.T) {
	content := "---\nstatus: accepted\napplies_to:\n  - \"**/*.go\"\n---\n\n" +
		"# 11. Use Active Voice in Commit Messages\n\n" +
		"## Context\n\nCommit messages are often written in passive voice, making them harder to understand.\n\n" +
		"## Decision\n\n" +
		"We will write all commit messages in active voice, starting with an imperative verb.\n\n" +
		"**Forbidden pattern:**\n```\n// BAD - passive voice\n\"Bug was fixed in the login flow\"\n```\n\n" +
		"**Required pattern:**\n```\n// GOOD - active voice\n\"Fix bug in login flow\"\n```\n\n" +
		"## Consequences\n\n**Positive:**\n- Clearer commit history\n- Consistent style\n\n" +
		"**Negative:**\n- Requires habit change\n\n" +
		"## References\n\n- Git documentation on commit messages\n"

	adr := ParseADR(content, "0011-active-voice.md")
	mustContain(t, adr.Decision, "We will write all commit messages in active voice")
	mustContain(t, adr.Decision, "Forbidden pattern")
	mustContain(t, adr.Decision, "Required pattern")
}

func TestParseADR_DecisionMinimal(t *testing.T) {
	content := "---\nstatus: accepted\napplies_to:\n  - \"**/*.go\"\n---\n\n" +
		"# 12. Use Const Assertions\n\n" +
		"## Context\n\nType inference can be too broad.\n\n" +
		"## Decision\n\nWe will use const assertions for literal types.\n\n" +
		"## Consequences\n\nBetter type narrowing.\n"

	adr := ParseADR(content, "0012-const.md")
	if len(adr.Decision) == 0 {
		t.Fatalf("Decision was empty")
	}
	mustContain(t, adr.Decision, "We will use const assertions")
}

// ---------- ParseADRs ----------

// writeADRs builds a hermetic ADR directory in t.TempDir().
func writeADRs(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func TestParseADRs_FiltersDeprecatedAndSuperseded(t *testing.T) {
	dir := writeADRs(t, map[string]string{
		"0001-keep.md":       "---\nstatus: accepted\n---\n\n# 1. Keep\n\n## Decision\n\nDo it.\n",
		"0002-deprecated.md": "---\nstatus: deprecated\n---\n\n# 2. Old\n\n## Decision\n\nOld thing.\n",
		"0003-superseded.md": "---\nstatus: superseded\n---\n\n# 3. Older\n\n## Decision\n\nOlder thing.\n",
		"0004-keep.md":       "---\nstatus: proposed\n---\n\n# 4. Maybe\n\n## Decision\n\nMaybe do it.\n",
	})

	adrs, err := ParseADRs(dir)
	if err != nil {
		t.Fatalf("ParseADRs: %v", err)
	}
	for _, a := range adrs {
		if a.Status == StatusDeprecated || a.Status == StatusSuperseded {
			t.Errorf("ParseADRs returned %s ADR %s", a.Status, a.ID)
		}
	}
	if len(adrs) != 2 {
		t.Errorf("len(adrs) = %d, want 2 (ids: %v)", len(adrs), idsOf(adrs))
	}
}

func TestParseADRs_FiltersRejectedAndWithdrawn(t *testing.T) {
	dir := writeADRs(t, map[string]string{
		"0001-keep.md":      "---\nstatus: accepted\n---\n\n# 1. Keep\n\n## Decision\n\nDo it.\n",
		"0002-rejected.md":  "---\nstatus: rejected\n---\n\n# 2. No\n\n## Decision\n\nNo.\n",
		"0003-withdrawn.md": "---\nstatus: withdrawn\n---\n\n# 3. Pulled\n\n## Decision\n\nPulled.\n",
	})

	adrs, err := ParseADRs(dir)
	if err != nil {
		t.Fatalf("ParseADRs: %v", err)
	}
	for _, a := range adrs {
		if a.Status == StatusRejected || a.Status == StatusWithdrawn {
			t.Errorf("ParseADRs returned %s ADR %s", a.Status, a.ID)
		}
	}
	if len(adrs) != 1 {
		t.Errorf("len(adrs) = %d, want 1 (ids: %v)", len(adrs), idsOf(adrs))
	}
}

func TestParseADRs_FiltersEnforcedBy(t *testing.T) {
	dir := writeADRs(t, map[string]string{
		"0001-ai.md":     "---\nstatus: accepted\n---\n\n# 1. AI Lint\n\n## Decision\n\nCheck it.\n",
		"0002-eslint.md": "---\nstatus: accepted\nenforced_by: eslint\n---\n\n# 2. ESLint\n\n## Decision\n\nLint it.\n",
	})

	adrs, err := ParseADRs(dir)
	if err != nil {
		t.Fatalf("ParseADRs: %v", err)
	}
	for _, a := range adrs {
		if a.EnforcedBy != nil {
			t.Errorf("ParseADRs returned enforced_by=%q ADR %s", *a.EnforcedBy, a.ID)
		}
	}
	if len(adrs) != 1 {
		t.Errorf("len(adrs) = %d, want 1 (ids: %v)", len(adrs), idsOf(adrs))
	}
}

func TestParseADRs_FiltersEmptyDecision(t *testing.T) {
	dir := writeADRs(t, map[string]string{
		"0001-real.md":  "---\nstatus: accepted\n---\n\n# 1. Real\n\n## Decision\n\nDo it.\n",
		"0002-empty.md": "---\nstatus: accepted\n---\n\n# 2. Empty\n\n## Context\n\nJust context.\n",
	})

	adrs, err := ParseADRs(dir)
	if err != nil {
		t.Fatalf("ParseADRs: %v", err)
	}
	if len(adrs) != 1 || adrs[0].ID != "1" {
		t.Errorf("ParseADRs returned %v, want only ADR 1", idsOf(adrs))
	}
}

func TestParseADRs_IgnoresNonADRFiles(t *testing.T) {
	dir := writeADRs(t, map[string]string{
		"0001-keep.md":      "---\nstatus: accepted\n---\n\n# 1. Keep\n\n## Decision\n\nDo it.\n",
		"README.md":         "# Not an ADR\n",
		"template.md":       "# Template\n\n## Decision\n\nN/A\n",
		"0002-also-keep.md": "---\nstatus: accepted\n---\n\n# 2. Also\n\n## Decision\n\nAlso.\n",
		"notes.txt":         "scratch\n",
	})

	adrs, err := ParseADRs(dir)
	if err != nil {
		t.Fatalf("ParseADRs: %v", err)
	}
	if len(adrs) != 2 {
		t.Errorf("len(adrs) = %d, want 2 (ids: %v)", len(adrs), idsOf(adrs))
	}
}

func TestCreate_WritesNumberedFileInEmptyDir(t *testing.T) {
	dir := t.TempDir()

	path, err := Create(dir, "Use Testify")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	wantPath := filepath.Join(dir, "0001-use-testify.md")
	if path != wantPath {
		t.Errorf("path = %q, want %q", path, wantPath)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Errorf("expected file at %q: %v", wantPath, err)
	}
}

func TestCreate_NumbersIncrementFromExisting(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "0001-first.md"), []byte("---\nstatus: accepted\n---\n## Decision\nx"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "0007-seventh.md"), []byte("---\nstatus: accepted\n---\n## Decision\nx"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	path, err := Create(dir, "Next One")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	want := filepath.Join(dir, "0008-next-one.md")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
}

func TestCreate_SlugStripsNonAlphanumerics(t *testing.T) {
	dir := t.TempDir()
	path, err := Create(dir, "ADRs live in doc/adr (4-digit)")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	want := filepath.Join(dir, "0001-adrs-live-in-doc-adr-4-digit.md")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected file at %q: %v", path, err)
	}
}

func TestCreate_WritesTemplateContent(t *testing.T) {
	dir := t.TempDir()
	path, err := Create(dir, "Use Testify")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(body)
	for _, want := range []string{
		"---\n",
		"status: proposed\n",
		"applies_to:\n",
		`  - "**/*"`,
		"# 1. Use Testify",
		"## Context",
		"## Decision",
		"## Consequences",
	} {
		if !contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}

func TestCreate_UsesDiskTemplateWhenPresent(t *testing.T) {
	dir := t.TempDir()
	tmplDir := filepath.Join(dir, "templates")
	if err := os.MkdirAll(tmplDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	tmpl := "---\nstatus: proposed\n---\n\n# {{number}}. {{title}}\n\nSENTINEL_BODY_MARKER\n"
	if err := os.WriteFile(filepath.Join(tmplDir, "template.md"), []byte(tmpl), 0644); err != nil {
		t.Fatalf("seed template: %v", err)
	}

	path, err := Create(dir, "Use Disk Template")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(body)
	if !contains(s, "SENTINEL_BODY_MARKER") {
		t.Errorf("on-disk template should be used; body:\n%s", s)
	}
	if !contains(s, "# 1. Use Disk Template") {
		t.Errorf("placeholders should be substituted; body:\n%s", s)
	}
}

func TestValidateFrontmatter_NoFrontmatterIsValid(t *testing.T) {
	if err := ValidateFrontmatter("# 1. Title\n\n## Decision\nx\n"); err != nil {
		t.Errorf("absent frontmatter should be nil err; got %v", err)
	}
}

func TestValidateFrontmatter_GoodYAMLIsValid(t *testing.T) {
	content := "---\nstatus: accepted\n---\n# 1. T\n\n## Decision\nx\n"
	if err := ValidateFrontmatter(content); err != nil {
		t.Errorf("good yaml should be nil err; got %v", err)
	}
}

func TestValidateFrontmatter_MalformedYAMLErrors(t *testing.T) {
	content := "---\nstatus: \"oops\nbroken: [unterminated\n---\n# 1. T\n\n## Decision\nx\n"
	if err := ValidateFrontmatter(content); err == nil {
		t.Error("malformed yaml should return error")
	}
}

func TestWriteFileAtomic_WritesContentAndMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := WriteFileAtomic(path, []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(body) != "hello" {
		t.Errorf("body = %q, want %q", body, "hello")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0644 {
		t.Errorf("mode = %v, want 0644", info.Mode().Perm())
	}
}

func TestWriteFileAtomic_LeavesNoTempArtifacts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := WriteFileAtomic(path, []byte("hi"), 0644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "file.txt" {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("expected only file.txt, got %v", names)
	}
}

func TestCreate_ConcurrentCallsAllocateDistinctPaths(t *testing.T) {
	dir := t.TempDir()
	const N = 20
	paths := make([]string, N)
	errs := make([]error, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p, err := Create(dir, "Same Title")
			paths[i] = p
			errs[i] = err
		}(i)
	}
	wg.Wait()
	seen := map[string]int{}
	for i, p := range paths {
		if errs[i] != nil {
			t.Errorf("call %d: %v", i, errs[i])
			continue
		}
		seen[p]++
	}
	for p, n := range seen {
		if n > 1 {
			t.Errorf("path %q allocated %d times", p, n)
		}
	}
	if len(seen) != N {
		t.Errorf("got %d distinct paths, want %d", len(seen), N)
		// help debugging
		for p := range seen {
			fmt.Println(" ", p)
		}
	}
}

func TestNormalizeID_PadsNumericIDs(t *testing.T) {
	cases := map[string]string{
		"1":    "0001",
		"42":   "0042",
		"0007": "0007",
		"1234": "1234",
	}
	for in, want := range cases {
		if got := NormalizeID(in); got != want {
			t.Errorf("NormalizeID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeID_PassesThroughNonNumeric(t *testing.T) {
	if got := NormalizeID("custom-id"); got != "custom-id" {
		t.Errorf("NormalizeID(%q) = %q, want pass-through", "custom-id", got)
	}
}

func TestParseADR_SupersededByFromFrontmatter(t *testing.T) {
	content := "---\nstatus: superseded\nsuperseded_by: \"0042\"\n---\n# 1. Old\n\n## Decision\nx\n"
	a := ParseADR(content, "0001.md")
	if !reflect.DeepEqual(a.SupersededBy, []string{"0042"}) {
		t.Errorf("SupersededBy = %q, want %q", a.SupersededBy, []string{"0042"})
	}
}

func TestParseADR_SupersededBySequencePreservesEverySuccessor(t *testing.T) {
	content := "---\nstatus: superseded\nsuperseded_by: [2, \"0042\"]\n---\n# 1. Old\n\n## Decision\nx\n"
	a := ParseADR(content, "0001.md")
	if !reflect.DeepEqual(a.SupersededBy, []string{"0002", "0042"}) {
		t.Errorf("SupersededBy = %q, want every normalized successor", a.SupersededBy)
	}
}

func TestParseADR_SupersededByAbsentIsEmpty(t *testing.T) {
	content := "---\nstatus: accepted\n---\n# 1. X\n\n## Decision\ny\n"
	a := ParseADR(content, "0001.md")
	if len(a.SupersededBy) != 0 {
		t.Errorf("SupersededBy = %q, want empty", a.SupersededBy)
	}
}

func TestSetStatus_ReplacesExistingLine(t *testing.T) {
	body := "---\nstatus: accepted\napplies_to:\n  - \"**/*\"\n---\n# 1\n"
	got, ok := SetStatus(body, "deprecated")
	if !ok {
		t.Fatal("ok=false, want true when status line is present")
	}
	if !contains(got, "status: deprecated") {
		t.Errorf("missing rewritten status line in:\n%s", got)
	}
	if contains(got, "status: accepted") {
		t.Errorf("old status line still present in:\n%s", got)
	}
}

func TestInsertAfterStatus_PlacesLineImmediatelyAfterStatus(t *testing.T) {
	body := "---\nstatus: accepted\napplies_to:\n  - \"**/*\"\n---\n# 1\n"
	got, ok := InsertAfterStatus(body, `superseded_by: "0002"`)
	if !ok {
		t.Fatal("ok=false, want true when status line is present")
	}
	want := "---\nstatus: accepted\nsuperseded_by: \"0002\"\napplies_to:\n  - \"**/*\"\n---\n# 1\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestInsertAfterStatus_ReportsMissingStatusLine(t *testing.T) {
	body := "---\napplies_to:\n  - \"**/*\"\n---\n# 1\n"
	got, ok := InsertAfterStatus(body, "x: y")
	if ok {
		t.Error("ok=true, want false when status line is absent")
	}
	if got != body {
		t.Errorf("body mutated; got:\n%s", got)
	}
}

func TestSetStatus_ReportsMissingLine(t *testing.T) {
	body := "---\napplies_to:\n  - \"**/*\"\n---\n# 1\n"
	got, ok := SetStatus(body, "deprecated")
	if ok {
		t.Error("ok=true, want false when no status line is present")
	}
	if got != body {
		t.Errorf("body changed unexpectedly; got:\n%s", got)
	}
}

// ---------- helpers ----------

func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !contains(haystack, needle) {
		t.Errorf("expected substring %q in:\n%s", needle, haystack)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func idsOf(adrs []ADR) []string {
	out := make([]string, 0, len(adrs))
	for _, a := range adrs {
		out = append(out, a.ID)
	}
	return out
}

// ---------- DisplayPath ----------
//
// These tests mutate os.Chdir and are therefore NOT parallel-safe — do not
// add t.Parallel() here, and be cautious adding it to other tests in this
// package while these exist.

// chdirTo switches into dir for the duration of the test, restoring the
// previous cwd via t.Cleanup. Fails the test on any error so silent cwd
// drift can't poison sibling tests.
func chdirTo(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(prev); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
}

func TestDisplayPath_RelativeWhenInsideCwd(t *testing.T) {
	tmp := t.TempDir()
	chdirTo(t, tmp)

	dir := filepath.Join(tmp, "doc", "adr")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	abs := filepath.Join(dir, "0001-foo.md")
	got := DisplayPath(abs)
	want := filepath.Join("doc", "adr", "0001-foo.md")
	if got != want {
		t.Errorf("DisplayPath(%q) = %q, want %q", abs, got, want)
	}
}

func TestDisplayPath_AbsoluteWhenRelativeEscapes(t *testing.T) {
	tmp := t.TempDir()
	inner := filepath.Join(tmp, "inner")
	if err := os.MkdirAll(inner, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdirTo(t, inner)

	// abs lives in a sibling dir — relative path would start with "..".
	abs := filepath.Join(tmp, "outside", "file.md")
	if got := DisplayPath(abs); got != abs {
		t.Errorf("DisplayPath(%q) = %q, want absolute %q", abs, got, abs)
	}
}

func TestDisplayPath_AbsoluteWhenRelativeLonger(t *testing.T) {
	tmp := t.TempDir()
	// Cwd deep under tmp; the abs file is one level under the filesystem root.
	deep := filepath.Join(tmp, "a", "b", "c", "d")
	if err := os.MkdirAll(deep, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdirTo(t, deep)

	abs := filepath.Join(string(filepath.Separator), "x.md")
	got := DisplayPath(abs)
	if got != abs {
		t.Errorf("DisplayPath(%q) = %q, want absolute %q (relative would be longer)", abs, got, abs)
	}
}
