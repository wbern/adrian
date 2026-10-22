package auditcmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func createTestRepo(t *testing.T) (string, string) {
	t.Helper()
	repoDir := t.TempDir()
	adrDir := filepath.Join(repoDir, "doc", "adr")
	if err := os.MkdirAll(adrDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Fake .git directory so resolveRepoRoot stops here
	if err := os.Mkdir(filepath.Join(repoDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	// Create some repository source files
	srcDir := filepath.Join(repoDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}

	// File with mutation keyword
	if err := os.WriteFile(filepath.Join(srcDir, "mutations.ts"), []byte("export const authedMutatingRpc = true;"), 0644); err != nil {
		t.Fatal(err)
	}

	// File with regular query
	if err := os.WriteFile(filepath.Join(srcDir, "users.ts"), []byte("export const getUser = true;"), 0644); err != nil {
		t.Fatal(err)
	}

	return repoDir, adrDir
}

func TestAuditRepository_HealthyADR(t *testing.T) {
	_, adrDir := createTestRepo(t)

	// ADR-0001: targets src/*.ts, pre_filter is mutation (which matches authedMutatingRpc via stem)
	adr1 := `---
id: "0001"
title: "Mutations must be audited"
status: "accepted"
applies_to: ["src/**/*.ts"]
pre_filter: ["mutation"]
complexity: "standard"
---

# Decision
Every mutation needs auditing.
`
	if err := os.WriteFile(filepath.Join(adrDir, "0001-mutations.md"), []byte(adr1), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	err := Run([]string{"--format", "json"}, adrDir, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report AuditReport
	if err := json.Unmarshal(buf.Bytes(), &report); err != nil {
		t.Fatalf("failed to parse json output: %v", err)
	}

	if report.Summary.TotalADRs != 1 {
		t.Errorf("TotalADRs = %d, want 1", report.Summary.TotalADRs)
	}
	if report.Summary.ErrorCount != 0 {
		t.Errorf("ErrorCount = %d, want 0", report.Summary.ErrorCount)
	}
	if report.Summary.WarningCount != 0 {
		t.Errorf("WarningCount = %d, want 0. Issues: %+v", report.Summary.WarningCount, report.Issues)
	}
	if !report.Summary.Passed {
		t.Errorf("expected Passed to be true")
	}
}

func TestAuditRepository_DormantAppliesTo(t *testing.T) {
	_, adrDir := createTestRepo(t)

	// ADR-0002: targets nonexistent directory
	adr2 := `---
id: "0002"
title: "Legacy Webpack Rules"
status: "accepted"
applies_to: ["legacy/webpack/**/*.js"]
---

# Decision
Old webpack rules.
`
	if err := os.WriteFile(filepath.Join(adrDir, "0002-webpack.md"), []byte(adr2), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	err := Run([]string{"--format", "json"}, adrDir, &buf)
	if err != nil {
		t.Fatalf("Run returned error without strict: %v", err)
	}

	var report AuditReport
	if err := json.Unmarshal(buf.Bytes(), &report); err != nil {
		t.Fatalf("failed to parse json: %v", err)
	}

	if report.Summary.WarningCount != 1 {
		t.Fatalf("WarningCount = %d, want 1", report.Summary.WarningCount)
	}
	if report.Issues[0].Code != "DORMANT_APPLIES_TO" {
		t.Errorf("issue code = %q, want DORMANT_APPLIES_TO", report.Issues[0].Code)
	}
}

func TestAuditRepository_DeadPreFilter(t *testing.T) {
	_, adrDir := createTestRepo(t)

	// ADR-0003: targets src/**/*.ts, but pre_filter token never appears in any ts file
	adr3 := `---
id: "0003"
title: "Dead Token Check"
status: "accepted"
applies_to: ["src/**/*.ts"]
pre_filter: ["completely_dead_and_nonexistent_function_name"]
---

# Decision
Rule about dead function.
`
	if err := os.WriteFile(filepath.Join(adrDir, "0003-dead.md"), []byte(adr3), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	err := Run([]string{"--format", "json"}, adrDir, &buf)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	var report AuditReport
	if err := json.Unmarshal(buf.Bytes(), &report); err != nil {
		t.Fatalf("failed to parse json: %v", err)
	}

	if report.Summary.WarningCount != 1 {
		t.Fatalf("WarningCount = %d, want 1", report.Summary.WarningCount)
	}
	if report.Issues[0].Code != "DEAD_PRE_FILTER" {
		t.Errorf("issue code = %q, want DEAD_PRE_FILTER", report.Issues[0].Code)
	}
	if !strings.Contains(report.Issues[0].Message, "completely_dead_and_nonexistent_function_name") {
		t.Errorf("message missing token: %q", report.Issues[0].Message)
	}
}

func TestAuditRepository_StrictModeFailsOnWarning(t *testing.T) {
	_, adrDir := createTestRepo(t)

	adrDormant := `---
id: "0004"
title: "Dormant Rule"
status: "accepted"
applies_to: ["nonexistent/**/*.go"]
---
# Decision
`
	if err := os.WriteFile(filepath.Join(adrDir, "0004-dormant.md"), []byte(adrDormant), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	err := Run([]string{"--strict"}, adrDir, &buf)
	if err == nil {
		t.Fatal("expected error in strict mode with warning, got nil")
	}
	if !strings.Contains(err.Error(), "audit failed in strict mode") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAuditRepository_TextOutput(t *testing.T) {
	_, adrDir := createTestRepo(t)

	adrOK := `---
id: "0005"
title: "Valid ADR"
status: "accepted"
applies_to: ["src/**/*.ts"]
pre_filter: ["getUser"]
---
# Decision
`
	if err := os.WriteFile(filepath.Join(adrDir, "0005-ok.md"), []byte(adrOK), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	err := Run([]string{"--format", "text"}, adrDir, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	outStr := buf.String()
	if !strings.Contains(outStr, "ADRian Repository Audit") {
		t.Errorf("missing header in text output: %s", outStr)
	}
	if !strings.Contains(outStr, "All ADRs healthy!") {
		t.Errorf("expected clean message in text output: %s", outStr)
	}
	if !strings.Contains(outStr, "Result: PASSED") {
		t.Errorf("expected PASSED result: %s", outStr)
	}
}
