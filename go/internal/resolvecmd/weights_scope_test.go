package resolvecmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func scopedWeightsFixture(t *testing.T, rows []byte, finish string) (string, string, string) {
	t.Helper()
	repo, bin := t.TempDir(), t.TempDir()
	log := filepath.Join(t.TempDir(), "gc-arguments")
	cache := filepath.Join(t.TempDir(), "weights.json")
	input := filepath.Join(t.TempDir(), "rows.json")
	if err := os.WriteFile(input, rows, 0600); err != nil {
		t.Fatal(err)
	}
	stub := `#!/bin/sh
printf 'CALL\n' >> "$ADRIAN_SCOPE_TEST_LOG"
printf '%s\n' "$@" >> "$ADRIAN_SCOPE_TEST_LOG"
case " $* " in
  *" --rig crm --allow-unbounded sql --json "*) ;;
  *) exit 77 ;;
esac
` + finish + "\n"
	if err := os.WriteFile(filepath.Join(bin, "gc"), []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ADRIAN_SCOPE_TEST_LOG", log)
	t.Setenv("ADRIAN_SCOPE_TEST_ROWS", input)
	t.Setenv("GC_RIG", "crm")
	t.Setenv("GC_RIG_ROOT", filepath.Join(t.TempDir(), "another-rig"))
	t.Setenv("REVIEW_WEIGHTS_CACHE", cache)
	t.Setenv("REVIEW_WEIGHTS_OFFLINE", "0")
	return repo, cache, log
}

func requireOneScopedWeightsQuery(t *testing.T, log string) {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), "CALL\n") != 1 ||
		!strings.Contains(string(raw), "bd\n--rig\ncrm\n--allow-unbounded\nsql\n--json\n") {
		t.Fatalf("wanted one explicitly scoped, unbounded-admitted query, got %q", raw)
	}
}

func TestScopedWeightsQueryAcceptsCompleteResultBeyondFirewallBudget(t *testing.T) {
	rows := make([]RawFindingRow, 600)
	for i := range rows {
		rows[i] = RawFindingRow{RuleID: "ADR-0014", File: "src/components/Accordion.tsx", PR: fmt.Sprintf("controlled-%04d", i), Day: "2026-10-27"}
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) <= 64*1024 {
		t.Fatalf("fixture must exceed the ordinary output firewall: %d bytes", len(raw))
	}
	repo, cache, log := scopedWeightsFixture(t, raw, `exec /bin/cat "$ADRIAN_SCOPE_TEST_ROWS"`)
	weights, err := LoadWeights(repo)
	if err != nil || weights == nil {
		t.Fatalf("valid explicitly scoped result refused: %v", err)
	}
	if weights.Window.CorpusFindings != len(rows) {
		t.Fatalf("lost findings: got %d, want %d", weights.Window.CorpusFindings, len(rows))
	}
	rules := weights.Buckets["src/components/**"].Rules
	if len(rules) != 1 || rules[0].Findings != len(rows) || rules[0].PRs != len(rows) {
		t.Fatalf("wrong scoped aggregation: %#v", rules)
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatalf("qualified scoped result was not cached: %v", err)
	}
	requireOneScopedWeightsQuery(t, log)
}

func TestMalformedScopedWeightsQueryIsNotEmptySuccess(t *testing.T) {
	for _, raw := range []string{"not-json", "[] trailing-data", "null", "{}", "[null]", `[{"rule_id":"ADR-0014"}]`} {
		t.Run(raw, func(t *testing.T) {
			repo, cache, log := scopedWeightsFixture(t, []byte(raw), `exec /bin/cat "$ADRIAN_SCOPE_TEST_ROWS"`)
			weights, err := LoadWeights(repo)
			if err == nil || weights != nil {
				t.Fatalf("malformed store result became successful corpus: %#v, %v", weights, err)
			}
			if _, err := os.Stat(cache); !os.IsNotExist(err) {
				t.Fatalf("malformed result created cache: %v", err)
			}
			requireOneScopedWeightsQuery(t, log)
		})
	}
}

func TestScopedEmptyArrayIsKnownZeroAndReusedWithoutAnotherQuery(t *testing.T) {
	repo, cache, log := scopedWeightsFixture(t, []byte("[]"), `exec /bin/cat "$ADRIAN_SCOPE_TEST_ROWS"`)
	weights, err := LoadWeights(repo)
	if err != nil || weights == nil || weights.Window.CorpusFindings != 0 || weights.Window.Origin != "store" || weights.Window.Stale == nil || *weights.Window.Stale {
		t.Fatalf("valid scoped empty result lost fresh-zero provenance: %#v, %v", weights, err)
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatal(err)
	}
	weights, err = LoadWeights(repo)
	if err != nil || weights == nil || weights.Window.CorpusFindings != 0 || weights.Window.Origin != "cache" || weights.Window.Stale == nil || !*weights.Window.Stale {
		t.Fatalf("known empty cache became unavailable or fresh: %#v, %v", weights, err)
	}
	requireOneScopedWeightsQuery(t, log)
}

func TestScopedQueryTimeoutIsUnavailableAndDoesNotCreateCache(t *testing.T) {
	// exec makes sleep the command's actual PID, so cancellation leaves no child.
	repo, cache, log := scopedWeightsFixture(t, nil, "exec /bin/sleep 30")
	weights, err := LoadWeights(repo)
	if err == nil || weights != nil {
		t.Fatalf("timed out query became a successful corpus: %#v, %v", weights, err)
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatalf("timed out query created a cache: %v", err)
	}
	requireOneScopedWeightsQuery(t, log)
}

func TestFailedScopedWeightsQueryCannotReadOrCacheAnotherRig(t *testing.T) {
	repo := t.TempDir()
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "gc-arguments")
	cache := filepath.Join(t.TempDir(), "weights.json")
	stub := `#!/bin/sh
printf 'CALL\n' >> "$ADRIAN_SCOPE_TEST_LOG"
printf '%s\n' "$@" >> "$ADRIAN_SCOPE_TEST_LOG"
case " $* " in
  *" --rig "*) exit 19 ;;
esac
printf '%s\n' '[{"rule_id":"ADR-0014","file":"src/components/Accordion.tsx","pr":"controlled-foreign-store","day":"2026-10-27"}]'
`
	if err := os.WriteFile(filepath.Join(bin, "gc"), []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ADRIAN_SCOPE_TEST_LOG", log)
	t.Setenv("GC_RIG", "crm")
	t.Setenv("GC_RIG_ROOT", filepath.Join(t.TempDir(), "another-rig"))
	t.Setenv("REVIEW_WEIGHTS_CACHE", cache)
	t.Setenv("REVIEW_WEIGHTS_OFFLINE", "0")

	weights, err := LoadWeights(repo)
	if err == nil || weights != nil {
		t.Fatalf("failed scoped query accepted another rig's data: weights=%#v err=%v", weights, err)
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatalf("failed scoped query created a foreign cache: %v", err)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if strings.Count(string(raw), "CALL\n") != 1 {
		t.Fatalf("scoped failure retried another subject: %q", args)
	}
	prefix := []string{"CALL", "bd", "--rig", "crm", "--allow-unbounded", "sql", "--json"}
	if len(args) != len(prefix)+1 {
		t.Fatalf("unexpected scoped query arguments: %q", args)
	}
	for i, expected := range prefix {
		if args[i] != expected {
			t.Fatalf("scoped argument %d = %q, want %q; all=%q", i, args[i], expected, args)
		}
	}
}
