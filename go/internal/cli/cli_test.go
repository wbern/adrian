package cli

import (
	"bytes"
	"strings"
	"testing"
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
