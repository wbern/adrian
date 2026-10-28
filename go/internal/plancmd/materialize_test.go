package plancmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/wbern/adrian/go/internal/sharedpacket"
)

func nativeMaterializeFixture(t *testing.T, format string) (CodePlan, []byte) {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "--object-format="+format)
	write(t, dir, ".adrian/review-capabilities.yml", "version: 1\ncapabilities:\n  code-judgment: {evidence: []}\n")
	write(t, dir, "doc/adr/0001-policy.md", "---\nstatus: accepted\napplies_to: ['src/**']\n---\n# Preserve bytes\n\n## Decision\nKeep <>& é🎹 \u2028\u2029 and complete context.\n")
	write(t, dir, "src/deleted.txt", "original deleted source\n")
	write(t, dir, "src/old.txt", strings.Repeat("rename remains bound\n", 30))
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "trusted inputs")
	base := git(t, dir, "rev-parse", "HEAD")
	write(t, dir, "src/unicode.txt", strings.Repeat("<>& é🎹 \u2028\u2029\n", 1300))
	write(t, dir, "src/empty.txt", "")
	write(t, dir, "src/binary.dat", string([]byte{0, 255, 1}))
	if err := os.Symlink("../../not-a-readable-source", filepath.Join(dir, "src/link")); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "rm", "src/deleted.txt")
	git(t, dir, "mv", "src/old.txt", "renamed.txt")
	git(t, dir, "add", ".")
	git(t, dir, "update-index", "--add", "--cacheinfo", "160000,"+base+",src/module")
	git(t, dir, "commit", "-qm", "all native representations")
	var out bytes.Buffer
	if err := RunCodeCLI([]string{"--repository", "owner/repo", "--base", base, "--head", "HEAD", "--policy", base}, dir, &out); err != nil {
		t.Fatal(err)
	}
	var plan CodePlan
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.SharedDelivery == nil || plan.SharedDelivery.Limits.FragmentLimitExceeded {
		t.Fatal("real fixture must be admitted by the existing producer")
	}
	return plan, out.Bytes()
}

func materializeTestFile(t *testing.T, raw []byte) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "producer.json")
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return file
}

func materializeTestArgs(plan CodePlan, file, atom string) []string {
	return []string{"--plan", file, "--expected-plan-fingerprint", plan.Fingerprint, "--expected-shared-fingerprint", plan.SharedDelivery.Fingerprint, "--atom", atom}
}

func TestMaterializeReconstructsEveryRealProducerArtifact(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			plan, raw := nativeMaterializeFixture(t, format)
			file := materializeTestFile(t, raw)
			inputs, err := materializeInputs(plan)
			if err != nil {
				t.Fatal(err)
			}
			collections, encodings, sourceKinds := map[string]bool{}, map[string]bool{}, map[string]bool{}
			reconstructed := map[string]string{}
			for _, source := range plan.Content {
				encodings[source.Encoding] = true
				sourceKinds[source.Kind] = true
			}
			for _, atom := range plan.SharedDelivery.Atoms {
				expected, err := sharedpacket.Materialize(*plan.SharedDelivery, atom.ID, inputs, plan.SharedDelivery.Fingerprint)
				if err != nil {
					t.Fatal(err)
				}
				var out bytes.Buffer
				if err := RunMaterializeCLI(materializeTestArgs(plan, file, atom.ID), &out); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(out.Bytes(), expected) {
					t.Fatal("CLI changed exact native envelope bytes")
				}
				var envelope map[string]any
				if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				reconstructed[atom.ArtifactID] += envelope["content"].(string)
			}
			for i, artifact := range plan.SharedDelivery.Artifacts {
				collections[artifact.CanonicalRef.Collection] = true
				if reconstructed[artifact.ID] != inputs[i].Content {
					t.Fatalf("lost canonical stored bytes for %s", artifact.Kind)
				}
			}
			for _, collection := range []string{"policies", "content", "root", "change_diffs"} {
				if !collections[collection] {
					t.Fatalf("fixture omitted canonical collection %s", collection)
				}
			}
			if !encodings["base64"] || !sourceKinds["symlink"] || !sourceKinds["gitlink"] {
				t.Fatalf("fixture omitted stored representations: %v %v", encodings, sourceKinds)
			}
			for _, scope := range plan.SharedDelivery.Scopes {
				if !scope.CrossPartAssessmentRequired {
					t.Fatal("producer lost whole-scope obligation")
				}
				for _, artifact := range scope.ArtifactIDs {
					if _, exists := reconstructed[artifact]; !exists {
						t.Fatal("scope artifact was not reconstructed")
					}
				}
			}
		})
	}
}

// These controlled bad-producer cases deliberately renew both caller pins so
// that refusal must come from the named structural guard, not stale identity.
func resealMaterializeTestPlan(t *testing.T, plan *CodePlan) []byte {
	t.Helper()
	plan.SharedDelivery.Fingerprint = ""
	var compact bytes.Buffer
	encoder := json.NewEncoder(&compact)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(plan.SharedDelivery); err != nil {
		t.Fatal(err)
	}
	plan.SharedDelivery.Fingerprint = digest(bytes.TrimSuffix(compact.Bytes(), []byte{'\n'}))
	plan.Fingerprint = ""
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	plan.Fingerprint = digest(raw)
	raw, err = json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestMaterializeRefusesMalformedEmbeddedInventoryBeforeWriting(t *testing.T) {
	_, pristine := nativeMaterializeFixture(t, "sha1")
	cases := []struct {
		name, diagnostic string
		mutate           func(*CodePlan)
	}{
		{"foreign-ref", "canonical reference", func(p *CodePlan) { p.SharedDelivery.Artifacts[0].CanonicalRef.Collection = "../../outside" }},
		{"wrong-field", "canonical reference", func(p *CodePlan) { p.SharedDelivery.Artifacts[0].CanonicalRef.Field = "path" }},
		{"negative-index", "canonical artifact", func(p *CodePlan) { p.SharedDelivery.Artifacts[0].CanonicalRef.Index = -1 }},
		{"outside-index", "canonical reference", func(p *CodePlan) { p.SharedDelivery.Artifacts[0].CanonicalRef.Index = 99999 }},
		{"duplicate-artifact", "canonical artifact", func(p *CodePlan) {
			p.SharedDelivery.Artifacts = append(p.SharedDelivery.Artifacts, p.SharedDelivery.Artifacts[0])
		}},
		{"duplicate-atom", "fragment identity", func(p *CodePlan) { p.SharedDelivery.Atoms = append(p.SharedDelivery.Atoms, p.SharedDelivery.Atoms[0]) }},
		{"orphan-atom", "fragment identity", func(p *CodePlan) { p.SharedDelivery.Atoms[0].ArtifactID = strings.Repeat("f", 64) }},
		{"missing-membership", "membership", func(p *CodePlan) { p.SharedDelivery.Artifacts[0].Atoms = nil }},
		{"bad-range", "range", func(p *CodePlan) { p.SharedDelivery.Atoms[0].Length = int(^uint(0) >> 1) }},
		{"bad-piece-hash", "fragment bytes", func(p *CodePlan) { p.SharedDelivery.Atoms[0].SHA256 = strings.Repeat("0", 64) }},
		{"split-codepoint", "fragment bytes", func(p *CodePlan) {
			content := p.Policies[0].Content
			start := strings.Index(content, "é") + 1
			p.SharedDelivery.Atoms[0].Offset = start
			p.SharedDelivery.Atoms[0].Length = 1
			p.SharedDelivery.Atoms[0].SHA256 = digest([]byte(content[start : start+1]))
		}},
		{"changed-metadata", "canonical artifact changed", func(p *CodePlan) { p.SharedDelivery.Artifacts[0].Metadata["path"] = "foreign-policy.md" }},
		{"token-underclaim", "fragment budget mismatch", func(p *CodePlan) { p.SharedDelivery.Atoms[0].EstimatedInputTokens-- }},
		{"fragment-overflow", "capacity", func(p *CodePlan) { p.SharedDelivery.Limits.FragmentLimitExceeded = true }},
		{"token-overflow", "capacity", func(p *CodePlan) {
			p.SharedDelivery.Atoms[0].EstimatedInputTokens = p.SharedDelivery.Limits.MaxFragmentInputTokens + 1
		}},
		{"wrong-coverage", "coverage link", func(p *CodePlan) { p.SharedDelivery.CoverageFingerprint = strings.Repeat("f", 64) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var plan CodePlan
			if err := json.Unmarshal(pristine, &plan); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&plan)
			raw := resealMaterializeTestPlan(t, &plan)
			var out bytes.Buffer
			err := RunMaterializeCLI(materializeTestArgs(plan, materializeTestFile(t, raw), plan.SharedDelivery.Atoms[0].ID), &out)
			if err == nil || out.Len() != 0 || !strings.Contains(err.Error(), tc.diagnostic) {
				t.Fatalf("targeted guard not reached: out=%q err=%v", out.String(), err)
			}
		})
	}
}

func TestMaterializeRequiresExternalPinsAndStrictJSON(t *testing.T) {
	plan, raw := nativeMaterializeFixture(t, "sha1")
	atom := plan.SharedDelivery.Atoms[0].ID
	var changed CodePlan
	if err := json.Unmarshal(raw, &changed); err != nil {
		t.Fatal(err)
	}
	changed.Policies[0].Content += "\nTampered policy body.\n"
	changedBody, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		raw  []byte
	}{
		{"unknown", bytes.Replace(raw, []byte("{\n"), []byte("{\n\"foreign\":true,"), 1)},
		{"duplicate-root", bytes.Replace(raw, []byte("{\n"), []byte("{\n\"schema\":\"adrian-code-plan-v2\","), 1)},
		{"duplicate-nested", bytes.Replace(raw, []byte("\"collection\": \"policies\""), []byte("\"collection\": \"policies\",\"collection\":\"policies\""), 1)},
		{"trailing", append(append([]byte{}, raw...), []byte("{}")...)},
		{"legacy-null", bytes.Replace(raw, []byte("{\n"), []byte("{\n\"packet_plan\":null,"), 1)},
		{"changed-body", changedBody},
		{"null", []byte("null")},
		{"truncated", raw[:len(raw)/2]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if bytes.Equal(tc.raw, raw) {
				t.Fatal("negative fixture did not change its intended input")
			}
			var out bytes.Buffer
			if err := RunMaterializeCLI(materializeTestArgs(plan, materializeTestFile(t, tc.raw), atom), &out); err == nil || out.Len() != 0 {
				t.Fatalf("malformed/pin-changed plan accepted: %v %q", err, out.String())
			}
		})
	}
	file := materializeTestFile(t, raw)
	for _, flag := range []string{"--expected-plan-fingerprint", "--expected-shared-fingerprint", "--atom"} {
		args := materializeTestArgs(plan, file, atom)
		for i := 0; i < len(args); i += 2 {
			if args[i] == flag {
				args[i+1] = strings.Repeat("f", 64)
			}
		}
		var out bytes.Buffer
		if err := RunMaterializeCLI(args, &out); err == nil || out.Len() != 0 {
			t.Fatalf("wrong external %s accepted", flag)
		}
	}
}

func TestMaterializeCanonicalFieldsCannotBeCaseAliases(t *testing.T) {
	plan, raw := nativeMaterializeFixture(t, "sha1")
	for _, tc := range []struct {
		name string
		raw  []byte
	}{
		{"schema-alias", bytes.Replace(raw, []byte("{\n"), []byte("{\n\"Schema\":\"adrian-code-plan-v2\","), 1)},
		{"legacy-alias", bytes.Replace(raw, []byte("{\n"), []byte("{\n\"PACKET_PLAN\":null,"), 1)},
		{"shared-alias", bytes.Replace(raw, []byte("{\n"), []byte("{\n\"SHARED_DELIVERY\":null,"), 1)},
		{"nested-alias", bytes.Replace(raw, []byte("\"coverage\": {"), []byte("\"coverage\": {\"FINGERPRINT\":\""+plan.Coverage.Fingerprint+"\","), 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if bytes.Equal(tc.raw, raw) {
				t.Fatal("case-alias probe did not change real producer bytes")
			}
			var out bytes.Buffer
			if err := RunMaterializeCLI(materializeTestArgs(plan, materializeTestFile(t, tc.raw), plan.SharedDelivery.Atoms[0].ID), &out); err == nil || out.Len() != 0 {
				t.Fatalf("case alias bypassed strict field grammar: err=%v bytes=%d", err, out.Len())
			}
		})
	}
}

func TestMaterializeMetadataKeysRemainDataAndNativeIdentityIgnoresFormatting(t *testing.T) {
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal([]byte("{\"shared_delivery\":{\"artifacts\":[{\"metadata\":{\"Schema\":true,\"schema\":false,\"PACKET_PLAN\":{\"AnyKey\":1}}}]}}"), &metadata); err != nil {
		t.Fatal(err)
	}
	if err := materializeObjectFields(metadata, reflect.TypeFor[CodePlan](), map[reflect.Type]map[string]reflect.Type{}); err != nil {
		t.Fatalf("metadata data keys treated as schema aliases: %v", err)
	}
	plan, raw := nativeMaterializeFixture(t, "sha1")
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	reordered, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(raw, reordered) {
		t.Fatal("formatting control did not change raw bytes")
	}
	var original, reformatted bytes.Buffer
	atom := plan.SharedDelivery.Atoms[0].ID
	if err := RunMaterializeCLI(materializeTestArgs(plan, materializeTestFile(t, raw), atom), &original); err != nil {
		t.Fatal(err)
	}
	if err := RunMaterializeCLI(materializeTestArgs(plan, materializeTestFile(t, reordered), atom), &reformatted); err != nil || !bytes.Equal(original.Bytes(), reformatted.Bytes()) {
		t.Fatalf("native identity became a raw-file hash: %v", err)
	}
}

func TestMaterializeRequiresCompleteFileOnlyArguments(t *testing.T) {
	plan, raw := nativeMaterializeFixture(t, "sha1")
	file := materializeTestFile(t, raw)
	atom := plan.SharedDelivery.Atoms[0].ID
	for _, flag := range []string{"--expected-plan-fingerprint", "--expected-shared-fingerprint", "--atom"} {
		args := materializeTestArgs(plan, file, atom)
		for i := 0; i < len(args); i += 2 {
			if args[i] == flag {
				args = append(args[:i], args[i+2:]...)
				break
			}
		}
		var out bytes.Buffer
		if err := RunMaterializeCLI(args, &out); err == nil || out.Len() != 0 || !strings.Contains(err.Error(), flag) {
			t.Fatalf("missing %s did not refuse before writing: %v", flag, err)
		}
	}
	for _, extra := range [][]string{{"--atom", atom}, {"--adapter", "must-not-execute"}} {
		args := append(materializeTestArgs(plan, file, atom), extra...)
		var out bytes.Buffer
		if err := RunMaterializeCLI(args, &out); err == nil || out.Len() != 0 {
			t.Fatalf("invalid argument accepted: %v", err)
		}
	}
	link := filepath.Join(t.TempDir(), "plan-link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"-", t.TempDir(), link} {
		var out bytes.Buffer
		if err := RunMaterializeCLI(materializeTestArgs(plan, source, atom), &out); err == nil || out.Len() != 0 {
			t.Fatalf("nonregular/stdin plan accepted: %v", err)
		}
	}
}

func TestMaterializeAdmitsProducerNewlineAtByteBoundary(t *testing.T) {
	plan, raw := nativeMaterializeFixture(t, "sha1")
	file := materializeTestFile(t, raw)
	stream, err := os.OpenFile(file, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	writer := bufio.NewWriter(stream)
	padding := bytes.Repeat([]byte{' '}, 64*1024)
	for remaining := maxMaterializePlanBytes - int64(len(raw)); remaining > 0; {
		size := min(remaining, int64(len(padding)))
		if _, err := writer.Write(padding[:size]); err != nil {
			t.Fatal(err)
		}
		remaining -= size
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	args := materializeTestArgs(plan, file, plan.SharedDelivery.Atoms[0].ID)
	var out bytes.Buffer
	if err := RunMaterializeCLI(args, &out); err != nil || out.Len() == 0 {
		t.Fatalf("valid native identity at encoded-byte boundary refused: %v", err)
	}
	if err := os.Truncate(file, maxMaterializePlanBytes+1); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := RunMaterializeCLI(args, &out); err == nil || out.Len() != 0 {
		t.Fatalf("over-capacity plan accepted: %v", err)
	}
}
