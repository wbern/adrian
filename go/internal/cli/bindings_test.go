package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func bindingGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.test", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.test")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
		return ""
	}
	return strings.TrimSpace(string(out))
}
func bindingInput(t *testing.T, input []byte) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "scope")
	if err != nil {
		t.Fatal(err)
		return
	}
	if _, err = f.Write(input); err != nil {
		t.Fatal(err)
		return
	}
	if _, err = f.Seek(0, 0); err != nil {
		t.Fatal(err)
		return
	}
	old := os.Stdin
	os.Stdin = f
	t.Cleanup(func() { os.Stdin = old; _ = f.Close() })
}
func bindingFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := corpusRepo(t)
	corpusWrite(t, root, ".adrian/review-capabilities.yml", "version: 1\ncapabilities:\n  code-judgment: {evidence: []}\n")
	corpusWrite(t, root, "doc/adr/0001-rule.md", "---\nstatus: accepted\napplies_to: ['src/**', '!src/generated/**']\npre_filter: ['never-present']\nenforced_by: existing-script\n---\n# Rule\n\n## Decision\nTrusted original policy.\n")
	corpusWrite(t, root, "doc/adr/0002-proposal.md", "---\nstatus: proposed\napplies_to: ['intermediate/**']\n---\n# Proposal\n")
	corpusWrite(t, root, "doc/adr/0003-old.md", "---\nstatus: superseded\nsuperseded_by: ['0001']\napplies_to: ['src/**']\n---\n# Old\n")
	corpusWrite(t, root, "src/old.go", "original\n")
	bindingGit(t, root, "add", ".")
	bindingGit(t, root, "commit", "-qm", "baseline")
	base := bindingGit(t, root, "rev-parse", "HEAD")
	if err := os.MkdirAll(filepath.Join(root, "other"), 0755); err != nil {
		t.Fatal(err)
		return "", "", ""
	}
	bindingGit(t, root, "mv", "src/old.go", "other/new.go")
	corpusWrite(t, root, "doc/adr/0001-rule.md", "---\nstatus: accepted\napplies_to: ['unrelated/**']\n---\n# Weakened head policy\n")
	bindingGit(t, root, "add", ".")
	bindingGit(t, root, "commit", "-qm", "rename and weaken head")
	return root, base, bindingGit(t, root, "rev-parse", "HEAD")
}
func TestImmutableBindingsAtCLIBoundary(t *testing.T) {
	for _, name := range []string{"adrian", "adr-lint"} {
		t.Run(name, func(t *testing.T) {
			root, base, head := bindingFixture(t)
			corpusWrite(t, root, ".adrian.toml", "[corpus]\ndir='hostile/policy'\n")
			foreign := t.TempDir()
			bindingGit(t, foreign, "init", "-q")
			t.Setenv("GIT_DIR", filepath.Join(foreign, ".git"))
			t.Setenv("GIT_WORK_TREE", foreign)
			corpusWrite(t, root, ".env.local", "this would produce a legacy dotenv warning\n")
			bindingInput(t, []byte("src/old.go\x00intermediate/transient.go\x00src/generated/skip.go\x00src/old.go\x00other/new.go\x00"))
			var out, err bytes.Buffer
			args := []string{"resolve", "--repository", "owner/repo", "--base", base, "--head", head, "--policy", base, "--paths0", "--format", "bindings-json"}
			if code := Run(name, args, &out, &err); code != 0 {
				t.Fatalf("immutable resolve exit%d: %s", code, err.String())
				return
			}
			var p struct {
				Schema         string `json:"schema"`
				Repository     string `json:"repository"`
				Kind           string `json:"repository_kind"`
				Policy         string `json:"policy_commit"`
				ManifestDigest string `json:"manifest_digest"`
				Scope          struct {
					Kind   string   `json:"kind"`
					Paths  []string `json:"paths"`
					Digest string   `json:"digest"`
				} `json:"scope"`
				Accepted []struct {
					ADR     string `json:"adr"`
					Content string `json:"content"`
					SHA     string `json:"sha256"`
					Matches []struct {
						Paths []string `json:"paths"`
					} `json:"matches"`
				} `json:"accepted"`
				Proposed []struct {
					ADR string `json:"adr"`
				} `json:"proposed"`
				Changes []struct {
					Path    string `json:"path"`
					OldPath string `json:"old_path"`
				} `json:"changes"`
				Fingerprint string `json:"fingerprint"`
			}
			if e := json.Unmarshal(out.Bytes(), &p); e != nil {
				t.Fatal(e)
				return
			}
			if p.Schema != "adrian-bindings-v1" || p.Repository != "owner/repo" || p.Kind != "github" || p.Policy != base || len(p.ManifestDigest) != 64 || len(p.Fingerprint) != 64 || p.Scope.Kind != "caller-selected-paths" || len(p.Scope.Paths) != 4 || len(p.Scope.Digest) != 64 {
				t.Fatalf("missing identities: %s", out.String())
				return
			}
			if len(p.Accepted) != 1 || p.Accepted[0].ADR != "0001" || !strings.Contains(p.Accepted[0].Content, "Trusted original policy") || len(p.Accepted[0].SHA) != 64 || len(p.Proposed) != 1 || p.Proposed[0].ADR != "0002" {
				t.Fatalf("wrong binding authority/body: %s", out.String())
				return
			}
			if len(p.Accepted[0].Matches) != 1 || strings.Join(p.Accepted[0].Matches[0].Paths, ",") != "src/old.go" {
				t.Fatalf("wrong exclusions: %s", out.String())
				return
			}
			found := false
			for _, c := range p.Changes {
				if c.Path == "other/new.go" && c.OldPath == "src/old.go" {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing complete rename manifest: %s", out.String())
				return
			}
		})
	}
}
func TestImmutableBindingsRejectInvalidScopeAndOptions(t *testing.T) {
	_, base, head := bindingFixture(t)
	valid := []string{"resolve", "--repository", "owner/repo", "--base", base, "--head", head, "--policy", base, "--paths0", "--format", "bindings-json"}
	for _, input := range [][]byte{nil, []byte("src/a.go"), []byte("\x00"), []byte("../escape\x00"), []byte("/absolute\x00"), []byte("a//b\x00"), []byte("a/./b\x00"), {0xff, 0}} {
		t.Run(string(input), func(t *testing.T) {
			bindingInput(t, input)
			var out, err bytes.Buffer
			if code := Run("adrian", valid, &out, &err); code == 0 || out.Len() != 0 {
				t.Fatalf("scope accepted: %d %s %s", code, out.String(), err.String())
				return
			}
		})
	}
	for _, extra := range [][]string{{"--policy", base}, {"--paths0"}, {"--ranked"}, {"--tag", "ux"}, {"src/a.go"}, {"--adr-dir", "doc/adr"}, {"--staged"}} {
		t.Run(strings.Join(extra, "_"), func(t *testing.T) {
			bindingInput(t, []byte("src/a.go\x00"))
			var out, err bytes.Buffer
			if code := Run("adrian", append(append([]string{}, valid...), extra...), &out, &err); code == 0 || out.Len() != 0 {
				t.Fatalf("mixed/duplicate options accepted: %d %s %s", code, out.String(), err.String())
				return
			}
		})
	}
}

func TestImmutableBindingsOriginlessLocalAliases(t *testing.T) {
	for _, name := range []string{"adrian", "adr-lint"} {
		t.Run(name, func(t *testing.T) {
			_, base, head := bindingFixture(t)
			bindingInput(t, []byte("src/old.go\x00"))
			var out, err bytes.Buffer
			if code := Run(name, []string{"resolve", "--repository", "local:explicit-project", "--base", base, "--head", head, "--policy", base, "--paths0", "--format", "bindings-json"}, &out, &err); code != 0 {
				t.Fatalf("originless local binding failed: %d %s", code, err.String())
				return
			}
			var p struct {
				Kind       string `json:"repository_kind"`
				Repository string `json:"repository"`
			}
			if e := json.Unmarshal(out.Bytes(), &p); e != nil {
				t.Fatal(e)
				return
			}
			if p.Kind != "local" || p.Repository != "local:explicit-project" {
				t.Fatalf("project identity inferred incorrectly: %s", out.String())
				return
			}
		})
	}
}
