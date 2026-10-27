// Package corpus selects one repository-owned ADR directory for every consumer.
package corpus

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/pelletier/go-toml/v2"
)

const ConfigPath = ".adrian.toml"

var Numbered = regexp.MustCompile(`^[0-9]{4}-.+\.md$`)

// ConfigDir parses the complete TOML document while keeping unrelated settings.
func ConfigDir(raw []byte) (string, error) {
	var doc map[string]any
	if err := toml.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("corpus configuration: %w", err)
	}
	value, ok := doc["corpus"]
	if !ok {
		return "", nil
	}
	table, ok := value.(map[string]any)
	if !ok {
		return "", fmt.Errorf("corpus configuration must be a table")
	}
	if len(table) != 1 {
		return "", fmt.Errorf("corpus configuration requires only dir")
	}
	dir, ok := table["dir"].(string)
	if !ok {
		return "", fmt.Errorf("corpus dir must be a string")
	}
	if err := ValidatePath(dir); err != nil {
		return "", err
	}
	return dir, nil
}
func ValidatePath(dir string) error {
	if dir == "" || strings.Contains(dir, "\\") || path.IsAbs(dir) || strings.ContainsRune(dir, ':') {
		return fmt.Errorf("invalid corpus dir %q: use a canonical repository-relative slash path", dir)
	}
	for _, r := range dir {
		if unicode.IsControl(r) {
			return fmt.Errorf("invalid corpus dir: control character")
		}
	}
	for _, p := range strings.Split(dir, "/") {
		if p == "" || p == "." || p == ".." {
			return fmt.Errorf("invalid corpus dir %q", dir)
		}
	}
	return nil
}

// Select uses the same precedence for a working tree and an immutable Git tree.
func Select(configured string, exists func(string) (bool, error)) (string, error) {
	if configured != "" {
		return configured, nil
	}
	found := ""
	for _, p := range []string{"doc/adr", "docs/adr"} {
		ok, err := exists(p)
		if err != nil {
			return "", err
		}
		if ok {
			if found != "" {
				return "", fmt.Errorf("ambiguous corpus: doc/adr and docs/adr both exist; configure [corpus] dir")
			}
			found = p
		}
	}
	if found == "" {
		return "", fmt.Errorf("ADR corpus missing: run adrian init or configure [corpus] dir")
	}
	return found, nil
}

// NoSymlinks checks every existing component, including the final path.
func NoSymlinks(root, rel string) error {
	current := root
	for _, part := range strings.Split(rel, "/") {
		current = filepath.Join(current, part)
		fi, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("corpus path %s must not contain symlinks", rel)
		}
	}
	return nil
}
func Discover(root string, bootstrap bool) (string, error) {
	if err := NoSymlinks(root, ConfigPath); err != nil {
		return "", err
	}
	raw, err := os.ReadFile(filepath.Join(root, ConfigPath))
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	configured := ""
	if err == nil {
		configured, err = ConfigDir(raw)
		if err != nil {
			return "", err
		}
	}
	selected, err := Select(configured, func(p string) (bool, error) {
		if err := NoSymlinks(root, p); err != nil {
			return false, err
		}
		fi, e := os.Stat(filepath.Join(root, filepath.FromSlash(p)))
		if os.IsNotExist(e) {
			return false, nil
		}
		if e != nil {
			return false, e
		}
		return fi.IsDir(), nil
	})
	if err != nil {
		if bootstrap && configured == "" && strings.Contains(err.Error(), "corpus missing") {
			selected = "doc/adr"
		} else {
			return "", err
		}
	}
	if err := NoSymlinks(root, selected); err != nil {
		return "", err
	}
	dir := filepath.Join(root, filepath.FromSlash(selected))
	if !bootstrap {
		if err := Require(dir); err != nil {
			return "", err
		}
	}
	return dir, nil
}
func Require(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("ADR corpus %s: %w", dir, err)
	}
	found := false
	for _, e := range entries {
		if Numbered.MatchString(e.Name()) {
			if e.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("ADR corpus record %s must not be a symlink", e.Name())
			}
			info, err := e.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("ADR corpus record %s must be a regular file", e.Name())
			}
			found = true
		}
	}
	if !found {
		return fmt.Errorf("ADR corpus %s contains no numbered ADRs", dir)
	}
	return nil
}

// RepositoryRoot supports compatibility package entrypoints receiving a corpus
// directory. New callers pass repository root separately instead of inferring it.
func RepositoryRoot(dir string) string {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	for current := absolute; ; current = filepath.Dir(current) {
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			return current
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	return filepath.Dir(absolute)
}
