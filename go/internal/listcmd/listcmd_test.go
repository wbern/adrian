package listcmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeADR(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
		t.Fatalf("seed %s: %v", name, err)
	}
}

func TestRun_IncludesADRsWithEmptyDecision(t *testing.T) {
	dir := t.TempDir()
	// A freshly-scaffolded ADR has no Decision content yet.
	writeADR(t, dir, "0001-fresh.md", "---\nstatus: proposed\n---\n# 1. Fresh Decision\n\n## Decision\n")

	var out bytes.Buffer
	if err := Run(nil, dir, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "Fresh Decision") {
		t.Errorf("list should include scaffolded ADRs; got %q", out.String())
	}
}

func TestRun_EmptyDirPrintsHelpfulMessage(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	if err := Run(nil, dir, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "No ADRs found") {
		t.Errorf("output should mention empty state; got %q", out.String())
	}
}

func TestRun_RejectsExtraArgs(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	err := Run([]string{"extra"}, dir, &out)
	if err == nil {
		t.Fatal("expected error for extra args")
	}
}

func TestRun_AnnotatesSupersededByReplacement(t *testing.T) {
	dir := t.TempDir()
	writeADR(t, dir, "0001-old.md", "---\nstatus: superseded\nsuperseded_by: \"0002\"\n---\n# 1. Old\n\n## Decision\nx\n")
	writeADR(t, dir, "0002-new.md", "---\nstatus: accepted\n---\n# 2. New\n\n## Decision\ny\n")

	var out bytes.Buffer
	if err := Run(nil, dir, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "by 0002") {
		t.Errorf("expected superseded-by annotation in output:\n%s", out.String())
	}
}

func TestRun_AnnotatesEverySupersededByReplacement(t *testing.T) {
	dir := t.TempDir()
	writeADR(t, dir, "0001-old.md", "---\nstatus: superseded\nsuperseded_by: [\"0002\", 3]\n---\n# 1. Old\n\n## Decision\nx\n")
	writeADR(t, dir, "0002-new-a.md", "---\nstatus: accepted\n---\n# 2. New A\n\n## Decision\ny\n")
	writeADR(t, dir, "0003-new-b.md", "---\nstatus: accepted\n---\n# 3. New B\n\n## Decision\nz\n")

	var out bytes.Buffer
	if err := Run(nil, dir, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "by 0002, 0003") {
		t.Errorf("expected every superseded-by annotation in output:\n%s", out.String())
	}
}

func TestRun_NormalizesNumericScalarSupersededBy(t *testing.T) {
	dir := t.TempDir()
	writeADR(t, dir, "0001-old.md", "---\nstatus: superseded\nsuperseded_by: 2\n---\n# 1. Old\n\n## Decision\nx\n")
	writeADR(t, dir, "0002-new.md", "---\nstatus: accepted\n---\n# 2. New\n\n## Decision\ny\n")
	var out bytes.Buffer
	if err := Run(nil, dir, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "by 0002") {
		t.Errorf("expected normalized numeric successor:\n%s", out.String())
	}
}

func TestRun_ListsADRsWithIDAndTitle(t *testing.T) {
	dir := t.TempDir()
	writeADR(t, dir, "0001-first.md", "---\nstatus: accepted\n---\n# 1. First Decision\n\n## Decision\nx\n")
	writeADR(t, dir, "0002-second.md", "---\nstatus: proposed\n---\n# 2. Second Decision\n\n## Decision\ny\n")

	var out bytes.Buffer
	if err := Run(nil, dir, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	s := out.String()
	for _, want := range []string{"0001", "First Decision", "0002", "Second Decision"} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q:\n%s", want, s)
		}
	}
}
