package resolvecmd

import (
	"bufio"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var (
	h2Re         = regexp.MustCompile(`^##\s+(.+?)\s*$`)
	h3Re         = regexp.MustCompile(`^###\s+(\d+)\.\s*(.+?)\s*$`)
	nonWordRe    = regexp.MustCompile(`[^\w\- ]+`)
	multiSpaceRe = regexp.MustCompile(`\s+`)
)

// HeadingAnchor implements GitHub's heading anchor derivation:
// lowercase, drop non-alphanumeric/non-hyphen/non-space, trim, convert spaces to hyphens.
func HeadingAnchor(heading string) string {
	s := strings.ToLower(heading)
	s = nonWordRe.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	return multiSpaceRe.ReplaceAllString(s, "-")
}

// ParseLenses extracts lens rules from the ## Review Lenses section of code-review.md.
func ParseLenses(filePath string) ([]LensRule, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var (
		lenses   []LensRule
		inLenses bool
		scanner  = bufio.NewScanner(f)
	)

	for scanner.Scan() {
		line := scanner.Text()
		if m := h2Re.FindStringSubmatch(line); m != nil {
			inLenses = strings.EqualFold(strings.TrimSpace(m[1]), "review lenses")
			continue
		}
		if !inLenses {
			continue
		}
		if m := h3Re.FindStringSubmatch(line); m != nil {
			num, _ := strconv.Atoi(m[1])
			title := strings.TrimSpace(m[2])
			lenses = append(lenses, LensRule{
				ID:     "lens#" + m[1],
				Number: num,
				Title:  title,
				Anchor: HeadingAnchor(m[1] + ". " + title),
			})
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return lenses, nil
}
