package plancmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wbern/adrian/go/internal/adr"
	"github.com/wbern/adrian/go/internal/budget"
	"github.com/wbern/adrian/go/internal/reviewpacket"
	"github.com/wbern/adrian/go/internal/reviewplan"
	"github.com/wbern/adrian/go/internal/sharedpacket"
)

const CodeUsage = "adrian code-plan --repository <owner/name> --base <ref> --head <ref> --policy <trusted-base-ref> [--legacy-packets] [--budget-profile <profile>] [--budget-mode <mode>] [--max-batch-bytes <bytes>] [--max-batch-segments <n>] [--fanout-threshold-bytes <bytes>] [--fanout-threshold-segments <n>] [--config <path>]"
const maxCodePlanBytes = 8 * 1024 * 1024

// CodePlan is an immutable source inventory, not an assessment or approval.
// Repository is supplied by the trusted caller and must be independently checked
// by the consumer together with this producer fingerprint and the current head.
type CodePlan struct {
	PacketPlan     *reviewpacket.Plan      `json:"packet_plan,omitempty"`
	Delivery       []DeliveryBinding       `json:"delivery,omitempty"`
	SharedDelivery *sharedpacket.Plan      `json:"shared_delivery,omitempty"`
	ChangeDiffs    []ChangeDiffRecord      `json:"change_diffs,omitempty"`
	Schema         string                  `json:"schema"`
	Repository     string                  `json:"repository"`
	Coverage       reviewplan.CoveragePlan `json:"coverage"`
	Content        []ContentRecord         `json:"content"`
	Policies       []PolicyRecord          `json:"policies"`
	Diff           string                  `json:"diff"`
	DiffSHA256     string                  `json:"diff_sha256"`
	Fingerprint    string                  `json:"fingerprint"`
	Budget         *budget.PlanSummary     `json:"budget,omitempty"`
}
type ContentRecord struct {
	reviewplan.PathVersion
	Mode     string `json:"mode"`
	ObjectID string `json:"object_id"`
	Kind     string `json:"kind"`
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
	SHA256   string `json:"sha256"`
}
type PolicyRecord struct {
	ADR     string `json:"adr"`
	Path    string `json:"path"`
	Content string `json:"content"`
	SHA256  string `json:"sha256"`
}

func RunCode(args []string, adrDir string, out io.Writer) error {
	return RunCodeCLI(args, filepath.Dir(filepath.Dir(adrDir)), out)
}

func RunCodeCLI(args []string, dir string, out io.Writer) error {
	var (
		repository              string
		legacy                  bool
		budgetProfile           string
		budgetMode              string
		maxBatchBytes           int64
		maxBatchSegments        int
		fanoutThresholdBytes    int64
		fanoutThresholdSegments int
		configFile              string
		planArgs                []string
	)

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--legacy-packets":
			if legacy {
				return fmt.Errorf("duplicate legacy packet option")
			}
			legacy = true
		case "--repository":
			if repository != "" || i+1 >= len(args) {
				return fmt.Errorf("repository must be specified once; %s", CodeUsage)
			}
			i++
			repository = args[i]
		case "--budget-profile":
			if i+1 >= len(args) {
				return fmt.Errorf("--budget-profile requires a profile name (e.g. cloud-high, cloud-standard, local-edge)")
			}
			i++
			budgetProfile = args[i]
		case "--budget-mode":
			if i+1 >= len(args) {
				return fmt.Errorf("--budget-mode requires a mode (auto, monolithic, partitioned)")
			}
			i++
			budgetMode = args[i]
		case "--max-batch-bytes":
			if i+1 >= len(args) {
				return fmt.Errorf("--max-batch-bytes requires a byte count")
			}
			i++
			n, err := strconv.ParseInt(args[i], 10, 64)
			if err != nil || n <= 0 {
				return fmt.Errorf("invalid --max-batch-bytes: %s", args[i])
			}
			maxBatchBytes = n
		case "--max-batch-segments":
			if i+1 >= len(args) {
				return fmt.Errorf("--max-batch-segments requires an integer count")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				return fmt.Errorf("invalid --max-batch-segments: %s", args[i])
			}
			maxBatchSegments = n
		case "--fanout-threshold-bytes":
			if i+1 >= len(args) {
				return fmt.Errorf("--fanout-threshold-bytes requires a byte count")
			}
			i++
			n, err := strconv.ParseInt(args[i], 10, 64)
			if err != nil || n <= 0 {
				return fmt.Errorf("invalid --fanout-threshold-bytes: %s", args[i])
			}
			fanoutThresholdBytes = n
		case "--fanout-threshold-segments":
			if i+1 >= len(args) {
				return fmt.Errorf("--fanout-threshold-segments requires an integer count")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				return fmt.Errorf("invalid --fanout-threshold-segments: %s", args[i])
			}
			fanoutThresholdSegments = n
		case "--config":
			if i+1 >= len(args) {
				return fmt.Errorf("--config requires a file path")
			}
			i++
			configFile = args[i]
		default:
			planArgs = append(planArgs, arg)
		}
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(repository) {
		return fmt.Errorf("trusted repository owner/name required; %s", CodeUsage)
	}
	// Reuse the existing strict immutable reader and complete NUL manifest. No
	// independent glob parser or head-policy shortcut is introduced here.
	var semanticJSON bytes.Buffer
	if err := RunCLI(planArgs, dir, &semanticJSON); err != nil {
		return err
	}
	var semantic reviewplan.Plan
	if err := json.Unmarshal(semanticJSON.Bytes(), &semantic); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	g := gitReader{dir: dir, ctx: ctx}
	root, err := g.run("rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	g.dir = strings.TrimSpace(string(root))
	registry, policies, err := g.readPolicy(semantic.PolicyCommit)
	if err != nil {
		return err
	}
	coverage := reviewplan.BuildCoverage(semantic.Provenance, semantic.Changes, policies, registry)
	p := CodePlan{Schema: "adrian-code-plan-v2", Repository: repository, Coverage: coverage, Content: []ContentRecord{}, Policies: []PolicyRecord{}}
	total := 0
	for _, v := range coverage.Baseline {
		record, err := g.content(v)
		if err != nil {
			return err
		}
		total += len(record.Content)
		if total > maxCodePlanBytes {
			return fmt.Errorf("code plan source exceeds byte budget")
		}
		p.Content = append(p.Content, record)
	}
	seen := map[string]bool{}
	for _, row := range coverage.Applicability {
		if seen[row.File] {
			continue
		}
		seen[row.File] = true
		raw, err := g.blob(semantic.PolicyCommit + ":" + row.File)
		if err != nil {
			return err
		}
		if !utf8.Valid(raw) {
			return fmt.Errorf("policy is not UTF-8: %s", row.File)
		}
		total += len(raw)
		if total > maxCodePlanBytes {
			return fmt.Errorf("code plan policy exceeds byte budget")
		}
		p.Policies = append(p.Policies, PolicyRecord{ADR: row.ADR, Path: row.File, Content: string(raw), SHA256: digest(raw)})
	}
	diff, err := g.runBounded("diff", "--no-relative", "--no-color", "--no-ext-diff", "--no-textconv", "--ignore-submodules=none", "--binary", semantic.MergeBase, semantic.HeadCommit, "--")
	if err != nil {
		return err
	}
	if !utf8.Valid(diff) {
		return fmt.Errorf("diff is not UTF-8")
	}
	p.Diff = string(diff)
	p.DiffSHA256 = digest(diff)
	if legacy {
		p.Schema = "adrian-code-plan-v1"
		if err := g.attachDelivery(&p); err != nil {
			return err
		}
	} else {
		if err := g.attachSharedDelivery(&p); err != nil {
			return err
		}
	}

	// Preemptive Capacity Budgeting
	cfg, profiles, err := budget.LoadConfig(configFile, g.dir)
	if err != nil {
		return err
	}
	if budgetMode != "" {
		cfg.Mode = budgetMode
	}
	if maxBatchBytes > 0 {
		cfg.BatchMaxBytes = maxBatchBytes
	}
	if maxBatchSegments > 0 {
		cfg.BatchMaxSegments = maxBatchSegments
	}
	if fanoutThresholdBytes > 0 {
		cfg.FanoutThresholdBytes = fanoutThresholdBytes
	}
	if fanoutThresholdSegments > 0 {
		cfg.FanoutThresholdSegments = fanoutThresholdSegments
	}

	activeProfile := budget.ResolveProfile(profiles, budgetProfile, cfg.DefaultProfile)

	policyByPath := make(map[string]PolicyRecord)
	for _, pol := range p.Policies {
		policyByPath[pol.Path] = pol
	}

	contentByPath := make(map[string]int64)
	for _, c := range p.Content {
		contentByPath[c.Path] = int64(len(c.Content))
	}

	type pathADRInfo struct {
		adrBytes    int64
		complexity  string
		diffContext string
	}
	pathInfo := make(map[string]pathADRInfo)
	for _, row := range p.Coverage.Applicability {
		rawPol, hasPol := policyByPath[row.File]
		polBytes := int64(0)
		complexity := "standard"
		diffCtx := "none"
		if hasPol {
			parsed := adr.ParseADR(rawPol.Content, rawPol.Path)
			polBytes = int64(len(rawPol.Content))
			if parsed.Complexity != "" {
				complexity = string(parsed.Complexity)
			}
			if parsed.DiffContextMode == adr.DiffContextFull {
				diffCtx = "full"
			} else if parsed.DiffContextMode == adr.DiffContextFunction {
				diffCtx = "function"
			}
		}

		for _, path := range row.Paths {
			existing := pathInfo[path]
			existing.adrBytes += polBytes
			if existing.complexity == "" || complexity == "complex" {
				existing.complexity = complexity
			}
			if diffCtx == "full" || (diffCtx == "function" && existing.diffContext != "full") {
				existing.diffContext = diffCtx
			}
			pathInfo[path] = existing
		}
	}

	var dominantComplexity string
	var dominantDiffContext string
	var totalBindingADRBytes int64
	var totalFullContextBytes int64
	segments := make([]budget.Segment, 0)

	if len(p.ChangeDiffs) > 0 {
		for idx, cd := range p.ChangeDiffs {
			segDiffBytes := int64(len(cd.Diff))
			segADRBytes := int64(0)
			segFullContextBytes := int64(0)
			segComplexity := "standard"
			segDiffContext := "none"

			for _, path := range cd.Paths {
				if info, ok := pathInfo[path]; ok {
					segADRBytes += info.adrBytes
					if info.complexity == "complex" || (info.complexity == "standard" && segComplexity != "complex") {
						segComplexity = info.complexity
					}
					if info.diffContext == "full" || (info.diffContext == "function" && segDiffContext != "full") {
						segDiffContext = info.diffContext
					}
				}
				if segDiffContext == "full" {
					if size, ok := contentByPath[path]; ok {
						if size > budget.MaxWholeFileSize {
							size = budget.MaxWholeFileSize
						}
						segFullContextBytes += size
					}
				}
			}

			if dominantComplexity == "" || segComplexity == "complex" {
				dominantComplexity = segComplexity
			}
			if segDiffContext == "full" || (segDiffContext == "function" && dominantDiffContext != "full") {
				dominantDiffContext = segDiffContext
			}

			segTokens, _ := budget.EstimateTokens(segDiffBytes, segADRBytes, segFullContextBytes, segComplexity, segDiffContext, activeProfile)

			segments = append(segments, budget.Segment{
				ID:               idx + 1,
				Bytes:            segDiffBytes,
				Paths:            cd.Paths,
				FullContextBytes: segFullContextBytes,
				BindingADRBytes:  segADRBytes,
				EstimatedTokens:  segTokens,
			})
		}
	} else if len(p.Coverage.Semantic.Changes) > 0 {
		for idx, c := range p.Coverage.Semantic.Changes {
			paths := []string{c.Path}
			if c.OldPath != "" && c.OldPath != c.Path {
				paths = append(paths, c.OldPath)
			}
			segDiffBytes := int64(len(p.Diff)) / int64(len(p.Coverage.Semantic.Changes))
			segADRBytes := int64(0)
			segFullContextBytes := int64(0)
			segComplexity := "standard"
			segDiffContext := "none"

			for _, path := range paths {
				if info, ok := pathInfo[path]; ok {
					segADRBytes += info.adrBytes
					if info.complexity == "complex" || (info.complexity == "standard" && segComplexity != "complex") {
						segComplexity = info.complexity
					}
					if info.diffContext == "full" || (info.diffContext == "function" && segDiffContext != "full") {
						segDiffContext = info.diffContext
					}
				}
				if segDiffContext == "full" {
					if size, ok := contentByPath[path]; ok {
						if size > budget.MaxWholeFileSize {
							size = budget.MaxWholeFileSize
						}
						segFullContextBytes += size
					}
				}
			}

			if dominantComplexity == "" || segComplexity == "complex" {
				dominantComplexity = segComplexity
			}
			if segDiffContext == "full" || (segDiffContext == "function" && dominantDiffContext != "full") {
				dominantDiffContext = segDiffContext
			}

			segTokens, _ := budget.EstimateTokens(segDiffBytes, segADRBytes, segFullContextBytes, segComplexity, segDiffContext, activeProfile)

			segments = append(segments, budget.Segment{
				ID:               idx + 1,
				Bytes:            segDiffBytes,
				Paths:            paths,
				FullContextBytes: segFullContextBytes,
				BindingADRBytes:  segADRBytes,
				EstimatedTokens:  segTokens,
			})
		}
	}

	for _, seg := range segments {
		totalBindingADRBytes += seg.BindingADRBytes
		totalFullContextBytes += seg.FullContextBytes
	}

	if dominantComplexity == "" {
		dominantComplexity = "standard"
	}
	if dominantDiffContext == "" {
		dominantDiffContext = "none"
	}

	totalDiffBytes := int64(len(p.Diff))
	summary := budget.BuildPlanSummary(segments, totalDiffBytes, totalBindingADRBytes, totalFullContextBytes, dominantComplexity, dominantDiffContext, activeProfile, cfg)
	p.Budget = &summary

	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	p.Fingerprint = digest(raw)
	raw, err = json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if len(raw) > maxCodePlanBytes {
		return fmt.Errorf("encoded code plan exceeds byte budget")
	}
	_, err = out.Write(append(raw, '\n'))
	return err
}

func (g gitReader) content(v reviewplan.PathVersion) (ContentRecord, error) {
	record := ContentRecord{PathVersion: v}
	raw, err := g.run("ls-tree", "-z", v.Commit, "--", ":(literal)"+v.Path)
	if err != nil {
		return record, err
	}
	if len(raw) == 0 || raw[len(raw)-1] != 0 {
		return record, fmt.Errorf("missing versioned content: %s", v.Path)
	}
	records := bytes.Split(raw[:len(raw)-1], []byte{0})
	if len(records) != 1 {
		return record, fmt.Errorf("ambiguous content: %s", v.Path)
	}
	header, path, ok := strings.Cut(string(records[0]), "\t")
	parts := strings.Fields(header)
	if !ok || path != v.Path || len(parts) != 3 || !commitID.MatchString(parts[2]) {
		return record, fmt.Errorf("invalid content record: %s", v.Path)
	}
	record.Mode, record.ObjectID = parts[0], parts[2]
	if parts[0] == "160000" && parts[1] == "commit" {
		record.Kind = "gitlink"
		record.Encoding = "none"
		return record, nil
	}
	if parts[1] != "blob" || (parts[0] != "100644" && parts[0] != "100755" && parts[0] != "120000") {
		return record, fmt.Errorf("unsupported content kind: %s", v.Path)
	}
	content, err := g.blob(parts[2])
	if err != nil {
		return record, err
	}
	record.Kind = "file"
	if parts[0] == "120000" {
		record.Kind = "symlink"
	}
	record.SHA256 = digest(content)
	if utf8.Valid(content) && !bytes.ContainsRune(content, 0) {
		record.Encoding = "utf-8"
		record.Content = string(content)
	} else {
		record.Encoding = "base64"
		record.Content = base64.StdEncoding.EncodeToString(content)
	}
	return record, nil
}
func (g gitReader) blob(object string) ([]byte, error) {
	raw, err := g.run("cat-file", "-s", object)
	if err != nil {
		return nil, err
	}
	size, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil || size < 0 || size > maxCodePlanBytes {
		return nil, fmt.Errorf("blob exceeds byte budget")
	}
	return g.runBounded("cat-file", "blob", object)
}
func digest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

// Repository-location overrides and object replacement must not redirect a
// trusted producer read to another checkout or silently acquire remote objects.
func cleanGitEnvironment() []string {
	env := []string{}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			env = append(env, entry)
		}
	}
	return append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_NO_LAZY_FETCH=1", "GIT_NO_REPLACE_OBJECTS=1", "GIT_TERMINAL_PROMPT=0")
}

type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(raw []byte) (int, error) {
	if len(raw) > maxCodePlanBytes-b.Len() {
		return 0, fmt.Errorf("git output exceeds byte budget")
	}
	return b.Buffer.Write(raw)
}
func (g gitReader) runBounded(args ...string) ([]byte, error) {
	parent := g.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.WaitDelay = time.Second
	cmd.Dir = g.dir
	cmd.Env = cleanGitEnvironment()
	var out boundedBuffer
	var stderr boundedBuffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return out.Bytes(), nil
}

// Delivery binds all chunks to one complete scope. Lexical prefilters are not
// used for this full-code contract: even a matched hunk cannot discharge the
// remaining changed code. Budget skips remain explicit fallback obligations.
type DeliveryBinding struct {
	SourceID         string   `json:"source_id"`
	Paths            []string `json:"paths"`
	PacketIndices    []int    `json:"packet_indices"`
	PacketSHA256     []string `json:"packet_sha256"`
	SkippedIndices   []int    `json:"skipped_indices"`
	FallbackRequired bool     `json:"fallback_required"`
}

func (g gitReader) attachDelivery(p *CodePlan) error {
	inputs := []reviewpacket.Input{}
	p.Delivery = []DeliveryBinding{}
	add := func(sourceID string, paths []string, policy adr.ADR) error {
		args := []string{"diff", "--no-relative", "--no-color", "--no-ext-diff", "--no-textconv", "--ignore-submodules=none", "--binary", p.Coverage.Semantic.MergeBase, p.Coverage.Semantic.HeadCommit, "--"}
		for _, path := range paths {
			args = append(args, ":(literal)"+path)
		}
		raw, err := g.runBounded(args...)
		if err != nil {
			return err
		}
		inputs = append(inputs, reviewpacket.Input{SourceID: sourceID, ADR: policy, Files: paths, Diff: string(raw)})
		p.Delivery = append(p.Delivery, DeliveryBinding{SourceID: sourceID, Paths: paths, PacketIndices: []int{}, PacketSHA256: []string{}, SkippedIndices: []int{}})
		return nil
	}
	if len(p.Coverage.Baseline) > 0 {
		paths := []string{}
		seen := map[string]bool{}
		for _, version := range p.Coverage.Baseline {
			if !seen[version.Path] {
				paths = append(paths, version.Path)
				seen[version.Path] = true
			}
		}
		if err := add("baseline", paths, adr.ADR{ID: "baseline", Title: "Baseline code review", Decision: "Assess all changed behavior and its direct consumers, accepted inputs, null/empty/error/async states, security boundaries and tests. Preserve reachable working paths. Cross-check interactions across chunks before consolidating findings.", DiffContext: true}); err != nil {
			return err
		}
	}
	byFile := map[string]PolicyRecord{}
	for _, policy := range p.Policies {
		byFile[policy.Path] = policy
	}
	for _, row := range p.Coverage.Applicability {
		raw, ok := byFile[row.File]
		if !ok {
			return fmt.Errorf("missing complete policy for scope %s", row.ID)
		}
		policy := adr.ParseADR(raw.Content, row.File)
		policy.ID = row.ADR
		policy.Decision = raw.Content
		policy.PreFilter = nil
		policy.EnforcedBy = nil
		policy.DiffContext = true
		if err := add(row.ID, row.Paths, policy); err != nil {
			return err
		}
	}
	native := reviewpacket.Build(inputs, reviewpacket.Options{MaxTokensPerChunk: 4096, MaxPackets: 256})
	p.PacketPlan = &native
	for i := range p.Delivery {
		b := &p.Delivery[i]
		for index, packet := range p.PacketPlan.Packets {
			if packet.SourceID == b.SourceID {
				b.PacketIndices = append(b.PacketIndices, index+1)
				b.PacketSHA256 = append(b.PacketSHA256, reviewpacket.PacketSHA256(packet))
			}
		}
		for index, skip := range p.PacketPlan.Skipped {
			if skip.SourceID == b.SourceID {
				b.SkippedIndices = append(b.SkippedIndices, index+1)
			}
		}
		b.FallbackRequired = len(b.SkippedIndices) > 0 || len(b.PacketIndices) == 0 || p.PacketPlan.Limits.PacketLimitExceeded
	}
	return nil
}
