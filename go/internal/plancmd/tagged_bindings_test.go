package plancmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func taggedArgs(base, head, tag string) []string {
	args := bindingsArgs(base, head)
	args[len(args)-1] = "bindings-json-v2"
	return append(args, "--tag", tag)
}

func TestTaggedBindingsValidateEveryPolicyWithoutTighteningV1(t *testing.T) {
	dir, _ := fixture(t)
	for _, status := range []string{"accepted", "proposed", "withdrawn", "deprecated", "rejected", "superseded"} {
		successor := ""
		if status == "superseded" {
			successor = "superseded_by: [0014]\n"
		}
		write(t, dir, "doc/adr/0099-unmatched.md", "---\n"+successor+"status: "+status+"\ntags: [ux, false]\napplies_to: ['unmatched/**']\n---\n# Unmatched\n")
		git(t, dir, "add", ".")
		git(t, dir, "commit", "-qm", "malformed unmatched "+status)
		policy := git(t, dir, "rev-parse", "HEAD")
		var old, tagged bytes.Buffer
		if err := RunBindingsCLI(bindingsArgs(policy, policy), dir, strings.NewReader("src/login.tsx\x00"), &old); err != nil {
			t.Fatalf("v1 acceptance changed for %s: %v", status, err)
			return
		}
		err := RunBindingsCLI(taggedArgs(policy, policy, "ux"), dir, strings.NewReader("src/login.tsx\x00"), &tagged)
		if err == nil || !strings.Contains(err.Error(), "0099-unmatched.md: invalid tags") || tagged.Len() != 0 {
			t.Fatalf("%s malformed corpus not refused: %v %s", status, err, tagged.String())
			return
		}
	}
}

func TestTaggedBindingsSelectionFingerprintAndCompleteScope(t *testing.T) {
	dir, _ := fixture(t)
	write(t, dir, "doc/adr/0014-mobile.md", "---\nstatus: accepted\ntags: [ux, ' ux ', UX]\napplies_to:\n  - paths: ['src/**', '!src/generated/**']\n    review: {requires: []}\n  - paths: ['intermediate/**']\n    review: {requires: []}\n---\n# Original\n")
	write(t, dir, "doc/adr/0015-empty.md", "---\nstatus: proposed\ntags: []\napplies_to: ['**']\n---\n# Empty tags\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "tagged policy")
	policy := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "mv", "src/login.tsx", "renamed.tsx")
	git(t, dir, "commit", "-qm", "rename")
	head := git(t, dir, "rev-parse", "HEAD")
	scope := "src/login.tsx\x00renamed.tsx\x00intermediate/earlier.tsx\x00src/generated/skip.tsx\x00"
	var full bytes.Buffer
	if err := RunBindingsCLI(bindingsArgs(policy, head), dir, strings.NewReader(scope), &full); err != nil {
		t.Fatal(err)
		return
	}
	var v1 BindingsPlan
	if err := json.Unmarshal(full.Bytes(), &v1); err != nil {
		t.Fatal(err)
		return
	}
	for _, tag := range []string{"ux", "UX", "none", "other"} {
		var out bytes.Buffer
		if err := RunBindingsCLI(taggedArgs(policy, head, tag), dir, strings.NewReader(scope), &out); err != nil {
			t.Fatal(err)
			return
		}
		var p BindingsPlan
		if err := json.Unmarshal(out.Bytes(), &p); err != nil {
			t.Fatal(err)
			return
		}
		if p.Selection == nil || p.Selection.Tag != tag || p.Schema != TaggedBindingsSchema || p.ManifestDigest != v1.ManifestDigest || p.Scope.Digest != v1.Scope.Digest || len(p.PolicyManifest) != len(v1.PolicyManifest) || len(p.Changes) != 1 || p.Changes[0].OldPath != "src/login.tsx" {
			t.Fatalf("selection changed independent identities: %s", out.String())
			return
		}
		if tag == "ux" || tag == "UX" {
			if len(p.Accepted) != 1 || len(p.Accepted[0].Matches) != 2 || strings.Join(p.Accepted[0].Matches[0].Paths, ",") != "src/login.tsx" || strings.Join(p.Accepted[0].Matches[1].Paths, ",") != "intermediate/earlier.tsx" {
				t.Fatalf("wrong scopes: %s", out.String())
				return
			}
		} else if len(p.Accepted) != 0 || len(p.Proposed) != 0 {
			t.Fatalf("empty selection promoted a binding: %s", out.String())
			return
		}
		got := p.Fingerprint
		p.Fingerprint = ""
		want, err := bindingsDigest(p)
		if err != nil || got != want {
			t.Fatalf("selector not fingerprinted: %v %s", err, out.String())
			return
		}
		p.Selection.Tag = "different"
		changed, err := bindingsDigest(p)
		if err != nil || changed == got {
			t.Fatalf("selection absent from fingerprint: %v", err)
			return
		}
	}
}

func TestTaggedBindingsObjectFormatsAndCustomCorpus(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			dir := t.TempDir()
			cmd := exec.Command("git", "init", "-q", "--object-format="+format, dir)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("controlled %s git init: %v %s", format, err, out)
				return
			}
			write(t, dir, ".adrian.toml", "[corpus]\ndir='architecture/[policy]'\n")
			write(t, dir, ".adrian/review-capabilities.yml", "version: 1\ncapabilities:\n  code-judgment: {}\n")
			policyPath := "architecture/[policy]/0001-ux.md"
			original := "---\nstatus: accepted\ntags:\n  - ' ux '\n  - ux\napplies_to: ['src/**']\n---\n# Original Unicode Ω\n"
			write(t, dir, policyPath, original)
			write(t, dir, "src/old.go", "stable source\n")
			git(t, dir, "add", ".")
			git(t, dir, "commit", "-qm", "custom trusted corpus")
			policy := git(t, dir, "rev-parse", "HEAD")
			git(t, dir, "mv", "src/old.go", "outside.go")
			write(t, dir, policyPath, strings.ReplaceAll(original, "ux", "code"))
			git(t, dir, "add", ".")
			git(t, dir, "commit", "-qm", "rename and change head tags")
			head := git(t, dir, "rev-parse", "HEAD")
			write(t, dir, ".adrian.toml", "[corpus]\ndir='not/the/policy'\n")
			write(t, dir, policyPath, "dirty malformed policy")
			args := taggedArgs(policy, head, "ux")
			args[1] = "local:originless-project"
			var out bytes.Buffer
			if err := RunBindingsCLI(args, dir, strings.NewReader("src/old.go\x00outside.go\x00intermediate/earlier.go\x00"), &out); err != nil {
				t.Fatal(err)
				return
			}
			var p BindingsPlan
			if err := json.Unmarshal(out.Bytes(), &p); err != nil {
				t.Fatal(err)
				return
			}
			sum := sha256.Sum256([]byte(original))
			wantLen := 40
			if format == "sha256" {
				wantLen = 64
			}
			if p.RepositoryKind != "local" || len(p.BaseCommit) != wantLen || p.PolicyCommit != policy || len(p.Accepted) != 1 || p.Accepted[0].Path != policyPath || p.Accepted[0].Content != original || p.Accepted[0].SHA256 != hex.EncodeToString(sum[:]) || len(p.Accepted[0].Matches) != 1 || strings.Join(p.Accepted[0].Matches[0].Paths, ",") != "src/old.go" {
				t.Fatalf("immutable object-format/local/custom corpus failure: %s", out.String())
				return
			}
		})
	}
}
