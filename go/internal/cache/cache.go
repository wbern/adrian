// Package cache stores LintResults keyed by a SHA-256 of the
// (ADR, diff, model, provider) tuple. Entries are flat files under
// cacheDir/<key>.json.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wbern/adrian/go/internal/adr"
	"github.com/wbern/adrian/go/internal/logger"
	"github.com/wbern/adrian/go/internal/types"
)

const cacheVersion = "1"

// Provider identifies the lint backend used to produce a cached result.
type Provider string

// keyInput is the JSON-serialized payload hashed into the cache key.
// Field order and names are load-bearing — changing either invalidates
// every existing cache entry.
type keyInput struct {
	V         string   `json:"v"`
	ADRID     string   `json:"adrId"`
	Decision  string   `json:"decision"`
	PreFilter []string `json:"preFilter,omitempty"`
	Diff      string   `json:"diff"`
	Model     string   `json:"model"`
	Provider  Provider `json:"provider"`
}

// LintFn produces a LintResult for a given (ADR, diff) pair.
type LintFn func(a adr.ADR, diff string) (types.LintResult, error)

// Options controls LintWithCache behavior. All fields are optional and
// zero-valued by default.
type Options struct {
	NoCache bool
	Verbose bool
	// Logger receives "Cache hit for ADR <id>" when Verbose or CI is
	// set. nil means logger.Default.
	Logger *logger.Logger
}

// LintWithCache returns either a cached LintResult or invokes lintFn
// and stores its result. Errors from cache I/O are non-fatal: on read
// failure we fall through to lintFn; on write failure we still return
// the fresh result.
func LintWithCache(
	a adr.ADR,
	diff, model string,
	provider Provider,
	lintFn LintFn,
	cacheDir string,
	opts Options,
) (types.LintResult, error) {
	key := BuildCacheKey(a, diff, model, provider)

	if !opts.NoCache {
		if cached, _ := GetCachedResult(key, cacheDir); cached != nil {
			if opts.Verbose || os.Getenv("CI") != "" {
				l := opts.Logger
				if l == nil {
					l = logger.Default
				}
				l.Log(fmt.Sprintf("Cache hit for ADR %s", a.ID))
			}
			cached.Cached = true
			return *cached, nil
		}
	}

	result, err := lintFn(a, diff)
	if err != nil {
		return result, err
	}

	if !opts.NoCache && result.Status != types.StatusERROR && result.Status != types.StatusSKIPPED {
		_ = SetCachedResult(key, result, cacheDir)
	}

	result.Cached = false
	return result, nil
}

// GetCacheDir returns the default cache directory under the current
// working directory: `.cache/adr-lint`.
func GetCacheDir() string {
	return ".cache/adr-lint"
}

// SetCachedResult writes a LintResult under key in cacheDir, creating
// the directory tree if needed.
func SetCachedResult(key string, r types.LintResult, cacheDir string) error {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cacheDir, key+".json"), data, 0o644)
}

// GetCachedResult reads a previously-stored LintResult for key, or
// (nil, nil) if no entry exists. Non-existence-related I/O errors are
// returned so callers can distinguish "I/O failed" from "miss".
func GetCachedResult(key, cacheDir string) (*types.LintResult, error) {
	path := filepath.Join(cacheDir, key+".json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r types.LintResult
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// BuildCacheKey returns the hex SHA-256 of the input tuple.
func BuildCacheKey(a adr.ADR, diff, model string, provider Provider) string {
	payload, _ := json.Marshal(keyInput{
		V:         cacheVersion,
		ADRID:     a.ID,
		Decision:  a.Decision,
		PreFilter: a.PreFilter,
		Diff:      diff,
		Model:     model,
		Provider:  provider,
	})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
