package diffstats

import (
	"strconv"
	"strings"
)

// FileDiff is one file's section of a unified diff, with its exact original
// bytes preserved in Text.
//
// Text matters as much as the paths. When a diff is supplied from outside git
// (adr-lint --diff), each ADR is handed only the sections for the files its
// applies_to globs match, sliced out of the supplied bytes. Concatenating the
// Texts of every FileDiff reproduces the input exactly (from the first header
// onward), so "the reviewer saw these bytes" stays a checkable claim rather
// than an assertion.
type FileDiff struct {
	OldPath string
	NewPath string
	Text    string
}

// Split cuts a unified diff into per-file sections.
//
// Paths come from the `--- a/…` / `+++ b/…` lines and `rename from|to` in
// preference to the `diff --git` header. The header line is genuinely
// ambiguous — `diff --git a/pkg/w b/x.go b/pkg/w b/x.go` cannot be cut
// correctly by any regex, because " b/" appears inside the path — whereas the
// marker lines carry one path each. The header is the last resort.
//
// A delete has `+++ /dev/null`, and an add has `--- /dev/null`; both fall back
// to the side that exists, so a deleted file still matches its ADRs. A rename
// retains both endpoints so applies_to can bind the path being left as well as
// the destination.
//
// Anything before the first `diff --git` header is dropped. If a header's paths
// cannot be resolved at all, the section is still returned with empty paths so
// the caller can refuse loudly rather than silently review nothing.
func Split(diff string) []FileDiff {
	if strings.TrimSpace(diff) == "" {
		return nil
	}

	starts := headerOffsets(diff)
	if len(starts) == 0 {
		return nil
	}

	files := make([]FileDiff, 0, len(starts))
	for i, start := range starts {
		end := len(diff)
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		text := diff[start:end]
		old, new := pathsFrom(text)
		files = append(files, FileDiff{OldPath: old, NewPath: new, Text: text})
	}
	return files
}

// headerOffsets returns the byte offset of every `diff --git ` that starts a
// line. Offsets rather than split-and-rejoin: rejoining on "\n" cannot tell a
// missing trailing newline from a present one, and that one byte is the
// difference between a faithful slice and a corrupted one.
func headerOffsets(diff string) []int {
	const marker = "diff --git "
	var offsets []int
	for i := 0; i < len(diff); {
		idx := strings.Index(diff[i:], marker)
		if idx < 0 {
			break
		}
		abs := i + idx
		if abs == 0 || diff[abs-1] == '\n' {
			offsets = append(offsets, abs)
		}
		i = abs + len(marker)
	}
	return offsets
}

// pathsFrom resolves one file section's old and new paths.
func pathsFrom(section string) (oldPath, newPath string) {
	var renameFrom, renameTo string

	for _, line := range strings.Split(section, "\n") {
		switch {
		case strings.HasPrefix(line, "--- "):
			if p, ok := stripPrefixPath(line[4:], "a/"); ok {
				oldPath = p
			}
		case strings.HasPrefix(line, "+++ "):
			if p, ok := stripPrefixPath(line[4:], "b/"); ok {
				newPath = p
			}
		case strings.HasPrefix(line, "rename from "):
			renameFrom = unquotePath(strings.TrimSpace(line[len("rename from "):]))
		case strings.HasPrefix(line, "rename to "):
			renameTo = unquotePath(strings.TrimSpace(line[len("rename to "):]))
		case strings.HasPrefix(line, "@@"):
			// Hunks begin; every path marker that exists has been seen.
			goto done
		}
	}
done:

	// A pure rename with no content change has no --- / +++ lines at all.
	if oldPath == "" && renameFrom != "" {
		oldPath = renameFrom
	}
	if newPath == "" && renameTo != "" {
		newPath = renameTo
	}

	// /dev/null on one side: fall back to the side that exists, so adds and
	// deletes still match their ADRs.
	if newPath == "" {
		newPath = oldPath
	}
	if oldPath == "" {
		oldPath = newPath
	}

	if oldPath == "" && newPath == "" {
		oldPath, newPath = pathsFromHeader(section)
	}
	return oldPath, newPath
}

// stripPrefixPath handles the `a/`-or-`b/`-prefixed operand of a --- / +++
// line, rejecting /dev/null. Reports false when the operand is not a real path.
func stripPrefixPath(operand, prefix string) (string, bool) {
	operand = strings.TrimSpace(operand)
	// A tab separates the path from any trailing timestamp git may add.
	if tab := strings.IndexByte(operand, '\t'); tab >= 0 {
		operand = operand[:tab]
	}
	if operand == "/dev/null" || operand == "" {
		return "", false
	}
	operand = unquotePath(operand)
	if operand == "/dev/null" {
		return "", false
	}
	return strings.TrimPrefix(operand, prefix), true
}

// unquotePath undoes git's C-style quoting of paths with spaces or non-ASCII
// bytes. A path that is not quoted is returned unchanged.
func unquotePath(p string) string {
	if len(p) < 2 || p[0] != '"' || p[len(p)-1] != '"' {
		return p
	}
	if unquoted, err := strconv.Unquote(p); err == nil {
		return unquoted
	}
	return p
}

// pathsFromHeader is the last resort: the ambiguous `diff --git a/X b/Y` line.
// Only reached when a section carries no marker lines at all.
func pathsFromHeader(section string) (string, string) {
	line := section
	if nl := strings.IndexByte(line, '\n'); nl >= 0 {
		line = line[:nl]
	}
	m := diffHeaderRe.FindStringSubmatch(line)
	if m == nil {
		return "", ""
	}
	p := unquotePath(m[1])
	return p, p
}
