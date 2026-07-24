package gitcontext

import (
	"slices"
	"strings"
	"testing"
)

func contains(s, substr string) bool { return strings.Contains(s, substr) }
func splitLines(s string) []string   { return strings.Split(s, "\n") }

var errBoom = errBoomT("boom")

type errBoomT string

func (e errBoomT) Error() string { return string(e) }

type recordingRunner struct {
	calls   [][]string
	respond func(args []string) (string, error)
}

func (r *recordingRunner) Run(args []string) (string, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	if r.respond != nil {
		return r.respond(args)
	}
	return "", nil
}

func newDefaultRunner(stdout string) *recordingRunner {
	return &recordingRunner{respond: func(args []string) (string, error) {
		if len(args) > 0 && args[0] == "rev-parse" {
			return "/repo\n", nil
		}
		if len(args) > 0 && args[0] == "merge-base" {
			return "abc123\n", nil
		}
		return stdout, nil
	}}
}

func findCall(r *recordingRunner, predicate func([]string) bool) []string {
	for _, c := range r.calls {
		if predicate(c) {
			return c
		}
	}
	return nil
}

func TestGetStagedFiles_ParsesNewlineSeparatedOutput(t *testing.T) {
	rr := &recordingRunner{respond: func(args []string) (string, error) {
		return "pkg/a.go\n  pkg/b.go  \n\npkg/c.go\n", nil
	}}
	c := NewClient(rr.Run)

	files := c.GetStagedFiles()
	want := []string{"pkg/a.go", "pkg/b.go", "pkg/c.go"}
	if !slices.Equal(files, want) {
		t.Errorf("got %v, want %v", files, want)
	}
}

func TestGetStagedFiles_ReturnsEmptyOnError(t *testing.T) {
	rr := &recordingRunner{respond: func(args []string) (string, error) {
		return "", errBoom
	}}
	c := NewClient(rr.Run)

	if got := c.GetStagedFiles(); len(got) != 0 {
		t.Errorf("got %v, want empty slice", got)
	}
}

func TestGetDiffAgainstMainForFiles_UsesBaseAndHeadShaFromEnv(t *testing.T) {
	t.Setenv("BASE_SHA", "deadbeef")
	t.Setenv("HEAD_SHA", "cafef00d")

	rr := newDefaultRunner("diff output")
	c := NewClient(rr.Run)

	c.GetDiffAgainstMainForFiles([]string{"pkg/foo.go"}, "", true)

	call := findCall(rr, func(args []string) bool {
		return slices.Contains(args, "deadbeef...cafef00d")
	})
	if call == nil {
		t.Errorf("expected diff to use BASE_SHA...HEAD_SHA, got calls=%v", rr.calls)
	}
}

func TestGetFilesChangedAgainstMain_ParsesLines(t *testing.T) {
	rr := newDefaultRunner("a.go\nb.go\n")
	c := NewClient(rr.Run)

	got := c.GetFilesChangedAgainstMain("")
	want := []string{"a.go", "b.go"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestGetDiffAgainstMainForFiles_ReturnsDiffOutputVerbatim(t *testing.T) {
	mockDiff := "diff --git a/test.go b/test.go\n" +
		"index abc123..def456 100644\n" +
		"--- a/test.go\n" +
		"+++ b/test.go\n" +
		"@@ -1,5 +1,6 @@\n" +
		" func example() {\n" +
		"-  fmt.Println(\"old\")\n" +
		"+  fmt.Println(\"new\")\n" +
		"+  x := 1\n" +
		" }\n"
	rr := newDefaultRunner(mockDiff)
	c := NewClient(rr.Run)

	got := c.GetDiffAgainstMainForFiles([]string{"test.go"}, "", true)

	for _, want := range []string{
		"diff --git", "@@",
		"+  fmt.Println(\"new\")",
		"+  x := 1",
		"-  fmt.Println(\"old\")",
	} {
		if !contains(got, want) {
			t.Errorf("output missing %q", want)
		}
	}
	// Find a context line (begins with space, not + or -).
	for _, line := range splitLines(got) {
		if contains(line, "func example") {
			if len(line) == 0 || line[0] != ' ' {
				t.Errorf("context line should start with space, got %q", line)
			}
			break
		}
	}
}

func TestGetDiffAgainstMainForFiles_DefaultsToWContextFlag(t *testing.T) {
	rr := newDefaultRunner("diff output")
	c := NewClient(rr.Run)

	c.GetDiffAgainstMainForFiles([]string{"pkg/foo.go"}, "", true)

	// We want the actual `git diff <range> -- <files>` call (not merge-base).
	call := findCall(rr, func(args []string) bool {
		return slices.Contains(args, "--") &&
			!slices.Contains(args, "--cached") &&
			!slices.Contains(args, "merge-base")
	})
	if call == nil {
		t.Fatalf("expected a `git diff <range> -- files` call; got %v", rr.calls)
	}
	if !slices.Contains(call, "-W") {
		t.Errorf("expected -W in args, got %v", call)
	}
}

func TestGetDiffAgainstMainForFilesWithContextLines_UsesRequestedContext(t *testing.T) {
	rr := newDefaultRunner("diff output")
	c := NewClient(rr.Run)

	c.GetDiffAgainstMainForFilesWithContextLines([]string{"pkg/foo.go"}, "", 3)

	call := findCall(rr, func(args []string) bool {
		return slices.Contains(args, "--") && !slices.Contains(args, "--cached")
	})
	if call == nil {
		t.Fatalf("expected a diff call, got %v", rr.calls)
	}
	if !slices.Contains(call, "-U3") {
		t.Errorf("expected -U3 in args, got %v", call)
	}
}

func TestGetStagedDiffForFiles_IncludeContextFalseUsesU0(t *testing.T) {
	rr := newDefaultRunner("diff output")
	c := NewClient(rr.Run)

	c.GetStagedDiffForFiles([]string{"pkg/foo.go"}, false)

	if len(rr.calls) == 0 {
		t.Fatal("runner was never invoked")
	}
	call := rr.calls[0]
	if !slices.Contains(call, "-U0") {
		t.Errorf("expected -U0 in args, got %v", call)
	}
}

func TestGetStagedDiffForFiles_DefaultsToWContextFlag(t *testing.T) {
	rr := newDefaultRunner("diff output")
	c := NewClient(rr.Run)

	c.GetStagedDiffForFiles([]string{"pkg/foo.go"}, true)

	call := findCall(rr, func(args []string) bool {
		return slices.Contains(args, "--cached")
	})
	if call == nil {
		t.Fatal("expected a `git diff --cached ...` invocation")
	}
	if !slices.Contains(call, "-W") {
		t.Errorf("expected -W in args, got %v", call)
	}
}
