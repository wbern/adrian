package runner

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/wbern/adrian/go/internal/diffstats"
)

// readSuppliedDiff loads a unified diff handed to us from outside git — a PR
// diff, typically — and cuts it into per-file sections.
//
// Both failure modes here are errors rather than empty results, deliberately.
// adr-lint is used as a gate, and a gate that cannot tell "I read nothing"
// from "there was nothing to read" reports success for an input it never saw.
func readSuppliedDiff(path string, in io.Reader) ([]diffstats.FileDiff, error) {
	var (
		raw []byte
		err error
	)
	if path == "-" {
		if in == nil {
			in = os.Stdin
		}
		raw, err = io.ReadAll(in)
		if err != nil {
			return nil, fmt.Errorf("reading diff from stdin: %w", err)
		}
	} else {
		raw, err = os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading diff from %s: %w", path, err)
		}
	}

	diff := string(raw)
	if strings.TrimSpace(diff) == "" {
		return nil, fmt.Errorf("supplied diff is empty: nothing to check")
	}

	files := diffstats.Split(diff)
	if len(files) == 0 {
		return nil, fmt.Errorf("supplied diff has %d bytes but no `diff --git` file headers: cannot determine which files changed", len(raw))
	}
	return files, nil
}

// changedFilesFromDiff derives the changed-file list from the diff's own
// headers, since there is no git to ask.
//
// A section whose paths could not be resolved is an error, not a silent drop:
// its bytes would otherwise be reviewed against no ADR at all while the run
// still reported success.
func changedFilesFromDiff(files []diffstats.FileDiff) ([]string, *int, error) {
	seen := make(map[string]struct{}, len(files))
	paths := make([]string, 0, len(files))
	for _, f := range files {
		p := f.NewPath
		if p == "" {
			p = f.OldPath
		}
		if p == "" {
			return nil, nil, fmt.Errorf("supplied diff contains a file section whose path could not be resolved: %s",
				firstLine(f.Text))
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths, nil, nil
}

// sliceSuppliedDiff returns the sections of the supplied diff belonging to the
// given files, in their original order and with their original bytes.
//
// This is what keeps a per-ADR check cheap: an ADR governing one path is shown
// that path's hunks and nothing else, exactly as branch mode would have given
// it. The bytes are never re-rendered, so the slice is always a verbatim
// substring of what the caller supplied.
func sliceSuppliedDiff(files []diffstats.FileDiff, want []string) string {
	if len(want) == 0 {
		return ""
	}
	wanted := make(map[string]struct{}, len(want))
	for _, w := range want {
		wanted[w] = struct{}{}
	}

	var b strings.Builder
	for _, f := range files {
		p := f.NewPath
		if p == "" {
			p = f.OldPath
		}
		if _, ok := wanted[p]; ok {
			b.WriteString(f.Text)
		}
	}
	return b.String()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
