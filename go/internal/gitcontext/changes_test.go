package gitcontext

import (
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func TestChangeManifestBothEndpointsAndLiteralDiff(t *testing.T) {
	raw := "R090\x00pkg/old[1].go\x00moved/new*.go\x00D\x00deleted.go\x00A\x00added.go\x00M\x00other\nname.go\x00"
	rr := newDefaultRunner(raw)
	c := NewClient(rr.Run)
	changes, err := c.GetStagedChanges()
	if err != nil {
		t.Fatal(err)
		return
	}
	want := []string{"added.go", "deleted.go", "moved/new*.go", "other\nname.go", "pkg/old[1].go"}
	if !slices.Equal(ChangedPaths(changes), want) {
		t.Fatalf("lost endpoints: %+v", changes)
		return
	}
	expanded := ExpandChangePaths(changes, []string{"pkg/old[1].go"})
	if !slices.Equal(expanded, []string{"moved/new*.go", "pkg/old[1].go"}) {
		t.Fatalf("wrong counterpart closure: %v", expanded)
		return
	}
	rr.respond = func(args []string) (string, error) { return "actual diff", nil }
	if _, err = c.ReadStagedDiffForFiles(expanded, false); err != nil {
		t.Fatal(err)
		return
	}
	last := rr.calls[len(rr.calls)-1]
	if !slices.Contains(last, ":(literal)pkg/old[1].go") || !slices.Contains(last, ":(literal)moved/new*.go") || !slices.Contains(last, "--find-renames") {
		t.Fatalf("nonliteral or partial pathspec: %v", last)
		return
	}
}
func TestMalformedChangeManifestRefusesPartialInventory(t *testing.T) {
	for _, raw := range []string{"M\x00missing-terminator", "R100\x00old\x00", "R+10\x00old\x00new\x00", "R101\x00old\x00new\x00", "Q\x00file\x00", "M\x00../escape\x00", "M\x00/absolute\x00", "M\x00a//b\x00", "M\x00bad\xff\x00", "M\x00\x00", "M\x00a\x00M\x00a\x00", "M\x00valid\x00R100\x00old\x00"} {
		t.Run(raw, func(t *testing.T) {
			c := NewClient(func([]string) (string, error) { return raw, nil })
			if got, err := c.GetStagedChanges(); err == nil || got != nil {
				t.Fatalf("invalid inventory accepted partially: %+v %v", got, err)
				return
			}
		})
	}
}
func TestChangeAndDiffQueriesReturnErrors(t *testing.T) {
	c := NewClient(func([]string) (string, error) { return "", errors.New("store unavailable") })
	if _, err := c.GetStagedChanges(); err == nil {
		t.Fatal("manifest query error lost")
		return
	}
	if _, err := c.GetChangesAgainstMain("HEAD"); err == nil {
		t.Fatal("branch query error lost")
		return
	}
	if _, err := c.ReadStagedDiffForFiles([]string{"file.go"}, false); err == nil {
		t.Fatal("diff query error lost")
		return
	}
	c = NewClient(func([]string) (string, error) { return "", nil })
	if _, err := c.GetChangesAgainstMain("HEAD"); err == nil {
		t.Fatal("empty merge-base falsely accepted as no changes")
		return
	}
	if _, err := c.ReadStagedDiffForFiles([]string{"file.go"}, false); err == nil {
		t.Fatal("empty selected diff accepted")
		return
	}
}
func TestCopyCounterpartExpansionDoesNotSelectUnrelatedCopy(t *testing.T) {
	changes := []Change{{Status: "C100", OldPath: "source.go", NewPath: "a.go"}, {Status: "C100", OldPath: "source.go", NewPath: "b.go"}}
	got := ExpandChangePaths(changes, []string{"a.go"})
	if strings.Join(got, ",") != "a.go,source.go" {
		t.Fatalf("transitive copy closure expanded scope: %v", got)
		return
	}
}

func TestStagedManifestIncludesGitlinkHiddenByLocalDiffConfig(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) (string, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.test", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.test")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	must := func(args ...string) {
		t.Helper()
		if out, err := run(args...); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
			return
		}
	}
	must("init", "-q")
	must("update-index", "--add", "--cacheinfo", "160000", strings.Repeat("1", 40), "deps/submodule")
	must("commit", "-qm", "gitlink baseline")
	must("update-index", "--cacheinfo", "160000", strings.Repeat("2", 40), "deps/submodule")
	must("config", "diff.ignoreSubmodules", "all")
	hidden, err := run("diff", "--cached", "--name-status", "-z", "--find-renames")
	if err != nil {
		t.Fatal(err)
		return
	}
	if hidden != "" {
		t.Fatalf("negative control did not hide gitlink: %q", hidden)
		return
	}
	client := NewClient(func(args []string) (string, error) { return run(args...) })
	client.SetGitRoot(root)
	changes, err := client.GetStagedChanges()
	if err != nil {
		t.Fatal(err)
		return
	}
	if !slices.Equal(ChangedPaths(changes), []string{"deps/submodule"}) {
		t.Fatalf("whole manifest silently hid configured gitlink: %+v", changes)
		return
	}
}
