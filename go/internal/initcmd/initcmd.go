// Package initcmd implements the `adrian init` subcommand, scaffolding
// the ADR directory, default decision record, configuration, and finding weights.
package initcmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wbern/adrian/go/internal/gitcontext"
)

const Usage = "adrian init [--docs-dir <path>] [--github-action] [--force]"

// Options configures the scaffolding behavior.
type Options struct {
	DocsDir      string
	GithubAction bool
	Force        bool
}

// Run scaffolds ADRian in the current repository.
func Run(args []string, adrDir string, out io.Writer) error {
	git := gitcontext.NewDefaultClient()
	repoRoot := git.GitRoot()
	return RunWithRoot(args, adrDir, repoRoot, out)
}

// RunWithRoot scaffolds ADRian with an explicit repository root.
func RunWithRoot(args []string, adrDir, repoRoot string, out io.Writer) error {
	opts, err := parseArgs(args)
	if err != nil {
		return err
	}

	if repoRoot == "" {
		repoRoot, _ = os.Getwd()
	}

	targetDocsDir := opts.DocsDir
	if targetDocsDir == "" {
		// Prefer existing docs/adr if present, otherwise default to doc/adr
		if fi, err := os.Stat(filepath.Join(repoRoot, "docs", "adr")); err == nil && fi.IsDir() {
			targetDocsDir = filepath.Join("docs", "adr")
		} else {
			targetDocsDir = filepath.Join("doc", "adr")
		}
	}
	absDocsDir := filepath.Join(repoRoot, targetDocsDir)

	var createdFiles []string

	// 1. Create docs directory
	if err := os.MkdirAll(absDocsDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", targetDocsDir, err)
	}

	// 2. Scaffold 0001-record-architecture-decisions.md
	adr1Path := filepath.Join(absDocsDir, "0001-record-architecture-decisions.md")
	if shouldWrite(adr1Path, opts.Force) {
		today := time.Now().UTC().Format("2006-01-02")
		adrContent := fmt.Sprintf(`---
status: accepted
date: %s
complexity: ultralite
applies_to:
  - "**/*"
---

# 1. Record architecture decisions

## Context
We need to record architectural decisions made on this project.

## Decision
We will use Architecture Decision Records, as described by Michael Nygard in this article:
http://thinkrelevance.com/blog/2011/11/15/documenting-architecture-decisions

We will use ADRian to lint, validate, and enforce these decisions during code review.

## Consequences
See Michael Nygard's article, linked above.
`, today)
		if err := os.WriteFile(adr1Path, []byte(adrContent), 0644); err != nil {
			return fmt.Errorf("failed to create %s: %w", adr1Path, err)
		}
		relPath, _ := filepath.Rel(repoRoot, adr1Path)
		createdFiles = append(createdFiles, relPath)
	}

	// 3. Scaffold .adrian.toml
	configPath := filepath.Join(repoRoot, ".adrian.toml")
	if shouldWrite(configPath, opts.Force) {
		configContent := `# ADRian Configuration
# See: https://github.com/wbern/adrian

version = 1

[budget]
mode = "auto"
default_profile = "cloud-standard"
fanout_threshold_bytes = 250000
fanout_threshold_segments = 2
batch_max_bytes = 200000
batch_max_segments = 4
max_batches = 32
safety_margin = 0.20
`
		if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
			return fmt.Errorf("failed to create %s: %w", configPath, err)
		}
		createdFiles = append(createdFiles, ".adrian.toml")
	}

	// 4. Scaffold .adrian/weights.json
	adrianDir := filepath.Join(repoRoot, ".adrian")
	if err := os.MkdirAll(adrianDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory .adrian: %w", err)
	}
	weightsPath := filepath.Join(adrianDir, "weights.json")
	if shouldWrite(weightsPath, opts.Force) {
		weightsContent := "{\n  \"version\": 1,\n  \"weights\": {}\n}\n"
		if err := os.WriteFile(weightsPath, []byte(weightsContent), 0644); err != nil {
			return fmt.Errorf("failed to create %s: %w", weightsPath, err)
		}
		createdFiles = append(createdFiles, ".adrian/weights.json")
	}

	// 5. Update .gitignore if present to ignore .adrian/cache/
	gitignorePath := filepath.Join(repoRoot, ".gitignore")
	if giBytes, err := os.ReadFile(gitignorePath); err == nil {
		giContent := string(giBytes)
		if !strings.Contains(giContent, ".adrian/cache") {
			addition := "\n# ADRian local cache\n.adrian/cache/\n"
			if err := os.WriteFile(gitignorePath, []byte(giContent+addition), 0644); err == nil {
				createdFiles = append(createdFiles, ".gitignore (updated)")
			}
		}
	}

	// 6. Scaffold GitHub Action if requested
	if opts.GithubAction {
		workflowDir := filepath.Join(repoRoot, ".github", "workflows")
		if err := os.MkdirAll(workflowDir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", workflowDir, err)
		}
		workflowPath := filepath.Join(workflowDir, "adrian.yml")
		if shouldWrite(workflowPath, opts.Force) {
			workflowContent := `name: ADRian Architecture Gate

on:
  pull_request:
    branches: [main]

jobs:
  adrian:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.24'

      - name: Install ADRian
        run: go install github.com/wbern/adrian/go/cmd/adrian@latest

      - name: Validate ADRs
        run: adrian validate

      - name: Audit ADRs
        run: adrian audit --strict

      - name: Frontload Review Rules
        run: adrian resolve --ranked --format pr-block
`
			if err := os.WriteFile(workflowPath, []byte(workflowContent), 0644); err != nil {
				return fmt.Errorf("failed to create %s: %w", workflowPath, err)
			}
			relWorkflow, _ := filepath.Rel(repoRoot, workflowPath)
			createdFiles = append(createdFiles, relWorkflow)
		}
	}

	// Print summary
	fmt.Fprintf(out, "Initialized ADRian in %s:\n", repoRoot)
	if len(createdFiles) == 0 {
		fmt.Fprintln(out, "  (all configuration and ADR files already present)")
	} else {
		for _, f := range createdFiles {
			fmt.Fprintf(out, "  ✓ Created %s\n", f)
		}
	}
	fmt.Fprintln(out, "\nRun `adrian validate` to verify your setup.")

	return nil
}

func parseArgs(args []string) (Options, error) {
	var opts Options
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--docs-dir":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--docs-dir requires a path; usage: %s", Usage)
			}
			i++
			opts.DocsDir = args[i]
		case "--github-action", "--ci":
			opts.GithubAction = true
		case "--force", "-f":
			opts.Force = true
		default:
			return opts, fmt.Errorf("unknown argument %q; usage: %s", arg, Usage)
		}
	}
	return opts, nil
}

func shouldWrite(path string, force bool) bool {
	if force {
		return true
	}
	_, err := os.Stat(path)
	return os.IsNotExist(err)
}
