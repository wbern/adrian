package plancmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wbern/adrian/go/internal/sharedpacket"
)

func TestCodePlanRetainsUnannotatedRulesAndDeletedBaseContent(t *testing.T) {
	dir, _ := fixture(t)
	write(t, dir, "doc/adr/0040-completeness.md", "---\nstatus: accepted\napplies_to: ['src/**']\npre_filter: ['never-hits']\n---\n# Whole rule\n## Decision\nRetain working paths.\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "trusted legacy rule")
	base := git(t, dir, "rev-parse", "HEAD")
	if err := os.Remove(filepath.Join(dir, "src/login.tsx")); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "delete route")
	head := git(t, dir, "rev-parse", "HEAD")
	args := []string{"--base", base, "--head", head, "--policy", base, "--repository", "wbern/crm"}
	var out bytes.Buffer
	if err := RunCodeCLI(args, dir, &out); err != nil {
		t.Fatal(err)
	}
	var p struct {
		Repository string `json:"repository"`
		Coverage   struct {
			Applicability []struct {
				ADR string `json:"adr"`
			} `json:"applicability"`
		} `json:"coverage"`
		Content []struct {
			Path    string `json:"path"`
			Side    string `json:"side"`
			Commit  string `json:"commit"`
			Content string `json:"content"`
		} `json:"content"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.Unmarshal(out.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Repository != "wbern/crm" || len(p.Coverage.Applicability) != 2 || p.Fingerprint == "" {
		t.Fatalf("missing obligations: %s", out.String())
	}
	if len(p.Content) != 1 || p.Content[0].Path != "src/login.tsx" || p.Content[0].Side != "base" || p.Content[0].Commit != base || p.Content[0].Content != "export const login = true\n" {
		t.Fatalf("lost deletion source: %+v", p.Content)
	}
	var repeat bytes.Buffer
	if err := RunCodeCLI(args, dir, &repeat); err != nil || repeat.String() != out.String() {
		t.Fatal("not deterministic", err)
	}
	var refused bytes.Buffer
	if err := RunCodeCLI(args[:len(args)-2], dir, &refused); err == nil || refused.Len() != 0 {
		t.Fatal("repository identity required")
	}
}

func TestCodePlanIgnoresRepositoryEnvironmentAndPreservesBinary(t *testing.T) {
	dir, base := fixture(t)
	write(t, dir, "src/binary.dat", string([]byte{0, 255, 1}))
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "binary")
	head := git(t, dir, "rev-parse", "HEAD")
	foreign, _ := fixture(t)
	t.Setenv("GIT_DIR", filepath.Join(foreign, ".git"))
	t.Setenv("GIT_WORK_TREE", foreign)
	var out bytes.Buffer
	if err := RunCodeCLI([]string{"--base", base, "--head", head, "--policy", base, "--repository", "wbern/crm"}, dir, &out); err != nil {
		t.Fatal(err)
	}
	var p CodePlan
	if err := json.Unmarshal(out.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Coverage.Semantic.HeadCommit != head || len(p.Content) != 1 || p.Content[0].Encoding != "base64" || p.Content[0].Content != "AP8B" {
		t.Fatalf("redirected/lost bytes: %+v", p.Content)
	}
}

func TestCodePlanNativePacketBindingsRetainWholeScopeAndBaseline(t *testing.T) {
	dir, _ := fixture(t)
	write(t, dir, "doc/adr/0040-completeness.md", "---\nstatus: accepted\napplies_to: ['src/**']\npre_filter: ['never-hits']\n---\n# Whole rule\n## Decision\nRetain working paths.\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "legacy rule")
	base := git(t, dir, "rev-parse", "HEAD")
	write(t, dir, "src/login.tsx", "export const login = false\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "behavior")
	var out bytes.Buffer
	if err := RunCodeCLI([]string{"--base", base, "--head", "HEAD", "--policy", base, "--repository", "wbern/crm", "--legacy-packets"}, dir, &out); err != nil {
		t.Fatal(err)
	}
	var p struct {
		PacketPlan struct {
			PlanSHA256 string `json:"planSha256"`
			Packets    []struct {
				SourceID string `json:"sourceId"`
			} `json:"packets"`
		} `json:"packet_plan"`
		Delivery []struct {
			SourceID      string `json:"source_id"`
			PacketIndices []int  `json:"packet_indices"`
			Fallback      bool   `json:"fallback_required"`
		} `json:"delivery"`
	}
	if err := json.Unmarshal(out.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.PacketPlan.PlanSHA256 == "" || len(p.Delivery) != 3 {
		t.Fatalf("missing native bridge: %s", out.String())
	}
	for _, binding := range p.Delivery {
		if binding.SourceID == "" || len(binding.PacketIndices) == 0 || binding.Fallback {
			t.Fatalf("scope dropped: %+v", binding)
		}
		for _, index := range binding.PacketIndices {
			if index < 1 || index > len(p.PacketPlan.Packets) || p.PacketPlan.Packets[index-1].SourceID != binding.SourceID {
				t.Fatal("foreign packet binding")
			}
		}
	}
}

func TestNestedCwdAndRelativeGitConfigCannotNarrowManifest(t *testing.T) {
	dir, _ := fixture(t)
	write(t, dir, "sub/a.txt", "before\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "nested baseline")
	base := git(t, dir, "rev-parse", "HEAD")
	write(t, dir, "sub/a.txt", "after\n")
	write(t, dir, "outside.txt", "outside\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "two paths")
	git(t, dir, "config", "diff.relative", "true")
	var out bytes.Buffer
	if err := RunCodeCLI([]string{"--base", base, "--head", "HEAD", "--policy", base, "--repository", "wbern/crm"}, filepath.Join(dir, "sub"), &out); err != nil {
		t.Fatal(err)
	}
	var p CodePlan
	if err := json.Unmarshal(out.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Coverage.Semantic.Changes) != 2 || p.Coverage.Semantic.Changes[0].Path != "outside.txt" || p.Coverage.Semantic.Changes[1].Path != "sub/a.txt" {
		t.Fatalf("narrowed manifest: %+v", p.Coverage.Semantic.Changes)
	}
}

func TestDefaultSharedDeliveryRetainsOversizedPolicyWithBoundedAtoms(t *testing.T) {
	dir, _ := fixture(t)
	body := "---\nstatus: accepted\napplies_to: ['src/**']\n---\n# Full rule\n" + strings.Repeat("Whole rule with Swedish åäö and <code>.\n", 1500)
	write(t, dir, "doc/adr/0040-full.md", body)
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "large trusted rule")
	base := git(t, dir, "rev-parse", "HEAD")
	write(t, dir, "src/login.tsx", "export const login = false\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "change")
	var out bytes.Buffer
	if err := RunCodeCLI([]string{"--base", base, "--head", "HEAD", "--policy", base, "--repository", "wbern/crm"}, dir, &out); err != nil {
		t.Fatal(err)
	}
	var p struct {
		Diff     string            `json:"diff"`
		Schema   string            `json:"schema"`
		Shared   sharedpacket.Plan `json:"shared_delivery"`
		Legacy   json.RawMessage   `json:"packet_plan"`
		Policies []PolicyRecord    `json:"policies"`
	}
	if err := json.Unmarshal(out.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Schema != "adrian-code-plan-v2" || p.Shared.Schema != sharedpacket.Schema || len(p.Shared.Scopes) != 3 || p.Shared.Limits.FragmentLimitExceeded || len(p.Legacy) != 0 {
		t.Fatalf("shared scope/budget contract missing: schema%s shared%+v", p.Schema, p.Shared)
	}
	if p.Policies[1].Content != body {
		t.Fatal("whole policy truncated")
	}
	var actual CodePlan
	if err := json.Unmarshal(out.Bytes(), &actual); err != nil {
		t.Fatal(err)
	}
	for _, artifact := range p.Shared.Artifacts {
		if artifact.Kind == "global-diff" && (artifact.ByteLength != len(actual.Diff) || artifact.RepresentationSHA256 != actual.DiffSHA256) {
			t.Fatal("global boundary proof does not match canonical diff")
		}
	}
	for _, atom := range p.Shared.Atoms {
		if atom.EstimatedInputTokens > 4096 {
			t.Fatal("fragment exceeded existing budget")
		}
	}
}
