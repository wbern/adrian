package resolvecmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type mockGitRunner struct {
	outputs map[string]string
	errors  map[string]error
}

func (m *mockGitRunner) RunGit(args ...string) ([]byte, error) {
	cmd := strings.Join(args, " ")
	if err, ok := m.errors[cmd]; ok {
		return nil, err
	}
	if out, ok := m.outputs[cmd]; ok {
		return []byte(out), nil
	}
	return nil, nil
}

func setupTestADRDir(t *testing.T) (string, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "adrian-resolve-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}

	adr1 := `---
status: accepted
applies_to:
  - "src/routes/**/*.tsx"
tags:
  - ux
complexity: standard
---

# 1. Route Component Archetypes

## Decision
All routes must use PageShell.
`

	adr2 := `---
status: accepted
applies_to:
  - "src/lib/db/**"
  - "supabase/migrations/**"
tags:
  - data
  - security
complexity: standard
---

# 2. Migration Invariants

## Decision
Migrations are forward-only.
`

	adr3 := `---
status: proposed
applies_to:
  - "src/**/*.css"
tags:
  - ux
complexity: lite
---

# 3. Design Tokens

## Decision
Use CSS variables.
`

	if err := os.WriteFile(filepath.Join(dir, "0001-routes.md"), []byte(adr1), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "0002-migrations.md"), []byte(adr2), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "0003-tokens.md"), []byte(adr3), 0644); err != nil {
		t.Fatal(err)
	}

	return dir, func() { _ = os.RemoveAll(dir) }
}

func TestResolve_BasicFileMatching(t *testing.T) {
	adrDir, cleanup := setupTestADRDir(t)
	defer cleanup()

	var stdout, stderr bytes.Buffer
	args := []string{"--adr-dir", adrDir, "src/routes/app/index.tsx", "src/styles.css"}

	err := Run(args, adrDir, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	outStr := stdout.String()
	if !strings.Contains(outStr, "Accepted (enforced):") {
		t.Errorf("expected Accepted (enforced) section, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "ADR-0001 — Route Component Archetypes") {
		t.Errorf("expected ADR-0001, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "Proposed (advisory):") {
		t.Errorf("expected Proposed (advisory) section, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "ADR-0003 — Design Tokens") {
		t.Errorf("expected ADR-0003, got:\n%s", outStr)
	}
	if strings.Contains(outStr, "ADR-0002") {
		t.Errorf("ADR-0002 should not match routes or css, got:\n%s", outStr)
	}
}

func TestResolve_NoMatchingFiles(t *testing.T) {
	adrDir, cleanup := setupTestADRDir(t)
	defer cleanup()

	var stdout, stderr bytes.Buffer
	args := []string{"--adr-dir", adrDir, "docs/architecture.md"}

	err := Run(args, adrDir, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	outStr := strings.TrimSpace(stdout.String())
	want := "No applicable ADRs for the given files."
	if outStr != want {
		t.Errorf("got %q, want %q", outStr, want)
	}
}

func TestResolve_EmptyFiles(t *testing.T) {
	adrDir, cleanup := setupTestADRDir(t)
	defer cleanup()

	var stdout, stderr bytes.Buffer
	args := []string{"--adr-dir", adrDir}

	err := Run(args, adrDir, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	outStr := strings.TrimSpace(stdout.String())
	want := "No files given/changed — nothing to resolve."
	if outStr != want {
		t.Errorf("got %q, want %q", outStr, want)
	}
}

func TestResolve_TagFiltering(t *testing.T) {
	adrDir, cleanup := setupTestADRDir(t)
	defer cleanup()

	// 1. Tag filtering to 'ux'
	var stdout, stderr bytes.Buffer
	args := []string{"--adr-dir", adrDir, "--tag", "ux", "src/routes/app/index.tsx", "src/lib/db/client.ts"}

	err := Run(args, adrDir, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	outStr := stdout.String()
	if !strings.Contains(outStr, "ADR-0001") {
		t.Errorf("expected ADR-0001 under tag ux, got:\n%s", outStr)
	}
	if strings.Contains(outStr, "ADR-0002") {
		t.Errorf("ADR-0002 has tags [data security], should be filtered out by tag ux, got:\n%s", outStr)
	}
	if !strings.Contains(stderr.String(), "--tag ux narrowed") {
		t.Errorf("expected diagnostic log in stderr, got:\n%s", stderr.String())
	}

	// 2. Missing tag argument -> ErrSyntax (exit code 2)
	stdout.Reset()
	stderr.Reset()
	errSyntax := Run([]string{"--adr-dir", adrDir, "--tag"}, adrDir, &stdout, &stderr)
	if !errors.Is(errSyntax, ErrSyntax) {
		t.Errorf("expected ErrSyntax on missing tag value, got %v", errSyntax)
	}
	if !strings.Contains(stderr.String(), "--tag requires a name") {
		t.Errorf("expected error message in stderr, got:\n%s", stderr.String())
	}
}

func TestResolve_TopFlag(t *testing.T) {
	adrDir, cleanup := setupTestADRDir(t)
	defer cleanup()

	var stdout, stderr bytes.Buffer
	// Invalid top: --top 0, followed by a real file
	args := []string{"--adr-dir", adrDir, "--top", "0", "src/routes/app/index.tsx"}

	err := Run(args, adrDir, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if !strings.Contains(stderr.String(), "ignoring --top 0") {
		t.Errorf("expected warning about --top 0, got:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "ADR-0001") {
		t.Errorf("expected ADR-0001 to still match even if --top was invalid, got:\n%s", stdout.String())
	}
}

func TestResolve_JSONFormat(t *testing.T) {
	adrDir, cleanup := setupTestADRDir(t)
	defer cleanup()

	var stdout, stderr bytes.Buffer
	args := []string{"--adr-dir", adrDir, "--json", "src/routes/app/index.tsx"}

	err := Run(args, adrDir, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	var rec FrontloadRecord
	if jErr := json.Unmarshal(stdout.Bytes(), &rec); jErr != nil {
		t.Fatalf("Unmarshal failed: %v, raw:\n%s", jErr, stdout.String())
	}
	if rec.Schema != FrontloadSchema {
		t.Errorf("schema = %q, want %q", rec.Schema, FrontloadSchema)
	}
	if rec.ChangedFiles != 1 {
		t.Errorf("changed_files = %d, want 1", rec.ChangedFiles)
	}
}

func TestResolve_PRBlockFormat(t *testing.T) {
	adrDir, cleanup := setupTestADRDir(t)
	defer cleanup()

	var stdout, stderr bytes.Buffer
	args := []string{"--adr-dir", adrDir, "--pr-block", "src/routes/app/index.tsx"}

	err := Run(args, adrDir, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	outStr := stdout.String()
	if !strings.Contains(outStr, FrontloadMarker) {
		t.Errorf("expected FrontloadMarker, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "<details>") || !strings.Contains(outStr, "</details>") {
		t.Errorf("expected collapsible details tag, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "```json") {
		t.Errorf("expected embedded json fenced block, got:\n%s", outStr)
	}
}

func TestPreferFresherBase(t *testing.T) {
	runner := &mockGitRunner{
		outputs: map[string]string{
			"rev-parse --verify --quiet origin/main^{commit}": "abcdef",
			"merge-base --is-ancestor main origin/main":       "",
		},
		errors: map[string]error{
			"merge-base --is-ancestor origin/main main": errors.New("not ancestor"),
		},
	}

	var stderr bytes.Buffer
	ref, switched := PreferFresherBase("main", runner, &stderr)
	if !switched {
		t.Errorf("expected switched=true")
	}
	if ref != "origin/main" {
		t.Errorf("ref = %q, want origin/main", ref)
	}
	if !strings.Contains(stderr.String(), "diffing against origin/main instead") {
		t.Errorf("expected stderr log, got: %s", stderr.String())
	}
}

func TestRankRules_BurstAndOrdering(t *testing.T) {
	weights := &FindingWeightsData{
		Buckets: map[string]FindingWeightsBucket{
			"src/routes/**": {
				Bucket: "src/routes/**",
				Rules: []FindingWeightsEntry{
					{
						ID:         "0001",
						PRs:        10,
						Findings:   15,
						DaysActive: 5, // non-burst
					},
					{
						ID:         "0002",
						PRs:        25,
						Findings:   40,
						DaysActive: 1, // BURST (<= 2 days active)
					},
					{
						ID:         "lens#1",
						PRs:        8,
						Findings:   12,
						DaysActive: 4, // non-burst lens
					},
				},
			},
		},
	}

	candidates := []ResolvedEntry{
		{ID: "0001", Title: "Route Shell", Files: []string{"src/routes/app.tsx"}},
		{ID: "0002", Title: "Heavy DB", Files: []string{"src/routes/app.tsx"}},
		{ID: "0003", Title: "Untriggered Rule", Files: []string{"src/routes/app.tsx"}},
	}

	lensTitles := map[string]string{
		"lens#1": "Behavioral Correctness",
	}

	res := RankRules(candidates, weights, []string{"src/routes/app.tsx"}, lensTitles)

	if len(res.Ranked) != 3 {
		t.Fatalf("expected 3 ranked rules, got %d", len(res.Ranked))
	}

	// Rule 0001 (non-burst, 10 PRs) should beat lens#1 (non-burst, 8 PRs)
	// Both non-burst rules should beat Rule 0002 (burst, 1 day active), despite 0002 having 25 PRs!
	if res.Ranked[0].ID != "0001" {
		t.Errorf("Rank 1 = %q, want 0001", res.Ranked[0].ID)
	}
	if res.Ranked[1].ID != "lens#1" {
		t.Errorf("Rank 2 = %q, want lens#1", res.Ranked[1].ID)
	}
	if res.Ranked[2].ID != "0002" {
		t.Errorf("Rank 3 = %q (burst rule), want 0002", res.Ranked[2].ID)
	}

	// 0003 was not in weights, so it should be in Untriggered
	if len(res.Untriggered) != 1 || res.Untriggered[0].ID != "0003" {
		t.Errorf("Untriggered = %v, want [0003]", res.Untriggered)
	}
}

func TestRawFindingRow_PRString(t *testing.T) {
	cases := []struct {
		input RawFindingRow
		want  string
	}{
		{RawFindingRow{PR: "2357"}, "2357"},
		{RawFindingRow{PR: float64(2357)}, "2357"},
		{RawFindingRow{PR: int(2357)}, "2357"},
		{RawFindingRow{PR: int64(2357)}, "2357"},
		{RawFindingRow{PR: json.Number("2357")}, "2357"},
	}
	for _, tc := range cases {
		got := tc.input.PRString()
		if got != tc.want {
			t.Errorf("PRString(%v) = %q, want %q", tc.input.PR, got, tc.want)
		}
	}
}

func testGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@example.test",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@example.test",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
}

func setupGitFixtureRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	testGit(t, dir, "init", "-q")
	testGit(t, dir, "checkout", "-b", "main")

	adrContent := `---
status: accepted
applies_to:
  - "src/routes/**/*.tsx"
tags:
  - ux
complexity: standard
---

# 1. Route Component Archetypes

## Decision
All routes must use PageShell.
`
	adrDir := filepath.Join(dir, "doc/adr")
	if err := os.MkdirAll(adrDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adrDir, "0001-routes.md"), []byte(adrContent), 0644); err != nil {
		t.Fatal(err)
	}

	srcDir := filepath.Join(dir, "src/routes")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "app.tsx"), []byte("// initial\n"), 0644); err != nil {
		t.Fatal(err)
	}

	testGit(t, dir, "add", ".")
	testGit(t, dir, "commit", "-qm", "initial commit")
	baseCommit := testGit(t, dir, "rev-parse", "HEAD")

	// Branch and modify
	testGit(t, dir, "checkout", "-b", "feat/route-change")
	if err := os.WriteFile(filepath.Join(srcDir, "app.tsx"), []byte("// modified\n"), 0644); err != nil {
		t.Fatal(err)
	}
	testGit(t, dir, "commit", "-qam", "modify route")

	return dir, baseCommit
}

func TestResolve_GitDiff_ExplicitBase(t *testing.T) {
	dir, baseCommit := setupGitFixtureRepo(t)
	adrDir := filepath.Join(dir, "doc/adr")

	var stdout, stderr bytes.Buffer
	args := []string{"--adr-dir", adrDir, "--base", baseCommit}

	err := Run(args, adrDir, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	outStr := stdout.String()
	if !strings.Contains(outStr, "Accepted (enforced):") {
		t.Errorf("expected Accepted (enforced), got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "ADR-0001 — Route Component Archetypes") {
		t.Errorf("expected ADR-0001, got:\n%s", outStr)
	}
}

func TestResolve_GitDiff_DefaultBase(t *testing.T) {
	dir, _ := setupGitFixtureRepo(t)
	adrDir := filepath.Join(dir, "doc/adr")

	// In this repo, 'main' exists, so default base falls back to 'main'
	var stdout, stderr bytes.Buffer
	args := []string{"--adr-dir", adrDir}

	err := Run(args, adrDir, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	outStr := stdout.String()
	if !strings.Contains(outStr, "Accepted (enforced):") {
		t.Errorf("expected Accepted (enforced) from default main base diff, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "ADR-0001 — Route Component Archetypes") {
		t.Errorf("expected ADR-0001, got:\n%s", outStr)
	}
}

func TestResolve_FormatFlag_JSON(t *testing.T) {
	dir, baseCommit := setupGitFixtureRepo(t)
	adrDir := filepath.Join(dir, "doc/adr")

	var stdout, stderr bytes.Buffer
	args := []string{"--adr-dir", adrDir, "--base", baseCommit, "--format", "json"}

	err := Run(args, adrDir, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	var rec FrontloadRecord
	if jErr := json.Unmarshal(stdout.Bytes(), &rec); jErr != nil {
		t.Fatalf("Unmarshal failed: %v, raw:\n%s", jErr, stdout.String())
	}
	if rec.Schema != FrontloadSchema {
		t.Errorf("schema = %q, want %q", rec.Schema, FrontloadSchema)
	}
	if rec.ChangedFiles != 1 {
		t.Errorf("changed_files = %d, want 1", rec.ChangedFiles)
	}
}

func TestResolve_FormatFlag_PRBlock(t *testing.T) {
	dir, baseCommit := setupGitFixtureRepo(t)
	adrDir := filepath.Join(dir, "doc/adr")

	var stdout, stderr bytes.Buffer
	args := []string{"--adr-dir", adrDir, "--base", baseCommit, "--format", "pr-block"}

	err := Run(args, adrDir, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	outStr := stdout.String()
	if !strings.Contains(outStr, FrontloadMarker) {
		t.Errorf("expected FrontloadMarker, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "<details>") || !strings.Contains(outStr, "</details>") {
		t.Errorf("expected collapsible details tag, got:\n%s", outStr)
	}
}

