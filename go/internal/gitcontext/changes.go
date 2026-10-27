package gitcontext

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Change keeps a rename's binding paths separate from its destination. An add
// has no old path and a deletion has no new path; neither fabricates source.
type Change struct {
	Status  string
	OldPath string
	NewPath string
}

var changeFlags = []string{"--no-relative", "--no-color", "--no-ext-diff", "--no-textconv", "--ignore-submodules=none", "--name-status", "-z", "--find-renames"}

func (c *Client) GetStagedChanges() ([]Change, error) {
	args := append([]string{"diff", "--cached"}, changeFlags...)
	raw, err := c.run(args)
	if err != nil {
		return nil, fmt.Errorf("query staged change manifest: %w", err)
	}
	return parseChangeManifest(raw)
}
func (c *Client) GetChangesAgainstMain(targetRef string) ([]Change, error) {
	r, err := c.getDiffRange(targetRef)
	if err != nil {
		return nil, err
	}
	args := append([]string{"diff"}, changeFlags...)
	args = append(args, r)
	raw, err := c.run(args)
	if err != nil {
		return nil, fmt.Errorf("query branch change manifest: %w", err)
	}
	return parseChangeManifest(raw)
}
func manifestPath(p string) bool {
	if p == "" || !utf8.ValidString(p) || strings.HasPrefix(p, "/") {
		return false
	}
	for _, component := range strings.Split(p, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
	}
	return true
}
func parseChangeManifest(raw string) ([]Change, error) {
	out := []Change{}
	if raw == "" {
		return out, nil
	}
	if raw[len(raw)-1] != 0 {
		return nil, fmt.Errorf("incomplete NUL change manifest")
	}
	parts := strings.Split(raw[:len(raw)-1], "\x00")
	seen := map[string]bool{}
	for i := 0; i < len(parts); {
		status := parts[i]
		i++
		count := 1
		switch status {
		case "A", "D", "M", "T":
		default:
			if len(status) < 2 || (status[0] != 'R' && status[0] != 'C') {
				return nil, fmt.Errorf("invalid change status %q", status)
			}
			for _, digit := range status[1:] {
				if digit < '0' || digit > '9' {
					return nil, fmt.Errorf("invalid rename score %q", status)
				}
			}
			score, err := strconv.Atoi(status[1:])
			if err != nil || score < 0 || score > 100 {
				return nil, fmt.Errorf("invalid rename score %q", status)
			}
			count = 2
		}
		if i+count > len(parts) {
			return nil, fmt.Errorf("incomplete change record")
		}
		first := parts[i]
		last := parts[i+count-1]
		i += count
		if !manifestPath(first) || !manifestPath(last) {
			return nil, fmt.Errorf("invalid repository-relative change path")
		}
		c := Change{Status: status}
		switch {
		case count == 2:
			c.OldPath = first
			c.NewPath = last
		case status == "A":
			c.NewPath = first
		case status == "D":
			c.OldPath = first
		default:
			c.OldPath = first
			c.NewPath = first
		}
		destination := c.NewPath
		if destination == "" {
			destination = c.OldPath
		}
		if seen[destination] {
			return nil, fmt.Errorf("duplicate change path %q", destination)
		}
		seen[destination] = true
		out = append(out, c)
	}
	return out, nil
}

// ChangedPaths returns every real endpoint once, including a removed old path.
func ChangedPaths(changes []Change) []string {
	seen := map[string]bool{}
	for _, c := range changes {
		for _, p := range []string{c.OldPath, c.NewPath} {
			if p != "" {
				seen[p] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// ExpandChangePaths fetches whole selected change identities. Matching uses the
// original selection only, so one copy destination does not select other copies.
func ExpandChangePaths(changes []Change, selected []string) []string {
	wanted := map[string]bool{}
	out := map[string]bool{}
	for _, p := range selected {
		wanted[p] = true
		out[p] = true
	}
	for _, c := range changes {
		if (c.OldPath != "" && wanted[c.OldPath]) || (c.NewPath != "" && wanted[c.NewPath]) {
			if c.OldPath != "" {
				out[c.OldPath] = true
			}
			if c.NewPath != "" {
				out[c.NewPath] = true
			}
		}
	}
	paths := make([]string, 0, len(out))
	for p := range out {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}
func literalPaths(files []string) []string {
	out := make([]string, len(files))
	for i, p := range files {
		out[i] = ":(literal)" + p
	}
	return out
}
func (c *Client) ReadStagedDiffForFiles(files []string, includeContext bool) (string, error) {
	return c.readSelectedDiff(files, "", true, includeContext)
}
func (c *Client) ReadDiffAgainstMainForFiles(files []string, targetRef string, includeContext bool) (string, error) {
	r, err := c.getDiffRange(targetRef)
	if err != nil {
		return "", err
	}
	return c.readSelectedDiff(files, r, false, includeContext)
}
func (c *Client) readSelectedDiff(files []string, r string, staged, includeContext bool) (string, error) {
	if len(files) == 0 {
		return "", fmt.Errorf("no selected changed paths")
	}
	flag := "-W"
	if !includeContext {
		flag = "-U0"
	}
	args := []string{"diff"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, flag, "--no-relative", "--no-color", "--no-ext-diff", "--no-textconv", "--ignore-submodules=none", "--find-renames")
	if r != "" {
		args = append(args, r)
	}
	args = append(args, "--")
	args = append(args, literalPaths(files)...)
	raw, err := c.run(args)
	if err != nil {
		return "", fmt.Errorf("query selected diff: %w", err)
	}
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("selected change manifest produced an empty diff")
	}
	return raw, nil
}
