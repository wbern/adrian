package reviewpolicy

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// NormalizeTag validates before trimming so whitespace control characters cannot
// disappear from routing metadata. Case and Unicode code points are preserved.
func NormalizeTag(label string) (string, error) {
	if !utf8.ValidString(label) {
		return "", fmt.Errorf("tag must be valid UTF-8")
	}
	for _, r := range label {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("tag contains a control character")
		}
	}
	label = strings.TrimSpace(label)
	if label == "" {
		return "", fmt.Errorf("tag must not be empty or whitespace")
	}
	return label, nil
}

// ParseTags is strict metadata validation for opt-in tagged bindings only.
// Existing plan, code-plan and unfiltered bindings retain their parser contract.
func ParseTags(raw []byte) ([]string, error) {
	raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(raw, []byte("---\n")) {
		return nil, fmt.Errorf("YAML frontmatter is required")
	}
	end := bytes.Index(raw[4:], []byte("\n---\n"))
	if end < 0 {
		return nil, fmt.Errorf("unterminated YAML frontmatter")
	}
	n, err := document(raw[4 : 4+end])
	if err != nil {
		return nil, err
	}
	tags := value(n, "tags")
	result := []string{}
	if tags == nil {
		return result, nil
	}
	entries := []*yaml.Node{tags}
	if tags.Kind == yaml.SequenceNode {
		entries = tags.Content
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.Kind != yaml.ScalarNode || entry.Tag != "!!str" {
			return nil, fmt.Errorf("tags must be a string or sequence of strings")
		}
		label, e := NormalizeTag(entry.Value)
		if e != nil {
			return nil, e
		}
		if !seen[label] {
			result = append(result, label)
			seen[label] = true
		}
	}
	return result, nil
}
