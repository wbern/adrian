package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wbern/adrian/go/internal/plancmd"
	"github.com/wbern/adrian/go/internal/sharedpacket"
)

func TestMaterializeHelpNamesBothAliases(t *testing.T) {
	for _, name := range []string{"adrian", "adr-lint"} {
		for _, args := range [][]string{{"materialize", "--help"}, {"help"}} {
			var out, stderr bytes.Buffer
			if code := Run(name, args, &out, &stderr); code != 0 || !strings.Contains(out.String(), name+" materialize --plan") {
				t.Fatalf("wrong materialize help: exit%d out=%q err=%s", code, out.String(), stderr.String())
			}
		}
	}
}

func TestMaterializeCommandUsesPinnedNativeInputsWithoutRepositoryDiscovery(t *testing.T) {
	for _, name := range []string{"adrian", "adr-lint"} {
		for _, kind := range []string{"", "github", "local"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				root, base, head := bindingFixture(t)
				var produced bytes.Buffer
				repository := "owner/repo"
				if kind == "local" {
					repository = "local:gc-chat"
				}
				producerArgs := []string{"code-plan", "--repository", repository, "--base", base, "--head", head, "--policy", base}
				if kind != "" {
					producerArgs = append(producerArgs, "--repository-kind", kind)
				}
				t.Chdir(root)
				var producerError bytes.Buffer
				if code := Run(name, producerArgs, &produced, &producerError); code != 0 || producerError.Len() != 0 {
					t.Fatalf("code-plan exit%d: %s", code, producerError.String())
				}
				var plan plancmd.CodePlan
				if err := json.Unmarshal(produced.Bytes(), &plan); err != nil {
					t.Fatal(err)
				}
				if plan.SharedDelivery == nil || len(plan.SharedDelivery.Atoms) == 0 {
					t.Fatal("real producer omitted shared inputs")
				}
				shared := plan.SharedDelivery
				inputs := make([]sharedpacket.Input, len(shared.Artifacts))
				for i, artifact := range shared.Artifacts {
					ref := artifact.CanonicalRef
					var content string
					switch ref.Collection {
					case "policies":
						content = plan.Policies[ref.Index].Content
					case "content":
						content = plan.Content[ref.Index].Content
					case "root":
						content = plan.Diff
					case "change_diffs":
						content = plan.ChangeDiffs[ref.Index].Diff
					default:
						t.Fatalf("unexpected real canonical ref: %+v", ref)
					}
					inputs[i] = sharedpacket.Input{Kind: artifact.Kind, CanonicalRef: ref, Encoding: artifact.Encoding, Metadata: artifact.Metadata, Content: content}
				}
				atom := shared.Atoms[0].ID
				expected, err := sharedpacket.Materialize(*shared, atom, inputs, shared.Fingerprint)
				if err != nil {
					t.Fatal(err)
				}
				home := t.TempDir()
				file := filepath.Join(home, "producer.json")
				if err = os.WriteFile(file, produced.Bytes(), 0600); err != nil {
					t.Fatal(err)
				}
				marker := filepath.Join(home, "unexpected-tool")
				guard := filepath.Join(home, "bin")
				if err = os.Mkdir(guard, 0700); err != nil {
					t.Fatal(err)
				}
				for _, tool := range []string{"git", "claude", "codex", "gc", "node", "adapter"} {
					if err = os.WriteFile(filepath.Join(guard, tool), []byte("#!/bin/sh\necho attempted > "+marker+"\nexit 91\n"), 0700); err != nil {
						t.Fatal(err)
					}
				}
				t.Chdir(home)
				if err = os.WriteFile(filepath.Join(home, ".env"), []byte("invalid dotenv content\n"), 0600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", guard)
				t.Setenv("GIT_DIR", filepath.Join(home, "foreign.git"))
				t.Setenv("ADRIAN_PROVIDER", "hostile")
				var out, stderr bytes.Buffer
				args := []string{"materialize", "--plan", file, "--expected-plan-fingerprint", plan.Fingerprint, "--expected-shared-fingerprint", shared.Fingerprint, "--atom", atom}
				if code := Run(name, args, &out, &stderr); code != 0 || !bytes.Equal(out.Bytes(), expected) || stderr.Len() != 0 {
					t.Fatalf("materialize exit%d stdout=%q stderr=%s", code, out.String(), stderr.String())
				}
				if _, err = os.Stat(marker); !os.IsNotExist(err) {
					t.Fatalf("unexpected external tool: %v", err)
				}
			})
		}
	}
}
