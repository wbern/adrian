package runner

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/wbern/adrian/go/internal/adr"
	"github.com/wbern/adrian/go/internal/cache"
	"github.com/wbern/adrian/go/internal/gitcontext"
	"github.com/wbern/adrian/go/internal/types"
)

// --diff supplies the diff from outside git, so adr-lint can check a pull
// request without checking its branch out. These tests pin the properties the
// PR reviewer will depend on: git is never consulted for the diff, each ADR
// still sees only its own files, and every applicable ADR reaches a terminal
// status so a caller can prove nothing was quietly dropped.

// noGit returns a client that fails the test if git is invoked at all.
func noGit(t *testing.T, gitRoot string) *gitcontext.Client {
	t.Helper()
	c := gitcontext.NewClient(func(args []string) (string, error) {
		t.Fatalf("git was invoked in --diff mode: %v", args)
		return "", nil
	})
	c.SetGitRoot(gitRoot)
	return c
}

const twoFileDiff = "diff --git a/pkg/a.go b/pkg/a.go\nindex 1..2 100644\n--- a/pkg/a.go\n+++ b/pkg/a.go\n@@ -1 +1 @@\n-old\n+SENTINEL_ALPHA\n" +
	"diff --git a/web/b.ts b/web/b.ts\nindex 3..4 100644\n--- a/web/b.ts\n+++ b/web/b.ts\n@@ -1 +1 @@\n-old\n+SENTINEL_BETA\n"

func writeDiff(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// recordingLint captures the diff each ADR was checked against.
func recordingLint(seen map[string]string, mu *sync.Mutex) cache.LintFn {
	return func(a adr.ADR, diff string) (types.LintResult, error) {
		mu.Lock()
		seen[a.ID] = diff
		mu.Unlock()
		return types.LintResult{ADR: a, Status: types.StatusPASS, Explanation: "ok"}, nil
	}
}

func TestRun_DiffMode_IssuesNoGitCommandForTheDiff(t *testing.T) {
	gitRoot := t.TempDir()
	adrDir := filepath.Join(gitRoot, "doc/adr")
	writeADR(t, adrDir, "0001", "Go Rules", "lite", "pkg/**/*.go")
	writeADR(t, adrDir, "0002", "Web Rules", "lite", "web/**/*.ts")
	diffPath := writeDiff(t, t.TempDir(), "pr.diff", twoFileDiff)

	seen := map[string]string{}
	var mu sync.Mutex
	code, err := Run(
		types.LintOptions{DiffSet: true, DiffPath: diffPath, Provider: types.ProviderClaude},
		RunDeps{
			Out: &strings.Builder{}, Err: &strings.Builder{},
			Git:      noGit(t, gitRoot),
			LintFns:  map[types.Provider]cache.LintFn{types.ProviderClaude: recordingLint(seen, &mu)},
			CacheDir: t.TempDir(),
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if len(seen) != 2 {
		t.Errorf("checked %d ADRs, want 2: %v", len(seen), seen)
	}
}

func TestRun_DiffMode_PerADRSlicing(t *testing.T) {
	// The economy of the whole design. ADR-0001 governs pkg/**; it must never
	// be paid for on web/** bytes. Anything else and a per-ADR check costs the
	// same as injecting everything.
	gitRoot := t.TempDir()
	adrDir := filepath.Join(gitRoot, "doc/adr")
	writeADR(t, adrDir, "0001", "Go Rules", "lite", "pkg/**/*.go")
	writeADR(t, adrDir, "0002", "Web Rules", "lite", "web/**/*.ts")
	diffPath := writeDiff(t, t.TempDir(), "pr.diff", twoFileDiff)

	seen := map[string]string{}
	var mu sync.Mutex
	if _, err := Run(
		types.LintOptions{DiffSet: true, DiffPath: diffPath, Provider: types.ProviderClaude},
		RunDeps{
			Out: &strings.Builder{}, Err: &strings.Builder{},
			Git:      noGit(t, gitRoot),
			LintFns:  map[types.Provider]cache.LintFn{types.ProviderClaude: recordingLint(seen, &mu)},
			CacheDir: t.TempDir(),
		},
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(seen["0001"], "SENTINEL_ALPHA") {
		t.Errorf("ADR-0001 did not receive its own file's hunks: %q", seen["0001"])
	}
	if strings.Contains(seen["0001"], "SENTINEL_BETA") {
		t.Errorf("ADR-0001 was charged for web/b.ts, which it does not govern: %q", seen["0001"])
	}
	if !strings.Contains(seen["0002"], "SENTINEL_BETA") {
		t.Errorf("ADR-0002 did not receive its own file's hunks: %q", seen["0002"])
	}
	if strings.Contains(seen["0002"], "SENTINEL_ALPHA") {
		t.Errorf("ADR-0002 was charged for pkg/a.go, which it does not govern: %q", seen["0002"])
	}
	// The slice must be the supplied bytes, not a re-rendering of them.
	for id, d := range seen {
		if !strings.Contains(twoFileDiff, d) {
			t.Errorf("ADR-%s received bytes that are not a verbatim slice of the supplied diff: %q", id, d)
		}
	}
}

func TestRun_DiffMode_ReadsStdin(t *testing.T) {
	gitRoot := t.TempDir()
	writeADR(t, filepath.Join(gitRoot, "doc/adr"), "0001", "Go Rules", "lite", "pkg/**/*.go")

	seen := map[string]string{}
	var mu sync.Mutex
	if _, err := Run(
		types.LintOptions{DiffSet: true, DiffPath: "-", Provider: types.ProviderClaude},
		RunDeps{
			Out: &strings.Builder{}, Err: &strings.Builder{},
			In:       strings.NewReader(twoFileDiff),
			Git:      noGit(t, gitRoot),
			LintFns:  map[types.Provider]cache.LintFn{types.ProviderClaude: recordingLint(seen, &mu)},
			CacheDir: t.TempDir(),
		},
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(seen["0001"], "SENTINEL_ALPHA") {
		t.Errorf("stdin diff did not reach the check: %q", seen["0001"])
	}
}

func TestRun_DiffMode_UnreadableInputIsAnError(t *testing.T) {
	// A gate must never confuse "I could not read the input" with "nothing to
	// do". Both exit 0 today if this is not enforced.
	gitRoot := t.TempDir()
	writeADR(t, filepath.Join(gitRoot, "doc/adr"), "0001", "Go Rules", "lite", "pkg/**/*.go")

	code, err := Run(
		types.LintOptions{DiffSet: true, DiffPath: filepath.Join(t.TempDir(), "absent.diff"), Provider: types.ProviderClaude},
		RunDeps{
			Out: &strings.Builder{}, Err: &strings.Builder{},
			Git:      noGit(t, gitRoot),
			LintFns:  map[types.Provider]cache.LintFn{types.ProviderClaude: recordingLint(map[string]string{}, &sync.Mutex{})},
			CacheDir: t.TempDir(),
		},
	)
	if err == nil {
		t.Error("expected an error for an unreadable --diff path")
	}
	if code == 0 {
		t.Error("exit code = 0 for an unreadable --diff path")
	}
}

func TestRun_DiffMode_BytesWithoutFileHeadersIsAnError(t *testing.T) {
	gitRoot := t.TempDir()
	writeADR(t, filepath.Join(gitRoot, "doc/adr"), "0001", "Go Rules", "lite", "pkg/**/*.go")
	diffPath := writeDiff(t, t.TempDir(), "pr.diff", "this is not a diff at all\njust prose\n")

	code, err := Run(
		types.LintOptions{DiffSet: true, DiffPath: diffPath, Provider: types.ProviderClaude},
		RunDeps{
			Out: &strings.Builder{}, Err: &strings.Builder{},
			Git:      noGit(t, gitRoot),
			LintFns:  map[types.Provider]cache.LintFn{types.ProviderClaude: recordingLint(map[string]string{}, &sync.Mutex{})},
			CacheDir: t.TempDir(),
		},
	)
	if err == nil {
		t.Error("expected an error for review bytes with no resolvable file headers")
	}
	if code == 0 {
		t.Error("exit code = 0 for review bytes with no resolvable file headers")
	}
}

func TestRun_DiffMode_PreFilterSkipIsStillReported(t *testing.T) {
	// A skipped ADR must remain a visible, terminal result. If it vanished from
	// the result set, a caller counting results could not tell a skip from a
	// dropped rule — and 91% of the saving comes from skips.
	gitRoot := t.TempDir()
	adrDir := filepath.Join(gitRoot, "doc/adr")
	writeADRWithPreFilter(t, adrDir, "0001", "Go Rules", "lite", "pkg/**/*.go", "ABSENT_TERM")
	diffPath := writeDiff(t, t.TempDir(), "pr.diff", twoFileDiff)

	called := false
	var results []types.LintResult
	_, err := Run(
		types.LintOptions{DiffSet: true, DiffPath: diffPath, Provider: types.ProviderClaude},
		RunDeps{
			Out: &strings.Builder{}, Err: &strings.Builder{},
			Git: noGit(t, gitRoot),
			LintFns: map[types.Provider]cache.LintFn{types.ProviderClaude: func(a adr.ADR, diff string) (types.LintResult, error) {
				called = true
				return types.LintResult{ADR: a, Status: types.StatusPASS}, nil
			}},
			CacheDir: t.TempDir(),
			Results:  &results,
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("the model was called for an ADR whose pre_filter term is absent from the diff")
	}
	if len(results) != 1 {
		t.Fatalf("expected the skipped ADR to still be reported, got %d results", len(results))
	}
	// SKIPPED, not PASS: the reviewer's gate counts what was actually enforced,
	// and a rule nothing checked must not inflate that count.
	if results[0].Status != types.StatusSKIPPED {
		t.Errorf("skip status = %v, want SKIPPED", results[0].Status)
	}
	if !strings.Contains(results[0].Explanation, "pre-filter") {
		t.Errorf("skip is not self-describing: %q", results[0].Explanation)
	}
}

func TestRun_DiffMode_NoPreFilterForcesTheCheck(t *testing.T) {
	// The control arm. Without this, a pre_filter whose terms drifted away from
	// the rule it guards is undetectable: the skip reads exactly like a pass.
	gitRoot := t.TempDir()
	adrDir := filepath.Join(gitRoot, "doc/adr")
	writeADRWithPreFilter(t, adrDir, "0001", "Go Rules", "lite", "pkg/**/*.go", "ABSENT_TERM")
	diffPath := writeDiff(t, t.TempDir(), "pr.diff", twoFileDiff)

	called := false
	if _, err := Run(
		types.LintOptions{DiffSet: true, DiffPath: diffPath, NoPreFilter: true, Provider: types.ProviderClaude},
		RunDeps{
			Out: &strings.Builder{}, Err: &strings.Builder{},
			Git: noGit(t, gitRoot),
			LintFns: map[types.Provider]cache.LintFn{types.ProviderClaude: func(a adr.ADR, diff string) (types.LintResult, error) {
				called = true
				return types.LintResult{ADR: a, Status: types.StatusPASS}, nil
			}},
			CacheDir: t.TempDir(),
		},
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("--no-pre-filter did not force the model check")
	}
}

func TestRun_DiffMode_EveryApplicableADRReachesATerminalStatus(t *testing.T) {
	// The invariant the PR reviewer's fail-closed gate is built on: it will
	// escalate unless every ADR it expected appears in the results with a
	// terminal status.
	gitRoot := t.TempDir()
	adrDir := filepath.Join(gitRoot, "doc/adr")
	writeADR(t, adrDir, "0001", "Go Rules", "lite", "pkg/**/*.go")
	writeADRWithPreFilter(t, adrDir, "0002", "Web Rules", "lite", "web/**/*.ts", "ABSENT_TERM")
	writeADR(t, adrDir, "0003", "Everything", "lite", "**/*")
	// Governs nothing in this diff, so it must NOT appear at all.
	writeADR(t, adrDir, "0004", "Sql Rules", "lite", "db/**/*.sql")
	diffPath := writeDiff(t, t.TempDir(), "pr.diff", twoFileDiff)

	var results []types.LintResult
	if _, err := Run(
		types.LintOptions{DiffSet: true, DiffPath: diffPath, Provider: types.ProviderClaude},
		RunDeps{
			Out: &strings.Builder{}, Err: &strings.Builder{},
			Git: noGit(t, gitRoot),
			LintFns: map[types.Provider]cache.LintFn{types.ProviderClaude: func(a adr.ADR, diff string) (types.LintResult, error) {
				return types.LintResult{ADR: a, Status: types.StatusPASS}, nil
			}},
			CacheDir: t.TempDir(),
			Results:  &results,
		},
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := map[string]types.ResultStatus{}
	for _, r := range results {
		got[r.ADR.ID] = r.Status
	}
	for _, id := range []string{"0001", "0002", "0003"} {
		st, ok := got[id]
		if !ok {
			t.Errorf("ADR-%s applies but produced no result", id)
			continue
		}
		switch st {
		case types.StatusPASS, types.StatusFAIL, types.StatusWARN, types.StatusSKIPPED:
		default:
			t.Errorf("ADR-%s status %v is not terminal", id, st)
		}
	}
	if _, ok := got["0004"]; ok {
		t.Error("ADR-0004 governs no changed file but was checked anyway")
	}
}

func TestRun_DiffMode_WritesNothingUnderGitRoot(t *testing.T) {
	// The reviewer runs with cwd set to a SHARED read-only checkout it borrows
	// for doc/adr. A cache or report tree appearing in there dirties a working
	// copy other agents are using.
	gitRoot := t.TempDir()
	writeADR(t, filepath.Join(gitRoot, "doc/adr"), "0001", "Go Rules", "lite", "pkg/**/*.go")
	diffPath := writeDiff(t, t.TempDir(), "pr.diff", twoFileDiff)

	before := treeSnapshot(t, gitRoot)
	if _, err := Run(
		types.LintOptions{
			DiffSet: true, DiffPath: diffPath, CI: true, Provider: types.ProviderClaude,
			CacheDir: t.TempDir(), ReportDir: t.TempDir(),
		},
		RunDeps{
			Out: &strings.Builder{}, Err: &strings.Builder{},
			Git: noGit(t, gitRoot),
			LintFns: map[types.Provider]cache.LintFn{types.ProviderClaude: func(a adr.ADR, diff string) (types.LintResult, error) {
				return types.LintResult{ADR: a, Status: types.StatusPASS}, nil
			}},
		},
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	after := treeSnapshot(t, gitRoot)
	if len(after) != len(before) {
		t.Errorf("run wrote into the checkout: before %v, after %v", before, after)
	}
}

func treeSnapshot(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	if err := filepath.Walk(root, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		paths = append(paths, p)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return paths
}
