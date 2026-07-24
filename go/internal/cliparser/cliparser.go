// Package cliparser parses argv into LintOptions.
package cliparser

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/wbern/adr-lint/go/internal/types"
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
	opts.ReviewPlan = slices.Contains(args, "--review-plan")
	if err := resolveReviewPlanTokenBudget(args, &opts); err != nil {
		return opts, err
	}
	if err := resolveReviewPlanPacketLimit(args, &opts); err != nil {
		return opts, err
	}

	resolveBranch(args, &opts)
	resolveFiles(args, &opts)

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

func resolveReviewPlanTokenBudget(args []string, opts *types.LintOptions) error {
	if !opts.ReviewPlan {
		return nil
	}
	opts.MaxTokensPerChunk = 4096
	idx := slices.Index(args, "--max-tokens-per-chunk")
	if idx == -1 {
		return nil
	}
	value := ""
	if idx+1 < len(args) {
		value = args[idx+1]
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 {
		return fmt.Errorf("invalid --max-tokens-per-chunk value %q: must be a positive integer", value)
	}
	opts.MaxTokensPerChunk = n
	return nil
}

func resolveReviewPlanPacketLimit(args []string, opts *types.LintOptions) error {
	if !opts.ReviewPlan {
		return nil
	}
	idx := slices.Index(args, "--max-packets")
	if idx == -1 {
		return nil
	}
	value := ""
	if idx+1 < len(args) {
		value = args[idx+1]
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 {
		return fmt.Errorf("invalid --max-packets value %q: must be a positive integer", value)
	}
	opts.MaxPackets = n
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
