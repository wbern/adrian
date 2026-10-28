package plancmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func codeIdentityFixture(t *testing.T, format string) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "--object-format="+format)
	write(t, dir, ".adrian/review-capabilities.yml", "version: 1\ncapabilities:\n  code-judgment: {evidence: []}\n")
	write(t, dir, "doc/adr/0001-rule.md", "---\nstatus: accepted\napplies_to: ['src/**']\n---\n# Stable behavior\n\n## Decision\nPreserve the working entry point.\n")
	write(t, dir, "src/component.tsx", "export const value = 1\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "trusted local policy")
	base := git(t, dir, "rev-parse", "HEAD")
	write(t, dir, "src/component.tsx", "export const value = 2\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "controlled nonempty source change")
	return dir, base, git(t, dir, "rev-parse", "HEAD")
}

func identityPlan(t *testing.T, dir, base, head, kind, repository string) (CodePlan, []byte) {
	t.Helper()
	args := []string{"--repository", repository, "--base", base, "--head", head, "--policy", base}
	if kind != "" {
		args = append(args, "--repository-kind", kind)
	}
	var out bytes.Buffer
	if err := RunCodeCLI(args, dir, &out); err != nil {
		t.Fatal(err)
	}
	var plan CodePlan
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	return plan, out.Bytes()
}

func TestCodePlanKindRejectsInvalidIdentityBeforeGit(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "git-called")
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte("#!/bin/sh\n: > \"$IDENTITY_GIT_CALLED\"\nexit 89\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("IDENTITY_GIT_CALLED", marker)
	cases := [][]string{
		{"--repository-kind"},
		{"--repository-kind", ""},
		{"--repository-kind", "unknown", "--repository", "owner/repo"},
		{"--repository-kind", "local", "--repository-kind", "local", "--repository", "local:gc-chat"},
		{"--repository-kind", "local", "--repository", "owner/repo"},
		{"--repository-kind", "github", "--repository", "local:gc-chat"},
		{"--repository-kind", "local", "--repository", "local:gc-chat", "--legacy-packets"},
		{"--legacy-packets", "--repository", "local:gc-chat", "--repository-kind", "local"},
		{"--repository-kind", "github", "--repository", "owner/repo", "--legacy-packets"},
		{"--legacy-packets", "--repository-kind", "github", "--repository", "owner/repo"},
		{"--repository", "", "--repository", "owner/repo", "--repository-kind", "github"},
		{"--repository-kind", "github", "--repository", "", "--repository", "owner/repo"},
		{"--repository", "local:gc-chat"}, // No implicit V3 opt-in.
	}
	for _, repository := range []string{"local:", "local:" + strings.Repeat("a", 129), "local:Upper", "local:_first", "local:a/b", "local:a\n", "local:a\t", "local:../path"} {
		cases = append(cases, []string{"--repository-kind", "local", "--repository", repository})
	}
	for _, args := range cases {
		var out bytes.Buffer
		err := RunCodeCLI(append(args, "--base", "missing", "--head", "missing", "--policy", "missing"), dir, &out)
		if err == nil || out.Len() != 0 {
			t.Fatalf("invalid explicit identity accepted: %q %v", args, err)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("invalid identity reached Git: %q %v", args, err)
		}
	}
}

func TestCodePlanKindPreservesOmittedFlagCompatibility(t *testing.T) {
	dir, base, head := codeIdentityFixture(t, "sha1")
	for _, repository := range []string{"owner/repo", ".owner/..", "../__"} {
		for _, legacy := range []bool{false, true} {
			args := []string{"--repository", repository, "--base", base, "--head", head, "--policy", base}
			if legacy {
				args = append(args, "--legacy-packets")
			}
			var ordinary, emptyFirst bytes.Buffer
			if err := RunCodeCLI(args, dir, &ordinary); err != nil {
				t.Fatalf("historic syntax was tightened: %v", err)
			}
			// Preserve this old syntactic acceptance only when kind is omitted.
			if err := RunCodeCLI(append([]string{"--repository", ""}, args...), dir, &emptyFirst); err != nil || !bytes.Equal(ordinary.Bytes(), emptyFirst.Bytes()) {
				t.Fatalf("omitted-kind argument behavior changed: %v", err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(ordinary.Bytes(), &fields); err != nil {
				t.Fatal(err)
			}
			if _, present := fields["repository_kind"]; present {
				t.Fatal("V1/V2 gained a repository kind field")
			}
			var schema string
			if err := json.Unmarshal(fields["schema"], &schema); err != nil {
				t.Fatal(err)
			}
			want := "adrian-code-plan-v2"
			if legacy {
				want = "adrian-code-plan-v1"
			}
			if schema != want {
				t.Fatalf("omitted-kind schema changed: %s", schema)
			}
		}
	}
	for _, repository := range []string{"local:a", "local:" + strings.Repeat("a", 128)} {
		plan, _ := identityPlan(t, dir, base, head, "local", repository)
		if plan.Repository != repository || plan.RepositoryKind != "local" || plan.Schema != CodeIdentitySchema {
			t.Fatal("existing local identity boundary was not preserved")
		}
	}
}

func TestCodePlanLocalIdentityRequiresIndependentOuterPin(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			dir, base, head := codeIdentityFixture(t, format)
			first, rawFirst := identityPlan(t, dir, base, head, "local", "local:gc-chat")
			second, rawSecond := identityPlan(t, dir, base, head, "local", "local:another")
			if first.Fingerprint == second.Fingerprint || first.SharedDelivery.Fingerprint != second.SharedDelivery.Fingerprint || first.Coverage.Fingerprint != second.Coverage.Fingerprint {
				t.Fatal("outer namespace or content identity contract changed")
			}
			atom := first.SharedDelivery.Atoms[0].ID
			var firstEnvelope, secondEnvelope bytes.Buffer
			if err := RunMaterializeCLI(materializeTestArgs(first, materializeTestFile(t, rawFirst), atom), &firstEnvelope); err != nil {
				t.Fatal(err)
			}
			fileSecond := materializeTestFile(t, rawSecond)
			if err := RunMaterializeCLI(materializeTestArgs(second, fileSecond, atom), &secondEnvelope); err != nil || !bytes.Equal(firstEnvelope.Bytes(), secondEnvelope.Bytes()) {
				t.Fatalf("valid shared envelope changed with outer namespace: %v", err)
			}
			args := materializeTestArgs(first, fileSecond, atom)
			var refused bytes.Buffer
			err := RunMaterializeCLI(args, &refused)
			if err == nil || refused.Len() != 0 || !strings.Contains(err.Error(), "trusted expectation") {
				t.Fatalf("matching shared identity substituted a foreign outer identity: %v", err)
			}
		})
	}
}

func TestCodePlanExplicitKindProducesVersionedOriginlessIdentity(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		for _, identity := range []struct{ kind, repository string }{{"local", "local:gc-chat"}, {"github", "owner/repo"}} {
			t.Run(format+"/"+identity.kind, func(t *testing.T) {
				dir, base, head := codeIdentityFixture(t, format)
				if git(t, dir, "remote") != "" {
					t.Fatal("fixture is not genuinely originless")
				}
				var out bytes.Buffer
				args := []string{"--repository-kind", identity.kind, "--repository", identity.repository, "--base", base, "--head", head, "--policy", base}
				if err := RunCodeCLI(args, dir, &out); err != nil {
					t.Fatalf("explicit identity cannot produce a code plan: %v", err)
				}
				var plan CodePlan
				if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
					t.Fatal(err)
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(out.Bytes(), &fields); err != nil {
					t.Fatal(err)
				}
				var kind string
				if err := json.Unmarshal(fields["repository_kind"], &kind); err != nil {
					t.Fatal(err)
				}
				if plan.Schema != "adrian-code-plan-v3" || plan.Repository != identity.repository || kind != identity.kind || plan.SharedDelivery == nil || len(plan.SharedDelivery.Atoms) == 0 {
					t.Fatalf("versioned identity/source missing: %s", out.String())
				}
				if git(t, dir, "remote") != "" {
					t.Fatal("planning invented a remote")
				}
			})
		}
	}
}
