package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestTaggedBindingsImmutableCLIBoundary(t *testing.T) {
	for _, name := range []string{"adrian", "adr-lint"} {
		t.Run(name, func(t *testing.T) {
			root, _, _ := bindingFixture(t)
			corpusWrite(t, root, "doc/adr/0001-rule.md", "---\nstatus: accepted\ntags: [ux, security, ux]\napplies_to: ['src/**', '!src/generated/**']\n---\n# Trusted UX body\n")
			corpusWrite(t, root, "doc/adr/0002-proposal.md", "---\nstatus: proposed\ntags: ux\napplies_to: ['intermediate/**']\n---\n# UX proposal\n")
			corpusWrite(t, root, "doc/adr/0004-code.md", "---\nstatus: accepted\ntags: [code]\napplies_to: ['src/**']\n---\n# Code only\n")
			corpusWrite(t, root, "src/deleted.go", "deleted\n")
			bindingGit(t, root, "add", ".")
			bindingGit(t, root, "commit", "-qm", "trusted tagged corpus")
			policy := bindingGit(t, root, "rev-parse", "HEAD")
			bindingGit(t, root, "rm", "src/deleted.go")
			corpusWrite(t, root, "doc/adr/0001-rule.md", "---\nstatus: accepted\ntags: [code]\napplies_to: ['nothing/**']\n---\n# Weakened head\n")
			bindingGit(t, root, "add", ".")
			bindingGit(t, root, "commit", "-qm", "delete and weaken tags")
			head := bindingGit(t, root, "rev-parse", "HEAD")
			corpusWrite(t, root, ".adrian.toml", "[corpus]\ndir='hostile'\n")
			corpusWrite(t, root, ".env.local", "invalid dotenv legacy marker\n")
			bindingInput(t, []byte("src/deleted.go\x00intermediate/earlier.go\x00src/generated/skip.go\x00"))
			var out, errs bytes.Buffer
			args := []string{"resolve", "--repository", "local:tagged-project", "--base", policy, "--head", head, "--policy", policy, "--paths0", "--format", "bindings-json-v2", "--tag", "ux"}
			if code := Run(name, args, &out, &errs); code != 0 {
				t.Fatalf("tagged immutable resolve exit %d: %s", code, errs.String())
				return
			}
			var data struct {
				Schema    string `json:"schema"`
				Selection struct {
					Kind string `json:"kind"`
					Tag  string `json:"tag"`
				} `json:"selection"`
				Accepted []struct {
					ADR     string `json:"adr"`
					Content string `json:"content"`
				} `json:"accepted"`
				Proposed []struct {
					ADR string `json:"adr"`
				} `json:"proposed"`
				PolicyManifest []any `json:"policy_manifest"`
				Changes        []struct {
					Status string `json:"status"`
					Path   string `json:"path"`
				} `json:"changes"`
			}
			if err := json.Unmarshal(out.Bytes(), &data); err != nil {
				t.Fatal(err)
				return
			}
			if data.Schema != "adrian-bindings-v2" || data.Selection.Kind != "tag" || data.Selection.Tag != "ux" || len(data.Accepted) != 1 || data.Accepted[0].ADR != "0001" || !strings.Contains(data.Accepted[0].Content, "Trusted UX body") || len(data.Proposed) != 1 || data.Proposed[0].ADR != "0002" || len(data.PolicyManifest) != 4 {
				t.Fatalf("wrong tagged authority: %s", out.String())
				return
			}
			found := false
			for _, c := range data.Changes {
				if c.Status == "D" && c.Path == "src/deleted.go" {
					found = true
				}
			}
			if !found || strings.Contains(errs.String(), "dotenv") {
				t.Fatalf("manifest or early-dispatch failure: %s %s", out.String(), errs.String())
				return
			}
		})
	}
}

func TestTaggedBindingsEarlyDispatchAndStrictOptions(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, args := range [][]string{
		{"resolve", "--format", "bindings-json-v2"},
		{"resolve", "--format=bindings-json-v2"},
		{"for-files", "--format", "bindings-json-v2"},
	} {
		var out, errs bytes.Buffer
		if code := Run("adrian", args, &out, &errs); code != 1 || strings.Contains(errs.String(), "not a git repository") || !strings.Contains(errs.String(), "immutable binding") && !strings.Contains(errs.String(), "is required") {
			t.Fatalf("not routed early: %v %d %s", args, code, errs.String())
			return
		}
	}
	_, base, head := bindingFixture(t)
	valid := []string{"resolve", "--repository", "owner/repo", "--base", base, "--head", head, "--policy", base, "--paths0", "--format", "bindings-json-v2"}
	cases := [][]string{valid, append(append([]string{}, valid...), "--tag", "ux", "--tag", "ux"), append(append([]string{}, valid...), "--tag", "ux", "--ranked"), append(append([]string{}, valid...), "--tag", "ux", "src/a.go")}
	for _, tag := range []string{"", " ", " ux", "ux ", "\tux", "ux\n", "\u0085ux"} {
		cases = append(cases, append(append([]string{}, valid...), "--tag", tag))
	}
	v1 := append([]string{}, valid...)
	v1[len(v1)-1] = "bindings-json"
	cases = append(cases, append(v1, "--tag", "ux"))
	for _, args := range cases {
		var out, errs bytes.Buffer
		if code := Run("adrian", args, &out, &errs); code == 0 || out.Len() != 0 {
			t.Fatalf("accepted invalid combination: %v %s %s", args, out.String(), errs.String())
			return
		}
	}
}
