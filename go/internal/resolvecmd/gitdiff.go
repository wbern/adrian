package resolvecmd

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// GitRunner abstracts git command execution for testing.
type GitRunner interface {
	RunGit(args ...string) ([]byte, error)
}

// OSGitRunner executes git in a target working directory.
type OSGitRunner struct {
	Dir string
}

func (g OSGitRunner) RunGit(args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	if g.Dir != "" {
		cmd.Dir = g.Dir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// PreferFresherBase inspects local vs remote tracking ref.
// If the local base is strictly behind its remote counterpart (e.g. local main
// behind origin/main), it returns origin/base with switched=true.
func PreferFresherBase(base string, runner GitRunner, logErr io.Writer) (string, bool) {
	if strings.Contains(base, "/") {
		return base, false
	}
	remote := fmt.Sprintf("origin/%s", base)

	// Check if remote ref exists
	if _, err := runner.RunGit("rev-parse", "--verify", "--quiet", remote+"^{commit}"); err != nil {
		return base, false
	}

	// Strictly behind: local is an ancestor of remote, and remote is NOT an ancestor of local.
	if _, err := runner.RunGit("merge-base", "--is-ancestor", base, remote); err != nil {
		return base, false
	}
	if _, err := runner.RunGit("merge-base", "--is-ancestor", remote, base); err == nil {
		// Remote is an ancestor of local, so they are equal or local is ahead.
		return base, false
	}

	if logErr != nil {
		_, _ = fmt.Fprintf(logErr, "adrian resolve: local %s is behind %s; diffing against %s instead.\n", base, remote, remote)
	}
	return remote, true
}

// GetChangedFiles extracts the target file list based on CLI arguments.
func GetChangedFiles(explicitFiles []string, staged bool, base string, runner GitRunner, logErr io.Writer) ([]string, error) {
	if len(explicitFiles) > 0 {
		var out []string
		for _, f := range explicitFiles {
			normalized := strings.TrimSpace(strings.ReplaceAll(f, "\\", "/"))
			if normalized != "" {
				out = append(out, normalized)
			}
		}
		return out, nil
	}

	if staged {
		out, err := runner.RunGit("diff", "--name-only", "--cached")
		if err != nil {
			return nil, err
		}
		return splitLines(out), nil
	}

	if base != "" {
		effectiveBase, _ := PreferFresherBase(base, runner, logErr)
		tripleDot := fmt.Sprintf("%s...HEAD", effectiveBase)
		out, err := runner.RunGit("diff", "--name-only", tripleDot)
		if err != nil {
			return nil, err
		}
		return splitLines(out), nil
	}

	return nil, nil
}

func splitLines(data []byte) []string {
	lines := strings.Split(string(data), "\n")
	var out []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(strings.ReplaceAll(l, "\\", "/"))
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
