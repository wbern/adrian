package initcmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInit_DefaultScaffolding(t *testing.T) {
	dir := t.TempDir()

	var out bytes.Buffer
	err := RunWithRoot([]string{}, "", dir, &out)
	if err != nil {
		t.Fatalf("RunWithRoot failed: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "Initialized ADRian") {
		t.Errorf("expected Initialized ADRian in output, got: %s", output)
	}
	if !strings.Contains(output, "0001-record-architecture-decisions.md") {
		t.Errorf("expected ADR 0001 in output, got: %s", output)
	}

	// Verify ADR 0001
	adrFile := filepath.Join(dir, "doc", "adr", "0001-record-architecture-decisions.md")
	content, err := os.ReadFile(adrFile)
	if err != nil {
		t.Fatalf("failed to read %s: %v", adrFile, err)
	}
	if !strings.Contains(string(content), "Record architecture decisions") {
		t.Errorf("unexpected ADR content: %s", string(content))
	}
	if !strings.Contains(string(content), "complexity: ultralite") {
		t.Errorf("expected complexity: ultralite, got: %s", string(content))
	}

	// Verify .adrian.toml
	cfgFile := filepath.Join(dir, ".adrian.toml")
	cfgContent, err := os.ReadFile(cfgFile)
	if err != nil {
		t.Fatalf("failed to read %s: %v", cfgFile, err)
	}
	if !strings.Contains(string(cfgContent), "default_profile = \"cloud-standard\"") {
		t.Errorf("unexpected config content: %s", string(cfgContent))
	}

	// Verify .adrian/weights.json
	weightsFile := filepath.Join(dir, ".adrian", "weights.json")
	wContent, err := os.ReadFile(weightsFile)
	if err != nil {
		t.Fatalf("failed to read %s: %v", weightsFile, err)
	}
	if !strings.Contains(string(wContent), "\"weights\": {}") {
		t.Errorf("unexpected weights content: %s", string(wContent))
	}

	// Test idempotent re-run without --force
	var out2 bytes.Buffer
	err = RunWithRoot([]string{}, "", dir, &out2)
	if err != nil {
		t.Fatalf("second run failed: %v", err)
	}
	if !strings.Contains(out2.String(), "all configuration and ADR files already present") {
		t.Errorf("expected already present message, got: %s", out2.String())
	}
}

func TestInit_CustomDocsDir(t *testing.T) {
	dir := t.TempDir()

	var out bytes.Buffer
	err := RunWithRoot([]string{"--docs-dir", "docs/architecture"}, "", dir, &out)
	if err != nil {
		t.Fatalf("RunWithRoot failed: %v", err)
	}

	adrFile := filepath.Join(dir, "docs", "architecture", "0001-record-architecture-decisions.md")
	if _, err := os.Stat(adrFile); err != nil {
		t.Fatalf("expected custom docs dir file to exist: %v", err)
	}
}

func TestInit_GithubActionAndGitignore(t *testing.T) {
	dir := t.TempDir()

	// Create initial .gitignore
	giFile := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(giFile, []byte("node_modules/\ndist/\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := RunWithRoot([]string{"--github-action"}, "", dir, &out)
	if err != nil {
		t.Fatalf("RunWithRoot failed: %v", err)
	}

	// Verify workflow file
	wfFile := filepath.Join(dir, ".github", "workflows", "adrian.yml")
	wfContent, err := os.ReadFile(wfFile)
	if err != nil {
		t.Fatalf("failed to read %s: %v", wfFile, err)
	}
	if !strings.Contains(string(wfContent), "adrian validate") {
		t.Errorf("unexpected workflow content: %s", string(wfContent))
	}

	// Verify .gitignore updated
	giBytes, err := os.ReadFile(giFile)
	if err != nil {
		t.Fatalf("failed to read .gitignore: %v", err)
	}
	if !strings.Contains(string(giBytes), ".adrian/cache/") {
		t.Errorf("expected .adrian/cache/ in .gitignore, got: %s", string(giBytes))
	}
}
