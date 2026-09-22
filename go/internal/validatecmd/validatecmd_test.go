package validatecmd

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

func TestRun_FlagsDuplicateIDs(t *testing.T) {
	dir := t.TempDir()
	writeADR(t, dir, "0001-first.md",
		"---\nstatus: accepted\n---\n# 1. First\n\n## Decision\nx\n")
	writeADR(t, dir, "0001-dup.md",
		"---\nstatus: accepted\n---\n# 1. Dup\n\n## Decision\ny\n")

	var out bytes.Buffer
	err := Run(nil, dir, &out)
	if err == nil {
		t.Fatal("expected error for duplicate ID")
	}
	if !strings.Contains(err.Error(), "0001") || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("err should describe duplicate ID 0001; got %q", err.Error())
	}
}

func TestRun_ReportsAllIssuesNotJustFirst(t *testing.T) {
	dir := t.TempDir()
	writeADR(t, dir, "0001-orphan.md",
		"---\nstatus: superseded\n---\n# 1. Orphan\n\n## Decision\nx\n")
	writeADR(t, dir, "0002-dangling.md",
		"---\nstatus: superseded\nsuperseded_by: \"0099\"\n---\n# 2. Dangling\n\n## Decision\nx\n")

	var out bytes.Buffer
	err := Run(nil, dir, &out)
	if err == nil {
		t.Fatal("expected error for multiple issues")
	}
	msg := err.Error()
	if !strings.Contains(msg, "0001") {
		t.Errorf("err should mention 0001 (orphan superseded); got %q", msg)
	}
	if !strings.Contains(msg, "0099") {
		t.Errorf("err should mention 0099 (dangling target); got %q", msg)
	}
}

func TestRun_FlagsMalformedFrontmatter(t *testing.T) {
	dir := t.TempDir()
	// Broken YAML: tab inside mapping value, unterminated string.
	writeADR(t, dir, "0001-broken.md",
		"---\nstatus: \"oops\nbroken: [unterminated\n---\n# 1. Broken\n\n## Decision\nx\n")

	var out bytes.Buffer
	err := Run(nil, dir, &out)
	if err == nil {
		t.Fatal("expected error for malformed frontmatter")
	}
	if !strings.Contains(err.Error(), "0001") {
		t.Errorf("err should mention which ADR has malformed YAML; got %q", err.Error())
	}
}

func TestRun_AcceptsValidSet(t *testing.T) {
	dir := t.TempDir()
	writeADR(t, dir, "0001-old.md",
		"---\nstatus: superseded\nsuperseded_by: \"0002\"\n---\n# 1. Old\n\n## Decision\nx\n")
	writeADR(t, dir, "0002-new.md",
		"---\nstatus: accepted\n---\n# 2. New\n\n## Decision\ny\n")

	var out bytes.Buffer
	if err := Run(nil, dir, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRun_AcceptsSequenceValuedSupersededBy(t *testing.T) {
	dir := t.TempDir()
	writeADR(t, dir, "0001-old.md",
		"---\nstatus: superseded\nsuperseded_by: [\"0002\", 3]\n---\n# 1. Old\n\n## Decision\nx\n")
	writeADR(t, dir, "0002-new-a.md", "---\nstatus: accepted\n---\n# 2. New A\n\n## Decision\ny\n")
	writeADR(t, dir, "0003-new-b.md", "---\nstatus: accepted\n---\n# 3. New B\n\n## Decision\nz\n")

	var out bytes.Buffer
	if err := Run(nil, dir, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRun_FlagsEveryDanglingSequenceSuccessor(t *testing.T) {
	dir := t.TempDir()
	writeADR(t, dir, "0001-old.md",
		"---\nstatus: superseded\nsuperseded_by: [\"0098\", 99]\n---\n# 1. Old\n\n## Decision\nx\n")

	var out bytes.Buffer
	err := Run(nil, dir, &out)
	if err == nil {
		t.Fatal("expected error for dangling successors")
	}
	for _, want := range []string{"0098", "0099"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing dangling successor %s: %v", want, err)
		}
	}
}

func TestRun_RejectsInvalidSequenceSuccessor(t *testing.T) {
	dir := t.TempDir()
	writeADR(t, dir, "0001-old.md",
		"---\nstatus: superseded\nsuperseded_by: [\"0002\", {id: \"0003\"}]\n---\n# 1. Old\n\n## Decision\nx\n")

	var out bytes.Buffer
	err := Run(nil, dir, &out)
	if err == nil || !strings.Contains(err.Error(), "malformed frontmatter") {
		t.Fatalf("expected malformed frontmatter error, got %v", err)
	}
}

func TestRun_RejectsSelfSuccessorCycle(t *testing.T) {
	dir := t.TempDir()
	writeADR(t, dir, "0001-self.md", "---\nstatus: superseded\nsuperseded_by: 1\n---\n# 1. Self\n\n## Decision\nx\n")
	var out bytes.Buffer
	err := Run(nil, dir, &out)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected successor cycle error, got %v", err)
	}
}

func TestRun_RejectsMultiNodeSuccessorCycle(t *testing.T) {
	dir := t.TempDir()
	writeADR(t, dir, "0001-a.md", "---\nstatus: superseded\nsuperseded_by: 2\n---\n# 1. A\n\n## Decision\nx\n")
	writeADR(t, dir, "0002-b.md", "---\nstatus: superseded\nsuperseded_by: 1\n---\n# 2. B\n\n## Decision\ny\n")
	var out bytes.Buffer
	err := Run(nil, dir, &out)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected successor cycle error, got %v", err)
	}
}

func TestRun_RejectsEmptyOrDuplicateSuccessors(t *testing.T) {
	for _, value := range []string{"[]", `""`, `[2, "0002"]`} {
		t.Run(value, func(t *testing.T) {
			dir := t.TempDir()
			writeADR(t, dir, "0001-old.md", "---\nstatus: superseded\nsuperseded_by: "+value+"\n---\n# 1. Old\n\n## Decision\nx\n")
			writeADR(t, dir, "0002-new.md", "---\nstatus: accepted\n---\n# 2. New\n\n## Decision\ny\n")
			var out bytes.Buffer
			if err := Run(nil, dir, &out); err == nil {
				t.Fatalf("expected %s to be rejected", value)
			}
		})
	}
}

func TestRun_RejectsExtraArgs(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	if err := Run([]string{"extra"}, dir, &out); err == nil {
		t.Fatal("expected error for extra args")
	}
}

func TestRun_FlagsSupersededWithoutTarget(t *testing.T) {
	dir := t.TempDir()
	writeADR(t, dir, "0001-orphan.md",
		"---\nstatus: superseded\n---\n# 1. Orphan\n\n## Decision\nx\n")

	var out bytes.Buffer
	err := Run(nil, dir, &out)
	if err == nil {
		t.Fatal("expected error for status=superseded with no superseded_by")
	}
	if !strings.Contains(err.Error(), "superseded_by") {
		t.Errorf("err should mention missing superseded_by; got %q", err.Error())
	}
}

func TestRun_FlagsDanglingSupersededBy(t *testing.T) {
	dir := t.TempDir()
	writeADR(t, dir, "0001-old.md",
		"---\nstatus: superseded\nsuperseded_by: \"0099\"\n---\n# 1. Old\n\n## Decision\nx\n")

	var out bytes.Buffer
	err := Run(nil, dir, &out)
	if err == nil {
		t.Fatal("expected error for dangling superseded_by")
	}
	if !strings.Contains(err.Error(), "0099") {
		t.Errorf("err should mention dangling target 0099; got %q", err.Error())
	}
}
