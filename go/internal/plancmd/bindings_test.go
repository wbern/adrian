package plancmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bindingsArgs(base, head string) []string {
	return []string{"--repository", "owner/repo", "--base", base, "--head", head, "--policy", base, "--paths0", "--format", "bindings-json"}
}
func TestBindingsScopesProvenanceAndFingerprint(t *testing.T) {
	dir, _ := fixture(t)
	write(t, dir, "doc/adr/0020-typed.md", "---\nstatus: accepted\napplies_to:\n  - paths: ['src/**', '!src/generated/**']\n    review: {requires: []}\n  - paths: ['removed/**']\n    review: {requires: []}\n---\n# Typed\n\n## Decision\nTyped scopes remain binding.\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "typed trusted policy")
	base := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "rm", "src/login.tsx")
	git(t, dir, "commit", "-qm", "delete route")
	head := git(t, dir, "rev-parse", "HEAD")
	input := []byte("src/z.tsx\x00removed/earlier.tsx\x00src/generated/no.tsx\x00src/z.tsx\x00")
	var out bytes.Buffer
	if err := RunBindingsCLI(bindingsArgs(base, head), dir, bytes.NewReader(input), &out); err != nil {
		t.Fatal(err)
		return
	}
	var p BindingsPlan
	if err := json.Unmarshal(out.Bytes(), &p); err != nil {
		t.Fatal(err)
		return
	}
	if len(p.Changes) != 1 || p.Changes[0].Status != "D" || p.Changes[0].Path != "src/login.tsx" || len(p.Scope.Paths) != 3 || len(p.Accepted) != 2 {
		t.Fatalf("scope/netmanifest conflated: %s", out.String())
		return
	}
	typed := p.Accepted[1]
	if typed.ADR != "0020" || len(typed.Matches) != 2 || strings.Join(typed.Matches[0].Paths, ",") != "src/z.tsx" || strings.Join(typed.Matches[1].Paths, ",") != "removed/earlier.tsx" {
		t.Fatalf("typed scope matching drift: %+v", typed)
		return
	}
	for _, binding := range p.Accepted {
		sha := sha256.Sum256([]byte(binding.Content))
		if binding.SHA256 != hex.EncodeToString(sha[:]) {
			t.Fatalf("policy body digest mismatch: %+v", binding)
			return
		}
	}
	fingerprint := p.Fingerprint
	p.Fingerprint = ""
	want, err := bindingsDigest(p)
	if err != nil {
		t.Fatal(err)
		return
	}
	if fingerprint != want {
		t.Fatal("fingerprint does not bind full producer artifact")
		return
	}
	var again bytes.Buffer
	if err := RunBindingsCLI(bindingsArgs(base, head), dir, strings.NewReader("src/generated/no.tsx\x00src/z.tsx\x00removed/earlier.tsx\x00"), &again); err != nil {
		t.Fatal(err)
		return
	}
	if out.String() != again.String() {
		t.Fatal("scope ordering or duplicate paths change identity")
		return
	}
	var changed bytes.Buffer
	if err := RunBindingsCLI(bindingsArgs(base, head), dir, strings.NewReader("src/z.tsx\x00"), &changed); err != nil {
		t.Fatal(err)
		return
	}
	if out.String() == changed.String() {
		t.Fatal("scope alteration did not change bound artifact")
		return
	}
}
func TestBindingsDoNotMaterializeChangedSource(t *testing.T) {
	dir, base := fixture(t)
	p := filepath.Join(dir, "large-source.bin")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
		return
	}
	if err = f.Truncate(defaultMaxCodePlanBytes + 1); err != nil {
		_ = f.Close()
		t.Fatal(err)
		return
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
		return
	}
	git(t, dir, "add", "large-source.bin")
	git(t, dir, "commit", "-qm", "source exceeds code-plan budget")
	head := git(t, dir, "rev-parse", "HEAD")
	var out bytes.Buffer
	if err = RunBindingsCLI(bindingsArgs(base, head), dir, strings.NewReader("large-source.bin\x00"), &out); err != nil {
		t.Fatal(err)
		return
	}
	var result BindingsPlan
	if err = json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
		return
	}
	if len(result.Changes) != 1 || result.Changes[0].Path != "large-source.bin" || len(result.Accepted) != 0 || out.Len() > 10000 {
		t.Fatalf("source bytes leaked into binding artifact: %d %s", out.Len(), out.String())
		return
	}
}
func TestBindingsValidateUnmatchedCorpusAndAncestry(t *testing.T) {
	for _, bad := range []string{"unmatched-status", "unmatched-utf8", "missing-registry", "untrusted-policy"} {
		t.Run(bad, func(t *testing.T) {
			dir, base := fixture(t)
			switch bad {
			case "unmatched-status":
				write(t, dir, "doc/adr/0099-unmatched.md", "---\nstatus: acceptde\napplies_to: ['elsewhere/**']\n---\n# Bad\n")
			case "unmatched-utf8":
				write(t, dir, "doc/adr/0099-unmatched.md", "---\nstatus: accepted\napplies_to: ['elsewhere/**']\n---\n# Bad body\n\xff\n")
			case "missing-registry":
				git(t, dir, "rm", ".adrian/review-capabilities.yml")
			case "untrusted-policy":
				write(t, dir, "unrelated.txt", "policy only on head\n")
			}
			git(t, dir, "add", ".")
			git(t, dir, "commit", "-qm", "bad policy")
			head := git(t, dir, "rev-parse", "HEAD")
			args := bindingsArgs(head, head)
			if bad == "untrusted-policy" {
				args = bindingsArgs(base, head)
				for i := range args {
					if args[i] == "--policy" {
						args[i+1] = head
					}
				}
			}
			var out bytes.Buffer
			if err := RunBindingsCLI(args, dir, strings.NewReader("src/login.tsx\x00"), &out); err == nil || out.Len() != 0 {
				t.Fatalf("unsafe policy accepted: %v %s", err, out.String())
				return
			}
		})
	}
}
func TestBindingsScopeBudgetAndStrictFlags(t *testing.T) {
	dir, base := fixture(t)
	cases := []struct {
		name  string
		args  []string
		input []byte
	}{{"oversize", bindingsArgs(base, base), bytes.Repeat([]byte{'a'}, maxBindingsScopeBytes+1)}, {"path-count", bindingsArgs(base, base), bytes.Repeat([]byte("a\x00"), maxBindingsScopePaths+1)}, {"missing-required", []string{"--format", "bindings-json"}, []byte("a\x00")}, {"invalid-repo", append([]string{"--repository", "../wrong"}, bindingsArgs(base, base)[2:]...), []byte("a\x00")}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := RunBindingsCLI(c.args, dir, bytes.NewReader(c.input), &out); err == nil || out.Len() != 0 {
				t.Fatalf("invalid request accepted: %v %s", err, out.String())
				return
			}
		})
	}
}

func TestBindingsExplicitLocalRepositoryIdentity(t *testing.T) {
	dir, base := fixture(t)
	var original BindingsPlan
	for _, identity := range []string{"local:engineering.workspace", "local:another-project", "local:" + strings.Repeat("x", 128), "owner/repo"} {
		args := bindingsArgs(base, base)
		args[1] = identity
		var out bytes.Buffer
		if err := RunBindingsCLI(args, dir, strings.NewReader("src/login.tsx\x00"), &out); err != nil {
			t.Fatal(err)
			return
		}
		var p BindingsPlan
		if err := json.Unmarshal(out.Bytes(), &p); err != nil {
			t.Fatal(err)
			return
		}
		wantKind := "local"
		if identity == "owner/repo" {
			wantKind = "github"
		}
		if p.Repository != identity || p.RepositoryKind != wantKind {
			t.Fatalf("wrong explicit project identity: %+v", p)
			return
		}
		if original.Fingerprint == "" {
			original = p
		} else if p.Fingerprint == original.Fingerprint {
			t.Fatal("repository kind/identity not bound into fingerprint")
			return
		}
	}
	for _, identity := range []string{"local:", "LOCAL:project", "local:Uppercase", "local:/absolute", "local:../escape", "local:foo/bar", "local:foo\\bar", "local:" + strings.Repeat("x", 129)} {
		args := bindingsArgs(base, base)
		args[1] = identity
		var out bytes.Buffer
		if err := RunBindingsCLI(args, dir, strings.NewReader("src/login.tsx\x00"), &out); err == nil || out.Len() != 0 {
			t.Fatalf("invalid project identity accepted: %q", identity)
			return
		}
	}
}

func TestBindingsIgnoreLocalDiffOrderFile(t *testing.T) {
	dir, base := fixture(t)
	write(t, dir, "src/a.tsx", "a\n")
	write(t, dir, "src/b.tsx", "b\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "two changed paths")
	head := git(t, dir, "rev-parse", "HEAD")
	args := bindingsArgs(base, head)
	scope := "src/a.tsx\x00src/b.tsx\x00"
	var before bytes.Buffer
	if err := RunBindingsCLI(args, dir, strings.NewReader(scope), &before); err != nil {
		t.Fatal(err)
		return
	}
	order := filepath.Join(t.TempDir(), "reverse.order")
	if err := os.WriteFile(order, []byte("src/b.tsx\nsrc/a.tsx\n"), 0600); err != nil {
		t.Fatal(err)
		return
	}
	git(t, dir, "config", "diff.orderFile", order)
	var after bytes.Buffer
	if err := RunBindingsCLI(args, dir, strings.NewReader(scope), &after); err != nil {
		t.Fatal(err)
		return
	}
	if before.String() != after.String() {
		t.Fatal("local nonpolicy diff.orderFile changed immutable manifest/fingerprint")
		return
	}
}
