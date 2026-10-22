package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/wbern/adrian/go/internal/adr"
	"github.com/wbern/adrian/go/internal/types"
)

func TestCanonicalAndCompatibilityCommands(t *testing.T) {
	for _, name := range []string{"adrian", "adr-lint"} {
		for _, args := range [][]string{{"version"}, {"help"}, {"--help"}, {"plan", "--help"}} {
			var out, stderr bytes.Buffer
			if code := Run(name, args, &out, &stderr); code != 0 {
				t.Fatalf("%s %v: exit=%d stderr=%s", name, args, code, stderr.String())
			}
			if len(args) == 1 && args[0] == "version" && !strings.HasPrefix(out.String(), name+" ") {
				t.Fatalf("wrong executable identity: %s", out.String())
			}
		}
		var out, stderr bytes.Buffer
		if code := Run(name, []string{"plan", "--head", "missing"}, &out, &stderr); code == 0 || out.Len() != 0 || !strings.Contains(stderr.String(), "--base is required") {
			t.Fatalf("invalid plan must fail without output: %d %s %s", code, out.String(), stderr.String())
		}
	}
}

func TestCodePlanCommandIsModelFreeAndRequiresProducerIdentity(t *testing.T) {
	for _, name := range []string{"adrian", "adr-lint"} {
		var out, stderr bytes.Buffer
		if code := Run(name, []string{"code-plan", "--help"}, &out, &stderr); code != 0 || !strings.Contains(out.String(), "--repository") {
			t.Fatalf("missing code-plan: %d %s %s", code, out.String(), stderr.String())
		}
		out.Reset()
		stderr.Reset()
		if code := Run(name, []string{"code-plan", "--base", "HEAD"}, &out, &stderr); code == 0 || out.Len() != 0 || !strings.Contains(stderr.String(), "repository") {
			t.Fatalf("unsafe code-plan: %d %s %s", code, out.String(), stderr.String())
		}
	}
}

func TestExecutePlanPublicCommandRequiresImmutableAttempt(t *testing.T) {
	var out, stderr bytes.Buffer
	if code := Run("adrian", []string{"execute-plan", "--help"}, &out, &stderr); code != 0 || !strings.Contains(out.String(), "--record") {
		t.Fatalf("missing durable executor: %d %s %s", code, out.String(), stderr.String())
	}
}

func TestMakeCommandLintFn_EnvironmentVariables(t *testing.T) {
	a := adr.ADR{
		ID:              "0042",
		Title:           "Use Strict Boundaries\nIn Tests",
		Complexity:      adr.ComplexityComplex,
		DiffContextMode: adr.DiffContextFull,
		MatchedFiles:    []string{"pkg/a.go", "pkg/b.go"},
	}
	diff := `diff --git a/pkg/a.go b/pkg/a.go
--- a/pkg/a.go
+++ b/pkg/a.go
@@ -1 +1 @@
-old
+new
`
	// The agent command writes env vars and manifest contents as JSON response
	cmdStr := `echo "ADR_ID=$ADRIAN_ADR_ID"
echo "ADR_TITLE=$ADRIAN_ADR_TITLE"
echo "COMPLEXITY=$ADRIAN_COMPLEXITY"
echo "DIFF_CONTEXT=$ADRIAN_DIFF_CONTEXT"
echo "COUNT=$ADRIAN_MATCHED_FILES_COUNT"
echo "FILES=$ADRIAN_MATCHED_FILES"
if [ -f "$ADRIAN_MATCHED_FILES_FILE" ]; then
  echo "MANIFEST=$(cat $ADRIAN_MATCHED_FILES_FILE | tr '\n' ' ')"
fi
echo '{"status": "PASS", "explanation": "ok"}'`

	fn := makeCommandLintFn(cmdStr)
	res, err := fn(a, diff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != types.StatusPASS {
		t.Fatalf("expected PASS, got %s: %s", res.Status, res.Explanation)
	}
}

func TestMakeCommandLintFn_HugeFileListBlanksInlineFiles(t *testing.T) {
	files := make([]string, 100)
	for i := range files {
		files[i] = "very/deeply/nested/path/to/some/source/file_" + strings.Repeat("x", 30) + ".go"
	}
	a := adr.ADR{
		ID:           "0099",
		Title:        "Massive Changes",
		MatchedFiles: files,
	}
	diff := `diff --git a/pkg/a.go b/pkg/a.go
+++ b/pkg/a.go
@@ -1 +1 @@
+x
`
	cmdStr := `if [ -n "$ADRIAN_MATCHED_FILES" ]; then
  echo "INLINE_NOT_BLANK" >&2
  exit 1
fi
if [ ! -f "$ADRIAN_MATCHED_FILES_FILE" ]; then
  echo "MANIFEST_MISSING" >&2
  exit 1
fi
echo '{"status": "PASS", "explanation": "manifest verified"}'`

	fn := makeCommandLintFn(cmdStr)
	res, err := fn(a, diff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != types.StatusPASS {
		t.Fatalf("expected PASS, got %s: %s", res.Status, res.Explanation)
	}
}
