package runner

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wbern/adrian/go/internal/adr"
	"github.com/wbern/adrian/go/internal/cache"
	"github.com/wbern/adrian/go/internal/diffstats"
	"github.com/wbern/adrian/go/internal/gitcontext"
	"github.com/wbern/adrian/go/internal/types"
)

const renameOldRuleDiff = "diff --git a/cmd/gc/cmd_handoff.go b/legacy/handoff.go\nsimilarity index 80%\nrename from cmd/gc/cmd_handoff.go\nrename to legacy/handoff.go\n--- a/cmd/gc/cmd_handoff.go\n+++ b/legacy/handoff.go\n@@ -1 +1 @@\n-func handoff() { return }\n+func handoff() { doHandoffWithRecycleOutcome() }\n"

func TestRun_RenameKeepsTrustedOldPathADR(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "doc/adr")
	if e := os.MkdirAll(p, 0755); e != nil {
		t.Fatal(e)
		return
	}
	raw, e := os.ReadFile("testdata/trusted-core-0006.md")
	if e != nil {
		t.Fatal(e)
		return
	}
	if e = os.WriteFile(filepath.Join(p, "0006-trusted.md"), raw, 0644); e != nil {
		t.Fatal(e)
		return
	}
	calls := 0
	fake := func(a adr.ADR, diff string) (types.LintResult, error) {
		calls++
		if !strings.Contains(a.Decision, "--recycle") || diff != renameOldRuleDiff {
			t.Errorf("trusted policy or exact diff lost")
		}
		return types.LintResult{ADR: a, Status: types.StatusFAIL, Explanation: "implicit self-recycle violates trusted ADR-0006"}, nil
	}
	var output bytes.Buffer
	code, e := Run(types.LintOptions{Provider: types.ProviderClaude, DiffSet: true, DiffPath: "-", NoPreFilter: true, NoCache: true, Format: "json"}, RunDeps{Out: &output, Err: &output, Git: fakeGit(root, nil), In: strings.NewReader(renameOldRuleDiff), CacheDir: t.TempDir(), LintFns: map[types.Provider]cache.LintFn{types.ProviderClaude: fake}})
	t.Logf("code=%d calls=%d err=%v output=%q", code, calls, e, output.String())
	if e != nil || calls != 1 || code != 1 {
		t.Fatalf("rename silently lost trusted old-path binding: code=%d calls=%d err=%v", code, calls, e)
		return
	}
}
func TestSliceSuppliedDiff_OldRenameEndpoint(t *testing.T) {
	got := sliceSuppliedDiff(diffstats.Split(renameOldRuleDiff), []string{"cmd/gc/cmd_handoff.go"})
	if got != renameOldRuleDiff {
		t.Fatalf("old-path ADR receives %d bytes of %d-byte actual diff", len(got), len(renameOldRuleDiff))
		return
	}
}

func TestRun_StagedAndBranchRenameKeepOldBinding(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		branch      bool
		filter      string
		destination string
	}{
		{"staged", false, "", "legacy/handoff.go"},
		{"branch", true, "", "legacy/handoff.go"},
		{"branch-old-gone", true, "cmd/gc/cmd_handoff.go", "legacy/handoff.go"},
		{"branch-new-literal", true, "legacy/new[1]*?.go", "legacy/new[1]*?.go"},
		{"branch-unclosed-bracket-literal", true, "legacy/new[.go", "legacy/new[.go"},
		{"staged-excluded-destination", false, "", "legacy/generated/handoff.go"},
	} {
		name := scenario.name
		branch := scenario.branch
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			g := func(args ...string) {
				cmd := exec.Command("git", args...)
				cmd.Dir = root
				cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.test", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.test")
				if out, e := cmd.CombinedOutput(); e != nil {
					t.Fatalf("git%v:%s %v", args, out, e)
					return
				}
			}
			g("init", "-q", "-b", "main")
			g("config", "diff.renames", "true")
			for _, p := range []string{"doc/adr", "cmd/gc", filepath.Dir(scenario.destination)} {
				if e := os.MkdirAll(filepath.Join(root, p), 0755); e != nil {
					t.Fatal(e)
					return
				}
			}
			raw, e := os.ReadFile("testdata/trusted-core-0006.md")
			if e != nil {
				t.Fatal(e)
				return
			}
			if e = os.WriteFile(filepath.Join(root, "doc/adr/0006-trusted.md"), raw, 0644); e != nil {
				t.Fatal(e)
				return
			}
			content := "package gc\n" + strings.Repeat("// stable content padding\n", 30) + "func handoff() { return }\n"
			old := filepath.Join(root, "cmd/gc/cmd_handoff.go")
			newp := filepath.Join(root, scenario.destination)
			if e = os.WriteFile(old, []byte(content), 0644); e != nil {
				t.Fatal(e)
				return
			}
			g("add", ".")
			g("commit", "-qm", "baseline")
			g("checkout", "-qb", "feature")
			g("mv", "cmd/gc/cmd_handoff.go", scenario.destination)
			if e = os.WriteFile(newp, []byte(strings.Replace(content, "func handoff() { return }", "func handoff() { doHandoffWithRecycleOutcome() }", 1)), 0644); e != nil {
				t.Fatal(e)
				return
			}
			g("add", ".")
			if branch {
				g("commit", "-qm", "renamed implicit recycle")
			}
			t.Chdir(root)
			calls := 0
			fake := func(a adr.ADR, diff string) (types.LintResult, error) {
				calls++
				if !strings.Contains(diff, "rename from cmd/gc/cmd_handoff.go") || !strings.Contains(diff, "rename to "+scenario.destination) || !strings.Contains(diff, "+func handoff() { doHandoffWithRecycleOutcome() }") {
					t.Error("model did not receive actual complete rename+violation section")
				}
				if strings.Join(a.MatchedFiles, ",") != "cmd/gc/cmd_handoff.go" {
					t.Errorf("counterpart incorrectly declared an applicable old-rule path: %v", a.MatchedFiles)
				}
				return types.LintResult{ADR: a, Status: types.StatusFAIL, Explanation: "implicit recycle"}, nil
			}
			var buf bytes.Buffer
			opts := types.LintOptions{Provider: types.ProviderClaude, BranchSet: branch, NoPreFilter: true, NoCache: true}
			if scenario.filter != "" {
				opts.Files = []string{scenario.filter}
			}
			code, e := Run(opts, RunDeps{Out: &buf, Err: &buf, Git: gitcontext.NewDefaultClient(), CacheDir: t.TempDir(), LintFns: map[types.Provider]cache.LintFn{types.ProviderClaude: fake}})
			t.Logf("%s code=%d calls=%d err=%v out=%q", name, code, calls, e, buf.String())
			if e != nil || code != 1 || calls != 1 {
				t.Fatalf("normal %s rename lost old-path ADR", name)
				return
			}
		})
	}
}

func TestSuppliedRenameSlicesEachSectionOnceAndKeepsDelete(t *testing.T) {
	pure := "diff --git a/old.go b/new.go\nsimilarity index 100%\nrename from old.go\nrename to new.go"
	deleted := "diff --git a/gone.go b/gone.go\ndeleted file mode 100644\n--- a/gone.go\n+++ /dev/null\n@@ -1 +0,0 @@\n-old\n"
	files := diffstats.Split(deleted + pure)
	paths, _, err := changedFilesFromDiff(files)
	if err != nil {
		t.Fatal(err)
		return
	}
	if strings.Join(paths, ",") != "gone.go,new.go,old.go" {
		t.Fatalf("wrong endpoint union: %v", paths)
		return
	}
	if got := sliceSuppliedDiff(files, []string{"old.go", "new.go"}); got != pure {
		t.Fatalf("rename duplicated/reformatted: %q", got)
		return
	}
	if got := sliceSuppliedDiff(files, []string{"gone.go"}); got != deleted {
		t.Fatalf("deletion changed: %q", got)
		return
	}
}
func TestSuppliedRenameEndpointNegationDoesNotCancelOtherEndpoint(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "doc/adr")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
		return
	}
	policy := "---\nstatus: accepted\napplies_to:\n  - paths: ['cmd/**', '!legacy/**']\n  - paths: ['legacy/**', '!cmd/**']\n---\n# 1. Endpoint scopes\n\n## Decision\nCheck added code.\n"
	if err := os.WriteFile(filepath.Join(dir, "0001-rule.md"), []byte(policy), 0644); err != nil {
		t.Fatal(err)
		return
	}
	calls := 0
	fake := func(a adr.ADR, diff string) (types.LintResult, error) {
		calls++
		if strings.Join(a.MatchedFiles, ",") != "cmd/gc/cmd_handoff.go,legacy/handoff.go" || diff != renameOldRuleDiff {
			t.Error("one endpoint exclusion canceled a matching independent scope or duplicated the diff")
		}
		return types.LintResult{ADR: a, Status: types.StatusFAIL}, nil
	}
	var buf bytes.Buffer
	code, err := Run(types.LintOptions{DiffSet: true, DiffPath: "-", Provider: types.ProviderClaude, NoCache: true, NoPreFilter: true}, RunDeps{Out: &buf, Err: &buf, In: strings.NewReader(renameOldRuleDiff), Git: noGit(t, root), CacheDir: t.TempDir(), LintFns: map[types.Provider]cache.LintFn{types.ProviderClaude: fake}})
	if err != nil || code != 1 || calls != 1 {
		t.Fatalf("endpoint rule was missed: code%d calls%d err%v", code, calls, err)
		return
	}
}
func TestRun_ManifestQueryAndParseFailuresAreNonzero(t *testing.T) {
	for _, raw := range []string{"query-error", "M\x00missing-terminator", "R100\x00old\x00"} {
		t.Run(raw, func(t *testing.T) {
			root := t.TempDir()
			writeADR(t, filepath.Join(root, "doc/adr"), "0001", "Rule", "lite", "**")
			client := gitcontext.NewClient(func([]string) (string, error) {
				if raw == "query-error" {
					return "", fmt.Errorf("failed query")
				}
				return raw, nil
			})
			client.SetGitRoot(root)
			var out bytes.Buffer
			code, err := Run(types.LintOptions{Provider: types.ProviderClaude}, RunDeps{Out: &out, Err: &out, Git: client})
			if err == nil || code != 1 {
				t.Fatalf("incomplete query passed: code%d err%v out%q", code, err, out.String())
				return
			}
		})
	}
}

func TestRun_SelectedDiffFailureIsNonzeroWithoutModelCall(t *testing.T) {
	for _, queryFailure := range []bool{false, true} {
		name := "empty-diff"
		if queryFailure {
			name = "failed-query"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeADR(t, filepath.Join(root, "doc/adr"), "0001", "Rule", "lite", "pkg/**")
			client := gitcontext.NewClient(func(args []string) (string, error) {
				if slices.Contains(args, "--name-status") {
					return "M\x00pkg/a.go\x00", nil
				}
				if queryFailure {
					return "", fmt.Errorf("diff read failed")
				}
				return "", nil
			})
			client.SetGitRoot(root)
			calls := 0
			fake := func(a adr.ADR, diff string) (types.LintResult, error) {
				calls++
				return types.LintResult{ADR: a, Status: types.StatusPASS}, nil
			}
			var out bytes.Buffer
			var results []types.LintResult
			code, err := Run(types.LintOptions{Provider: types.ProviderClaude, NoCache: true, NoPreFilter: true}, RunDeps{Out: &out, Err: &out, Git: client, Results: &results, CacheDir: t.TempDir(), LintFns: map[types.Provider]cache.LintFn{types.ProviderClaude: fake}})
			if err != nil || code != 1 || calls != 0 || len(results) != 1 || results[0].Status != types.StatusERROR {
				t.Fatalf("selected diff failure passed: code%d calls%d err%v results%+v", code, calls, err, results)
				return
			}
		})
	}
}

func TestRun_BranchFileSelectionKeepsOnlySelectedCopyIdentity(t *testing.T) {
	root := t.TempDir()
	writeADR(t, filepath.Join(root, "doc/adr"), "0001", "Source rule", "lite", "source.go")
	client := gitcontext.NewClient(func(args []string) (string, error) {
		if len(args) > 0 && args[0] == "merge-base" {
			return "base\n", nil
		}
		if slices.Contains(args, "--name-status") {
			return "C100\x00source.go\x00new/a.go\x00C100\x00source.go\x00new/b.go\x00", nil
		}
		if slices.Contains(args, ":(literal)new/b.go") {
			return "", fmt.Errorf("unselected copy leaked into fetched scope")
		}
		if !slices.Contains(args, ":(literal)new/a.go") || !slices.Contains(args, ":(literal)source.go") {
			return "", fmt.Errorf("selected identity was incomplete")
		}
		return "diff --git a/source.go b/new/a.go\nsimilarity index 100%\ncopy from source.go\ncopy to new/a.go\n", nil
	})
	client.SetGitRoot(root)
	calls := 0
	fake := func(a adr.ADR, diff string) (types.LintResult, error) {
		calls++
		return types.LintResult{ADR: a, Status: types.StatusPASS}, nil
	}
	var out bytes.Buffer
	code, err := Run(types.LintOptions{Provider: types.ProviderClaude, BranchSet: true, Files: []string{"new/a.go"}, NoCache: true, NoPreFilter: true}, RunDeps{Out: &out, Err: &out, Git: client, CacheDir: t.TempDir(), LintFns: map[types.Provider]cache.LintFn{types.ProviderClaude: fake}})
	if err != nil || code != 0 || calls != 1 {
		t.Fatalf("branch counterpart closure changed requested identity: code%d calls%d err%v output%s", code, calls, err, out.String())
		return
	}
}
