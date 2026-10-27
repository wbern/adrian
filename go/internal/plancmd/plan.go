// Package plancmd obtains an immutable policy and a complete change manifest
// directly from Git before publishing any review plan.
package plancmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wbern/adrian/go/internal/corpus"
	"github.com/wbern/adrian/go/internal/reviewplan"
	"github.com/wbern/adrian/go/internal/reviewpolicy"
)

const Usage = "adrian plan --base <ref> --head <ref> --policy <trusted-base-ref> [--format json] [--budget-profile <profile>] [--budget-mode <mode>] [--max-batch-bytes <bytes>] [--max-batch-segments <n>] [--max-batches <n>] [--fanout-threshold-bytes <bytes>] [--max-plan-bytes <bytes>] [--config <path>]"

func Run(args []string, adrDir string, out io.Writer) error {
	return RunCLI(args, corpus.RepositoryRoot(adrDir), out)
}

func RunCLI(args []string, dir string, out io.Writer) error {
	opts, err := parseArgs(args)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	var maxPlanBytes int64
	if s, ok := opts["--max-plan-bytes"]; ok {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
			maxPlanBytes = n
		}
	}
	git := gitReader{dir: dir, ctx: ctx, maxBytes: maxPlanBytes}
	root, err := git.run("rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	git.dir = strings.TrimSpace(string(root))
	base, err := git.commit(opts["--base"])
	if err != nil {
		return err
	}
	head, err := git.commit(opts["--head"])
	if err != nil {
		return err
	}
	policy, err := git.commit(opts["--policy"])
	if err != nil {
		return err
	}
	if _, err = git.run("merge-base", "--is-ancestor", policy, base); err != nil {
		return fmt.Errorf("policy must be from the trusted base history: %w", err)
	}
	mergeRaw, err := git.run("merge-base", base, head)
	if err != nil {
		return err
	}
	mergeBase := strings.TrimSpace(string(mergeRaw))
	if !commitID.MatchString(mergeBase) {
		return fmt.Errorf("invalid merge base returned by Git")
	}
	manifest, err := git.run("diff", "--no-relative", "--no-color", "--no-ext-diff", "--no-textconv", "--ignore-submodules=none", "--name-status", "-z", "--find-renames", mergeBase, head, "--")
	if err != nil {
		return err
	}
	changes, err := parseChanges(manifest)
	if err != nil {
		return err
	}
	registry, policies, err := git.readPolicy(policy)
	if err != nil {
		return err
	}
	p := reviewplan.Build(reviewplan.Provenance{BaseCommit: base, MergeBase: mergeBase, HeadCommit: head, PolicyCommit: policy}, changes, policies, registry)
	payload, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	_, err = out.Write(payload)
	return err
}

func parseArgs(args []string) (map[string]string, error) {
	values := map[string]string{}
	for i := 0; i < len(args); i++ {
		flag := args[i]
		switch flag {
		case "--base", "--head", "--policy", "--format",
			"--budget-profile", "--budget-mode", "--max-batch-bytes",
			"--max-batch-segments", "--max-batches", "--fanout-threshold-bytes", "--fanout-threshold-segments", "--max-plan-bytes", "--config":
		default:
			return nil, fmt.Errorf("unknown argument %q; usage: %s", flag, Usage)
		}
		if _, ok := values[flag]; ok {
			return nil, fmt.Errorf("%s must be specified once", flag)
		}
		i++
		if i >= len(args) || args[i] == "" || strings.HasPrefix(args[i], "-") {
			return nil, fmt.Errorf("%s requires a value", flag)
		}
		values[flag] = args[i]
	}
	for _, required := range []string{"--base", "--head", "--policy"} {
		if values[required] == "" {
			return nil, fmt.Errorf("%s is required; usage: %s", required, Usage)
		}
	}
	if f := values["--format"]; f != "" && f != "json" {
		return nil, fmt.Errorf("unsupported format %q; only json is supported", f)
	}
	return values, nil
}

type gitReader struct {
	dir      string
	ctx      context.Context
	maxBytes int64
}

func (g gitReader) maxPlanBytes() int64 {
	if g.maxBytes > 0 {
		return g.maxBytes
	}
	return defaultMaxCodePlanBytes
}

var commitID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

func (g gitReader) run(args ...string) ([]byte, error) { return g.runBounded(args...) }

func (g gitReader) commit(ref string) (string, error) {
	raw, err := g.run("rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(raw))
	if !commitID.MatchString(id) {
		return "", fmt.Errorf("invalid commit returned by Git for %q", ref)
	}
	return id, nil
}

func (g gitReader) readPolicy(commit string) (reviewpolicy.Registry, []reviewpolicy.ADR, error) {
	var registry reviewpolicy.Registry
	// Configuration is policy: read it from the trusted commit, never the checkout.
	configTree, err := g.run("ls-tree", "-z", "--full-tree", commit, "--", ":(literal)"+corpus.ConfigPath)
	if err != nil {
		return registry, nil, err
	}
	configured := ""
	if len(configTree) > 0 {
		header, _, ok := strings.Cut(strings.TrimSuffix(string(configTree), "\x00"), "\t")
		parts := strings.Fields(header)
		if !ok || len(parts) != 3 || (parts[0] != "100644" && parts[0] != "100755") || parts[1] != "blob" || !commitID.MatchString(parts[2]) {
			return registry, nil, fmt.Errorf("corpus configuration must be a regular Git blob")
		}
		config, e := g.run("cat-file", "blob", parts[2])
		if e != nil {
			return registry, nil, e
		}
		configured, e = corpus.ConfigDir(config)
		if e != nil {
			return registry, nil, e
		}
	}
	exists := func(p string) (bool, error) {
		raw, e := g.run("ls-tree", "-z", "--full-tree", commit, "--", ":(literal)"+p)
		if e != nil {
			return false, e
		}
		if len(raw) == 0 {
			return false, nil
		}
		fields := strings.Fields(strings.SplitN(string(raw), "\t", 2)[0])
		if len(fields) != 3 || fields[0] != "040000" || fields[1] != "tree" {
			return false, fmt.Errorf("corpus %s must be a Git tree", p)
		}
		return true, nil
	}
	selected, err := corpus.Select(configured, exists)
	if err != nil {
		return registry, nil, err
	}
	// Check every ancestor explicitly, rejecting symlinks even for custom paths.
	components := strings.Split(selected, "/")
	for i := range components {
		ok, e := exists(strings.Join(components[:i+1], "/"))
		if e != nil {
			return registry, nil, e
		}
		if !ok {
			return registry, nil, fmt.Errorf("ADR corpus %s missing in policy commit", selected)
		}
	}
	raw, err := g.run("ls-tree", "-r", "-z", "--full-tree", commit, "--", ":(literal)"+selected, ":(literal)"+reviewpolicy.RegistryPath)
	if err != nil {
		return registry, nil, err
	}
	if len(raw) == 0 || raw[len(raw)-1] != 0 {
		return registry, nil, fmt.Errorf("missing or incomplete policy tree")
	}
	type blob struct{ path, oid string }
	var adrs []blob
	var registryOID string
	for _, record := range bytes.Split(raw[:len(raw)-1], []byte{0}) {
		header, file, ok := strings.Cut(string(record), "\t")
		if !ok {
			return registry, nil, fmt.Errorf("malformed policy tree record")
		}
		parts := strings.Fields(header)
		if len(parts) != 3 {
			return registry, nil, fmt.Errorf("malformed policy tree header")
		}
		if file != reviewpolicy.RegistryPath && (filepath.ToSlash(filepath.Dir(file)) != selected || !corpus.Numbered.MatchString(filepath.Base(file))) {
			continue
		}
		if (parts[0] != "100644" && parts[0] != "100755") || parts[1] != "blob" || !commitID.MatchString(parts[2]) {
			return registry, nil, fmt.Errorf("policy file %s must be a regular Git blob", file)
		}
		if file == reviewpolicy.RegistryPath {
			registryOID = parts[2]
		} else {
			adrs = append(adrs, blob{path: file, oid: parts[2]})
		}
	}
	if registryOID == "" {
		return registry, nil, fmt.Errorf("missing %s in policy commit", reviewpolicy.RegistryPath)
	}
	if len(adrs) == 0 {
		return registry, nil, fmt.Errorf("no numbered ADRs in policy commit")
	}
	contents, err := g.run("cat-file", "blob", registryOID)
	if err != nil {
		return registry, nil, err
	}
	registry, err = reviewpolicy.ParseRegistry(contents)
	if err != nil {
		return registry, nil, fmt.Errorf("%s: %w", reviewpolicy.RegistryPath, err)
	}
	policyBytes := len(raw) + len(contents)
	policies := make([]reviewpolicy.ADR, 0, len(adrs))
	seen := map[string]bool{}
	for _, item := range adrs {
		contents, err = g.run("cat-file", "blob", item.oid)
		if err != nil {
			return registry, nil, err
		}
		policyBytes += len(contents)
		if int64(policyBytes) > g.maxPlanBytes() {
			return registry, nil, fmt.Errorf("policy set exceeds byte budget")
		}
		a, err := reviewpolicy.ParseADR(item.path, contents, registry)
		if err != nil {
			return registry, nil, err
		}
		if seen[a.ID] {
			return registry, nil, fmt.Errorf("duplicate ADR ID %s", a.ID)
		}
		seen[a.ID] = true
		policies = append(policies, a)
	}
	if err = reviewpolicy.ValidateSet(policies); err != nil {
		return registry, nil, err
	}
	return registry, policies, nil
}

func parseChanges(raw []byte) ([]reviewplan.Change, error) {
	changes := []reviewplan.Change{}
	if len(raw) == 0 {
		return changes, nil
	}
	if raw[len(raw)-1] != 0 {
		return nil, fmt.Errorf("incomplete Git change manifest")
	}
	parts := bytes.Split(raw[:len(raw)-1], []byte{0})
	seen := map[string]bool{}
	for i := 0; i < len(parts); {
		status := string(parts[i])
		i++
		if status == "" {
			return nil, fmt.Errorf("empty Git change status")
		}
		count := 1
		switch status {
		case "M", "A", "D", "T":
		default:
			if status[0] != 'R' && status[0] != 'C' {
				return nil, fmt.Errorf("unsupported Git change status %q", status)
			}
			score, err := strconv.Atoi(status[1:])
			if err != nil || score < 0 || score > 100 {
				return nil, fmt.Errorf("invalid rename score %q", status)
			}
			count = 2
		}
		if i+count > len(parts) {
			return nil, fmt.Errorf("incomplete Git change record")
		}
		c := reviewplan.Change{Status: status, Path: string(parts[i+count-1])}
		if count == 2 {
			c.OldPath = string(parts[i])
		}
		i += count
		if !validPath(c.Path) || (count == 2 && !validPath(c.OldPath)) {
			return nil, fmt.Errorf("invalid repository-relative path in Git manifest")
		}
		if seen[c.Path] {
			return nil, fmt.Errorf("duplicate path %q in Git manifest", c.Path)
		}
		seen[c.Path] = true
		changes = append(changes, c)
	}
	return changes, nil
}
func validPath(p string) bool {
	if p == "" || !utf8.ValidString(p) || strings.HasPrefix(p, "/") || strings.Contains(p, "\x00") {
		return false
	}
	for _, component := range strings.Split(p, "/") {
		if component == ".." || component == "." || component == "" {
			return false
		}
	}
	return true
}
