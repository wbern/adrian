// Package cliparser parses argv into LintOptions.
package cliparser

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/wbern/adrian/go/internal/types"
)

var validProviders = []types.Provider{
	types.ProviderClaude,
}

func isProvider(s string) bool {
	for _, p := range validProviders {
		if string(p) == s {
			return true
		}
	}
	return false
}

// ParseArgs converts argv into a LintOptions. Returns an error for an
// invalid provider, non-numeric --adrs id, or non-positive --parallel.
func ParseArgs(args []string) (types.LintOptions, error) {
	opts := types.LintOptions{}

	if err := resolveProvider(args, &opts); err != nil {
		return opts, err
	}

	opts.CI = slices.Contains(args, "--ci")
	opts.Verbose = slices.Contains(args, "--verbose") || slices.Contains(args, "-v")
	opts.DryRun = slices.Contains(args, "--dry-run")
	opts.NoCache = slices.Contains(args, "--no-cache")
	opts.PerFile = slices.Contains(args, "--per-file")
	opts.NoPreFilter = slices.Contains(args, "--no-pre-filter")

	resolveBranch(args, &opts)
	resolveFiles(args, &opts)

	if err := resolveDiff(args, &opts); err != nil {
		return opts, err
	}
	if err := resolveValueFlag(args, "--cache-dir", &opts.CacheDir); err != nil {
		return opts, err
	}
	if err := resolveValueFlag(args, "--report-dir", &opts.ReportDir); err != nil {
		return opts, err
	}

	if err := resolveADRs(args, &opts); err != nil {
		return opts, err
	}
	if err := resolveParallel(args, &opts); err != nil {
		return opts, err
	}

	return opts, nil
}

func resolveBranch(args []string, opts *types.LintOptions) {
	branchIndex := -1
	if i := slices.Index(args, "--branch"); i > branchIndex {
		branchIndex = i
	}
	if i := slices.Index(args, "-b"); i > branchIndex {
		branchIndex = i
	}
	if branchIndex == -1 {
		return
	}
	opts.BranchSet = true
	if branchIndex+1 < len(args) {
		next := args[branchIndex+1]
		if !strings.HasPrefix(next, "-") {
			opts.BranchRef = next
		}
	}
}

// resolveDiff handles --diff <path> / --diff=<path>, where "-" means stdin.
//
// It cannot reuse resolveBranch's "next token is a value unless it starts with
// a dash" rule, because the stdin spelling IS a dash. Requiring the value
// explicitly also turns `--diff` with a forgotten argument into an error rather
// than a silent fall-through to the staged diff, which would review the wrong
// changes while looking like it worked.
func resolveDiff(args []string, opts *types.LintOptions) error {
	var val string
	found := false
	for i, a := range args {
		switch {
		case a == "--diff":
			if i+1 < len(args) {
				val = args[i+1]
			}
			found = true
		case strings.HasPrefix(a, "--diff="):
			val = strings.TrimPrefix(a, "--diff=")
			found = true
		default:
			continue
		}
		break
	}
	if !found {
		return nil
	}
	if val == "" || (strings.HasPrefix(val, "-") && val != "-") {
		return fmt.Errorf("missing --diff value: expected a path to a unified diff, or - for stdin")
	}
	// --branch computes a diff and --files synthesises one; --diff supplies it.
	// Honouring one and dropping the other silently would review bytes the
	// caller never named.
	if opts.BranchSet {
		return fmt.Errorf("--diff cannot be combined with --branch: both supply the diff to check")
	}
	if opts.Files != nil {
		return fmt.Errorf("--diff cannot be combined with --files: both supply the diff to check")
	}
	opts.DiffSet = true
	opts.DiffPath = val
	return nil
}

// resolveValueFlag reads a plain `--name value` / `--name=value` string option.
func resolveValueFlag(args []string, name string, dst *string) error {
	eq := name + "="
	for i, a := range args {
		switch {
		case a == name:
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return fmt.Errorf("missing %s value: expected a directory path", name)
			}
			*dst = args[i+1]
			return nil
		case strings.HasPrefix(a, eq):
			val := strings.TrimPrefix(a, eq)
			if val == "" {
				return fmt.Errorf("missing %s value: expected a directory path", name)
			}
			*dst = val
			return nil
		}
	}
	return nil
}

func resolveFiles(args []string, opts *types.LintOptions) {
	idx := slices.Index(args, "--files")
	if idx == -1 {
		return
	}
	opts.Files = []string{}
	for i := idx + 1; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			break
		}
		opts.Files = append(opts.Files, args[i])
	}
}

var numericRE = regexp.MustCompile(`^\d+$`)

func resolveADRs(args []string, opts *types.LintOptions) error {
	idx := slices.Index(args, "--adrs")
	if idx == -1 {
		return nil
	}
	opts.ADRs = []string{}
	for i := idx + 1; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			break
		}
		for _, id := range strings.Split(args[i], ",") {
			id = strings.TrimSpace(id)
			if !numericRE.MatchString(id) {
				return fmt.Errorf("invalid ADR ID %q: must be numeric (e.g., --adrs 3,5,6)", id)
			}
			opts.ADRs = append(opts.ADRs, id)
		}
	}
	return nil
}

func resolveParallel(args []string, opts *types.LintOptions) error {
	idx := slices.Index(args, "--parallel")
	if idx == -1 {
		return nil
	}
	var val string
	if idx+1 < len(args) {
		val = args[idx+1]
	}
	n, err := strconv.Atoi(val)
	if err != nil || n < 1 {
		return fmt.Errorf("invalid --parallel value %q: must be a positive integer", val)
	}
	opts.Parallel = &n
	return nil
}

func resolveProvider(args []string, opts *types.LintOptions) error {
	for i, a := range args {
		if a == "--provider" {
			if i+1 >= len(args) {
				return fmt.Errorf("missing --provider value: must be one of: %s", providerList())
			}
			val := args[i+1]
			if !isProvider(val) {
				return fmt.Errorf("invalid --provider value %q: must be one of: %s", val, providerList())
			}
			opts.Provider = types.Provider(val)
			return nil
		}
		if strings.HasPrefix(a, "--provider=") {
			val := strings.TrimPrefix(a, "--provider=")
			if !isProvider(val) {
				return fmt.Errorf("invalid --provider value %q: must be one of: %s", val, providerList())
			}
			opts.Provider = types.Provider(val)
			return nil
		}
	}
	if env := os.Getenv("ADR_LINT_PROVIDER"); isProvider(env) {
		opts.Provider = types.Provider(env)
		return nil
	}
	opts.Provider = types.ProviderClaude
	return nil
}

func providerList() string {
	parts := make([]string, len(validProviders))
	for i, p := range validProviders {
		parts[i] = string(p)
	}
	return strings.Join(parts, ", ")
}
