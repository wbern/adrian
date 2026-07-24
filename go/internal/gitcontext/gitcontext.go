// Package gitcontext provides helpers for reading staged and
// branch-relative diffs from a git working tree. The git shell-out is
// abstracted behind a Runner function so tests can inject
// deterministic responses without touching the real git binary.
package gitcontext

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Runner shells out to git and returns its combined stdout. Callers
// fall back to an empty result on error.
type Runner func(args []string) (string, error)

// Client carries the git Runner and a memoized git root.
type Client struct {
	run     Runner
	gitRoot string
	hasRoot bool
}

// NewClient builds a Client backed by run.
func NewClient(run Runner) *Client {
	return &Client{run: run}
}

// NewDefaultClient builds a Client that shells out to `git` via
// os/exec from the resolved git root (falling back to the current
// working directory if `git rev-parse --show-toplevel` fails).
func NewDefaultClient() *Client {
	c := &Client{}
	c.run = func(args []string) (string, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = c.resolveGitRoot()
		out, err := cmd.Output()
		return string(out), err
	}
	return c
}

// SetGitRoot overrides the resolved git root. Intended for tests that
// inject a Runner and want to control the working directory without
// touching the real git binary.
func (c *Client) SetGitRoot(root string) {
	c.gitRoot = root
	c.hasRoot = true
}

// GitRoot returns the resolved repository root, falling back to the
// current working directory when `git rev-parse --show-toplevel` fails.
func (c *Client) GitRoot() string {
	return c.resolveGitRoot()
}

func (c *Client) resolveGitRoot() string {
	if c.hasRoot {
		return c.gitRoot
	}
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		wd, _ := os.Getwd()
		c.gitRoot = wd
	} else {
		c.gitRoot = strings.TrimSpace(string(out))
	}
	c.hasRoot = true
	return c.gitRoot
}

func (c *Client) getDiffRange(targetRef string) (string, error) {
	baseSha := os.Getenv("BASE_SHA")
	headSha := os.Getenv("HEAD_SHA")
	if baseSha != "" && headSha != "" {
		return baseSha + "..." + headSha, nil
	}
	ref := targetRef
	if ref == "" {
		ref = "HEAD"
	}
	base, err := c.getMergeBase(ref)
	if err != nil {
		return "", err
	}
	return base + ".." + ref, nil
}

func (c *Client) getMergeBase(ref string) (string, error) {
	for _, branch := range []string{"main", "origin/main", "master", "origin/master"} {
		out, err := c.run([]string{"merge-base", ref, branch})
		if err == nil {
			return strings.TrimSpace(out), nil
		}
	}
	return "", errors.New("could not find merge base with main or master")
}

// GetDiffAgainstMainForFiles returns `git diff <range> -- files` where
// <range> is BASE_SHA...HEAD_SHA from CI env, or merge-base..ref
// otherwise. Empty string on any failure (no main/master branch).
func (c *Client) GetDiffAgainstMainForFiles(files []string, targetRef string, includeContext bool) string {
	if len(files) == 0 {
		return ""
	}
	diffRange, err := c.getDiffRange(targetRef)
	if err != nil {
		return ""
	}
	flag := "-W"
	if !includeContext {
		flag = "-U0"
	}
	args := append([]string{"diff", flag, diffRange, "--"}, files...)
	out, err := c.run(args)
	if err != nil {
		return ""
	}
	return out
}

// GetDiffAgainstMainForFilesWithContextLines returns a unified diff with a
// bounded number of surrounding lines per hunk. It avoids function-wide
// context when a downstream reviewer has a strict context budget.
func (c *Client) GetDiffAgainstMainForFilesWithContextLines(files []string, targetRef string, contextLines int) string {
	if len(files) == 0 || contextLines < 0 {
		return ""
	}
	diffRange, err := c.getDiffRange(targetRef)
	if err != nil {
		return ""
	}
	args := append([]string{"diff", fmt.Sprintf("-U%d", contextLines), diffRange, "--"}, files...)
	out, err := c.run(args)
	if err != nil {
		return ""
	}
	return out
}

// GetStagedFiles returns the names of currently-staged files.
func (c *Client) GetStagedFiles() []string {
	out, err := c.run([]string{"diff", "--cached", "--name-only"})
	if err != nil {
		return nil
	}
	return parseLines(out)
}

// GetFilesChangedAgainstMain returns names of files changed against
// the merge-base with main/master (or BASE..HEAD when CI sets them).
func (c *Client) GetFilesChangedAgainstMain(targetRef string) []string {
	diffRange, err := c.getDiffRange(targetRef)
	if err != nil {
		return nil
	}
	out, err := c.run([]string{"diff", "--name-only", diffRange})
	if err != nil {
		return nil
	}
	return parseLines(out)
}

func parseLines(s string) []string {
	parts := strings.Split(s, "\n")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// GetStagedDiffForFiles returns `git diff --cached` output for files.
// includeContext picks between -W (function-level) and -U0 (none).
func (c *Client) GetStagedDiffForFiles(files []string, includeContext bool) string {
	if len(files) == 0 {
		return ""
	}
	flag := "-W"
	if !includeContext {
		flag = "-U0"
	}
	args := append([]string{"diff", "--cached", flag, "--"}, files...)
	out, err := c.run(args)
	if err != nil {
		return ""
	}
	return out
}

// GetStagedDiffForFilesWithContextLines returns a staged unified diff with a
// bounded number of surrounding lines per hunk.
func (c *Client) GetStagedDiffForFilesWithContextLines(files []string, contextLines int) string {
	if len(files) == 0 || contextLines < 0 {
		return ""
	}
	args := append([]string{"diff", "--cached", fmt.Sprintf("-U%d", contextLines), "--"}, files...)
	out, err := c.run(args)
	if err != nil {
		return ""
	}
	return out
}
