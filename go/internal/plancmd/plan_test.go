package plancmd

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wbern/adrian/go/internal/reviewplan"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.test", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.test")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
}
func write(t *testing.T, dir, path, content string) {
	t.Helper()
	p := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
func fixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	write(t, dir, ".adrian/review-capabilities.yml", "version: 1\ncapabilities:\n  visual-product-judgment: {evidence: [rendered-preview]}\n")
	write(t, dir, "doc/adr/0014-mobile.md", "---\nstatus: accepted\napplies_to: ['src/**/*.tsx', '!src/**/*.test.tsx']\nreview: {requires: [visual-product-judgment]}\n---\n# Mobile\n")
	write(t, dir, "src/login.tsx", "export const login = true\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "baseline")
	return dir, git(t, dir, "rev-parse", "HEAD")
}

func TestPinnedPolicyAndRenameManifest(t *testing.T) {
	dir, base := fixture(t)
	git(t, dir, "mv", "src/login.tsx", "renamed with space\nand newline.txt")
	write(t, dir, "doc/adr/0014-mobile.md", "---\nstatus: accepted\napplies_to: ['src/**/*.tsx']\nreview: {requires: []}\n---\n# weakened on head\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "rename and try self waiver")
	head := git(t, dir, "rev-parse", "HEAD")
	var out bytes.Buffer
	if err := RunCLI([]string{"--base", base, "--head", head, "--policy", base, "--format", "json"}, dir, &out); err != nil {
		t.Fatal(err)
	}
	var p reviewplan.Plan
	if err := json.Unmarshal(out.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.HeadCommit != head || p.PolicyCommit != base || len(p.Requirements) != 1 {
		t.Fatalf("incorrect policy plan: %s", out.String())
	}
	if p.Requirements[0].Capability != "visual-product-judgment" {
		t.Fatal("head changed its own policy")
	}
	var found bool
	for _, c := range p.Changes {
		if c.OldPath == "src/login.tsx" && c.Path == "renamed with space\nand newline.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("rename manifest lost paths: %#v", p.Changes)
	}
	var selfWaiver bytes.Buffer
	if err := RunCLI([]string{"--base", base, "--head", head, "--policy", head}, dir, &selfWaiver); err == nil || selfWaiver.Len() != 0 {
		t.Fatal("accepted policy from the PR head outside trusted base history")
	}
	var second bytes.Buffer
	if err := RunCLI([]string{"--base", base, "--head", head, "--policy", base}, dir, &second); err != nil {
		t.Fatal(err)
	}
	if out.String() != second.String() {
		t.Fatal("non deterministic JSON")
	}
}

func TestMissingOrSymlinkedPolicyFailsClosed(t *testing.T) {
	for _, scenario := range []string{"missing registry", "symlinked ADR", "missing frontmatter"} {
		t.Run(scenario, func(t *testing.T) {
			dir, _ := fixture(t)
			switch scenario {
			case "missing registry":
				if err := os.Remove(filepath.Join(dir, ".adrian/review-capabilities.yml")); err != nil {
					t.Fatal(err)
				}
			case "symlinked ADR":
				p := filepath.Join(dir, "doc/adr/0014-mobile.md")
				if err := os.Remove(p); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("../../README.md", p); err != nil {
					t.Fatal(err)
				}
			case "missing frontmatter":
				write(t, dir, "doc/adr/0014-mobile.md", "# Legacy prose cannot silently waive routing\n")
			}
			git(t, dir, "add", "-A")
			git(t, dir, "commit", "-qm", "invalid policy")
			commit := git(t, dir, "rev-parse", "HEAD")
			var out bytes.Buffer
			if err := RunCLI([]string{"--base", commit, "--head", commit, "--policy", commit}, dir, &out); err == nil || out.Len() != 0 {
				t.Fatal("invalid policy published a plan")
			}
		})
	}
}

func TestFailClosedInputs(t *testing.T) {
	dir, base := fixture(t)
	for _, args := range [][]string{
		{"--base", base, "--head", "missing", "--policy", base},
		{"--base", base, "--head", base},
		{"--base", base, "--head", base, "--policy", base, "--file", "src/login.test.tsx"},
		{"--base", base, "--head", base, "--policy", base, "--format", "text"},
		{"--base", base, "--head", base, "--policy", base, "--head", base},
	} {
		var out bytes.Buffer
		if err := RunCLI(args, dir, &out); err == nil {
			t.Fatalf("accepted bad arguments %v", args)
		}
		if out.Len() != 0 {
			t.Fatal("failure published a partial plan")
		}
	}
	write(t, dir, "doc/adr/0099-unrelated.md", "---\nstatus: acceptde\napplies_to: ['other/**']\n---\n# unrelated\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "invalid unrelated policy")
	bad := git(t, dir, "rev-parse", "HEAD")
	var out bytes.Buffer
	if err := RunCLI([]string{"--base", bad, "--head", bad, "--policy", bad}, dir, &out); err == nil {
		t.Fatal("ignored invalid unrelated policy")
	}
}

func TestManifestParsing(t *testing.T) {
	for _, raw := range []string{"M\x00src/a", "R100\x00old\x00", "Q\x00src/a\x00", "M\x00../escape\x00", "R200\x00a\x00b\x00", "M\x00a\x00M\x00a\x00"} {
		if _, err := parseChanges([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid manifest %q", raw)
		}
	}
	changes, err := parseChanges([]byte("D\x00src/deleted.tsx\x00M\x00src/with space\nand newline.tsx\x00"))
	if err != nil || len(changes) != 2 {
		t.Fatalf("valid manifest: %v %v", changes, err)
	}
}

func TestSubmoduleIgnoreCannotHideChangedGitlink(t *testing.T) {
	dir, first := fixture(t)
	write(t, dir, ".gitmodules", "[submodule \"lib\"]\n\tpath = vendor/lib\n\turl = ../lib\n\tignore = all\n")
	git(t, dir, "add", ".gitmodules")
	git(t, dir, "update-index", "--add", "--cacheinfo", "160000,"+first+",vendor/lib")
	git(t, dir, "commit", "-qm", "add gitlink")
	base := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "update-index", "--cacheinfo", "160000,"+base+",vendor/lib")
	git(t, dir, "commit", "-qm", "update gitlink")
	head := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "config", "diff.ignoreSubmodules", "all")
	if got := git(t, dir, "diff", "--name-only", base, head); got != "" {
		t.Fatalf("control must demonstrate ignored gitlink, got %q", got)
	}
	var out bytes.Buffer
	if err := RunCLI([]string{"--base", base, "--head", head, "--policy", base}, dir, &out); err != nil {
		t.Fatal(err)
	}
	var p reviewplan.Plan
	if err := json.Unmarshal(out.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Changes) != 1 || p.Changes[0].Path != "vendor/lib" {
		t.Fatalf("gitlink was omitted: %s", out.String())
	}
}

func TestCorpusSelectionUsesTrustedPolicyTree(t *testing.T) {
	for _, layout := range []string{"docs/adr", "architecture/decisions/records", "architecture/[special]"} {
		t.Run(layout, func(t *testing.T) {
			dir, _ := fixture(t)
			if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(layout)), 0755); err != nil {
				t.Fatal(err)
			}
			git(t, dir, "mv", "doc/adr", layout)
			if layout != "docs/adr" {
				write(t, dir, ".adrian.toml", "version = 1\n[corpus]\ndir = \""+layout+"\"\n")
			}
			git(t, dir, "add", ".")
			git(t, dir, "commit", "-qm", "native corpus")
			base := git(t, dir, "rev-parse", "HEAD")
			write(t, dir, "src/login.tsx", "export const login = false\n")
			write(t, dir, ".adrian.toml", "[corpus]\ndir = \"hostile/policy\"\n")
			write(t, dir, "hostile/policy/0099-waiver.md", "---\nstatus: accepted\napplies_to: ['**']\nreview: {requires: []}\n---\n# Self waiver\n")
			git(t, dir, "add", ".")
			git(t, dir, "commit", "-qm", "head redirect")
			head := git(t, dir, "rev-parse", "HEAD")
			write(t, dir, ".adrian.toml", "[corpus]\ndir = \"dirty/policy\"\n")
			for _, code := range []bool{false, true} {
				var out bytes.Buffer
				args := []string{"--base", base, "--head", head, "--policy", base}
				var err error
				if code {
					args = append(args, "--repository", "owner/repo")
					err = RunCodeCLI(args, dir, &out)
				} else {
					err = RunCLI(args, dir, &out)
				}
				if err != nil {
					t.Fatal(err)
				}
				if code {
					var p CodePlan
					if e := json.Unmarshal(out.Bytes(), &p); e != nil {
						t.Fatal(e)
					}
					if len(p.Policies) != 1 || p.Policies[0].Path != layout+"/0014-mobile.md" {
						t.Fatalf("wrong code policy: %s", out.String())
					}
				} else {
					var p reviewplan.Plan
					if e := json.Unmarshal(out.Bytes(), &p); e != nil {
						t.Fatal(e)
					}
					if len(p.Requirements) != 1 || len(p.Requirements[0].Sources) != 1 || p.Requirements[0].Sources[0].File != layout+"/0014-mobile.md" {
						t.Fatalf("wrong plan policy: %s", out.String())
					}
				}
			}
		})
	}
}
func TestPolicyCorpusRejectsSymlinkAndAmbiguity(t *testing.T) {
	for _, mode := range []string{"config-symlink", "corpus-symlink", "ambiguous", "empty"} {
		t.Run(mode, func(t *testing.T) {
			dir, _ := fixture(t)
			switch mode {
			case "config-symlink":
				write(t, dir, "config.toml", "[corpus]\ndir = \"doc/adr\"\n")
				if err := os.Symlink("config.toml", filepath.Join(dir, ".adrian.toml")); err != nil {
					t.Fatal(err)
				}
			case "corpus-symlink":
				git(t, dir, "mv", "doc/adr", "records")
				if err := os.Symlink("../records", filepath.Join(dir, "doc/adr")); err != nil {
					t.Fatal(err)
				}
			case "ambiguous":
				write(t, dir, "docs/adr/README.md", "second corpus\n")
			case "empty":
				git(t, dir, "rm", "doc/adr/0014-mobile.md")
				write(t, dir, "doc/adr/README.md", "empty\n")
			}
			git(t, dir, "add", ".")
			git(t, dir, "commit", "-qm", "bad corpus")
			base := git(t, dir, "rev-parse", "HEAD")
			var out bytes.Buffer
			if err := RunCLI([]string{"--base", base, "--head", base, "--policy", base}, dir, &out); err == nil || out.Len() != 0 {
				t.Fatalf("accepted unsafe corpus %s: %s", mode, out.String())
			}
		})
	}
}
