package plancmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wbern/adrian/go/internal/sharedpacket"
)

func TestMaterializeV3RetainsNativeEnvelopesForBothIdentityKinds(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		for _, identity := range []struct{ kind, repository string }{{"local", "local:gc-chat"}, {"github", "owner/repo"}} {
			t.Run(format+"/"+identity.kind, func(t *testing.T) {
				dir, base, head := codeIdentityFixture(t, format)
				plan, raw := identityPlan(t, dir, base, head, identity.kind, identity.repository)
				inputs, err := materializeInputs(plan)
				if err != nil {
					t.Fatal(err)
				}
				file := materializeTestFile(t, raw)
				for _, atom := range plan.SharedDelivery.Atoms {
					expected, err := sharedpacket.Materialize(*plan.SharedDelivery, atom.ID, inputs, plan.SharedDelivery.Fingerprint)
					if err != nil {
						t.Fatal(err)
					}
					var out bytes.Buffer
					if err := RunMaterializeCLI(materializeTestArgs(plan, file, atom.ID), &out); err != nil || !bytes.Equal(expected, out.Bytes()) {
						t.Fatalf("V3 changed native envelope: %v", err)
					}
				}
			})
		}
	}
}

func TestMaterializeVersionedIdentityRefusesRenewedPinMalformedPlans(t *testing.T) {
	dir, base, head := codeIdentityFixture(t, "sha1")
	_, pristine := identityPlan(t, dir, base, head, "local", "local:gc-chat")
	for _, tc := range []struct {
		name, diagnostic string
		change           func(*CodePlan)
		rawKind          string
	}{
		{"missing-kind", "V3 repository_kind", func(p *CodePlan) { p.RepositoryKind = "" }, ""},
		{"null-kind", "V3 repository_kind", func(p *CodePlan) { p.RepositoryKind = "" }, "null"},
		{"empty-kind", "V3 repository_kind", func(p *CodePlan) { p.RepositoryKind = "" }, "\"\""},
		{"wrong-kind", "V3 repository_kind", func(p *CodePlan) { p.RepositoryKind = "github" }, ""},
		{"unknown-kind", "V3 repository_kind", func(p *CodePlan) { p.RepositoryKind = "another" }, ""},
		{"wrong-namespace", "V3 repository_kind", func(p *CodePlan) { p.Repository = "owner/repo" }, ""},
		{"schema-downgrade", "repository_kind requires", func(p *CodePlan) { p.Schema = "adrian-code-plan-v2" }, ""},
		{"unknown-schema", "V2 or V3", func(p *CodePlan) { p.Schema = "adrian-code-plan-v4" }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var plan CodePlan
			if err := json.Unmarshal(pristine, &plan); err != nil {
				t.Fatal(err)
			}
			tc.change(&plan)
			// Renew both controlled caller pins, forcing structural admission to
			// reject this malformed producer instead of relying on stale hashes.
			raw := resealMaterializeTestPlan(t, &plan)
			if tc.rawKind != "" {
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(raw, &fields); err != nil {
					t.Fatal(err)
				}
				fields["repository_kind"] = json.RawMessage(tc.rawKind)
				var err error
				raw, err = json.Marshal(fields)
				if err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			err := RunMaterializeCLI(materializeTestArgs(plan, materializeTestFile(t, raw), plan.SharedDelivery.Atoms[0].ID), &out)
			if err == nil || out.Len() != 0 || !strings.Contains(err.Error(), tc.diagnostic) {
				t.Fatalf("schema/identity guard was not reached: %v bytes=%d", err, out.Len())
			}
		})
	}
}

func TestMaterializeV2RejectsKindPresenceDespiteMatchingNativeIdentity(t *testing.T) {
	dir, base, head := codeIdentityFixture(t, "sha1")
	_, pristine := identityPlan(t, dir, base, head, "", "owner/repo")
	for _, value := range []string{"null", "\"\"", "\"github\"", "\"local\""} {
		var plan CodePlan
		if err := json.Unmarshal(pristine, &plan); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(value), &plan.RepositoryKind); err != nil {
			t.Fatal(err)
		}
		sealed := resealMaterializeTestPlan(t, &plan)
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(sealed, &fields); err != nil {
			t.Fatal(err)
		}
		fields["repository_kind"] = json.RawMessage(value)
		raw, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		err = RunMaterializeCLI(materializeTestArgs(plan, materializeTestFile(t, raw), plan.SharedDelivery.Atoms[0].ID), &out)
		if err == nil || out.Len() != 0 || !strings.Contains(err.Error(), "repository_kind requires") {
			t.Fatalf("V2 silently accepted a V3-only field %s: %v", value, err)
		}
	}
}

func TestMaterializeV3RejectsKindAliasesAndLegacyDelivery(t *testing.T) {
	dir, base, head := codeIdentityFixture(t, "sha1")
	plan, raw := identityPlan(t, dir, base, head, "local", "local:gc-chat")
	for _, key := range []string{"Repository_Kind", "packet_plan", "delivery"} {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		if key == "Repository_Kind" {
			fields[key] = json.RawMessage("\"local\"")
		} else {
			fields[key] = json.RawMessage("null")
		}
		malformed, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		err = RunMaterializeCLI(materializeTestArgs(plan, materializeTestFile(t, malformed), plan.SharedDelivery.Atoms[0].ID), &out)
		if err == nil || out.Len() != 0 || strings.Contains(err.Error(), "fingerprint") {
			t.Fatalf("noncanonical V3 field reached identity verification: %s %v", key, err)
		}
	}
}
