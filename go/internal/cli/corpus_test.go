package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func corpusRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q", dir)
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("%s %v", b, e)
	}
	t.Chdir(dir)
	return dir
}
func corpusWrite(t *testing.T, root, p, s string) {
	t.Helper()
	p = filepath.Join(root, p)
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(s), 0644); e != nil {
		t.Fatal(e)
	}
}

const corpusRecord = "---\nstatus: accepted\napplies_to: ['src/**']\n---\n# 1. Rule\n\n## Decision\nKeep the rule.\n"

func TestCorpusLayoutsAtCLI(t *testing.T) {
	for _, name := range []string{"adrian", "adr-lint"} {
		for _, layout := range []string{"doc/adr", "docs/adr", "architecture/decisions/records"} {
			t.Run(name+layout, func(t *testing.T) {
				dir := corpusRepo(t)
				corpusWrite(t, dir, layout+"/0001-rule.md", corpusRecord)
				corpusWrite(t, dir, "unrelated.txt", "no applicable rule\n")
				if strings.HasPrefix(layout, "architecture") {
					corpusWrite(t, dir, ".adrian.toml", "version = 1\n[corpus]\ndir = \""+layout+"\"\n[budget]\nmode = \"auto\"\n")
				}
				for _, args := range [][]string{{"validate"}, {"list"}, {"show", "1"}, {"resolve", "src/a.go"}, {"audit", "--format", "json"}, {"check", "--files", "unrelated.txt"}} {
					var out, err bytes.Buffer
					if code := Run(name, args, &out, &err); code != 0 {
						t.Errorf("%v exit %d: %s", args, code, err.String())
					}
					if args[0] == "list" && !strings.Contains(out.String(), "Rule") {
						t.Errorf("corpus ignored: %s", out.String())
					}
				}
			})
		}
	}
}
func TestMissingAndEmptyCorpusFailBeforeNoChanges(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "empty"}[empty], func(t *testing.T) {
			dir := corpusRepo(t)
			if empty {
				if e := os.MkdirAll(filepath.Join(dir, "doc/adr"), 0755); e != nil {
					t.Fatal(e)
				}
			}
			for _, args := range [][]string{{"validate"}, {"list"}, {"resolve"}, {"audit"}, {"check"}} {
				var out, err bytes.Buffer
				if code := Run("adrian", args, &out, &err); code == 0 || !strings.Contains(strings.ToLower(err.String()), "corpus") {
					t.Errorf("%v accepted absent/empty corpus: %d %s %s", args, code, out.String(), err.String())
				}
			}
		})
	}
}
func TestInitPreservesExistingCorpusAndBudget(t *testing.T) {
	dir := corpusRepo(t)
	original := "version = 1\n[budget]\nmode = \"monolithic\"\n"
	corpusWrite(t, dir, ".adrian.toml", original)
	corpusWrite(t, dir, "docs/adr/0001-existing.md", corpusRecord)
	var out, err bytes.Buffer
	if code := Run("adrian", []string{"init", "--docs-dir", "docs/adr", "--force"}, &out, &err); code != 0 {
		t.Fatal(err.String())
	}
	files, _ := filepath.Glob(filepath.Join(dir, "docs/adr/0001-*"))
	if len(files) != 1 {
		t.Fatalf("duplicate 0001: %v", files)
	}
	config, _ := os.ReadFile(filepath.Join(dir, ".adrian.toml"))
	if !strings.Contains(string(config), original) || !strings.Contains(string(config), "[corpus]") {
		t.Fatalf("config not preserved/persisted: %s", config)
	}
}

func TestInvalidCorpusConfigurationAndSymlinks(t *testing.T) {
	for _, config := range []string{
		"[corpus]\n", "[corpus]\ndir = \"\"\n", "[corpus]\ndir = \"doc/adr\"\ndir = \"docs/adr\"\n", "[corpus]\ndir = \"doc/adr\"\nother = true\n", "[corpus]\ndir = \"../adr\"\n", "[corpus]\ndir = \"doc//adr\"\n", "[corpus]\ndir = \"/tmp/adr\"\n", "[budget\n",
	} {
		t.Run(config, func(t *testing.T) {
			dir := corpusRepo(t)
			corpusWrite(t, dir, "doc/adr/0001-rule.md", corpusRecord)
			corpusWrite(t, dir, ".adrian.toml", config)
			var out, err bytes.Buffer
			if code := Run("adrian", []string{"validate"}, &out, &err); code == 0 {
				t.Fatalf("accepted bad config: %s", config)
			}
		})
	}
	for _, link := range []string{"config", "parent", "record"} {
		t.Run(link, func(t *testing.T) {
			dir := corpusRepo(t)
			external := t.TempDir()
			corpusWrite(t, external, "adr/0001-rule.md", corpusRecord)
			switch link {
			case "config":
				corpusWrite(t, dir, "doc/adr/0001-rule.md", corpusRecord)
				corpusWrite(t, external, "config.toml", "version = 1\n")
				if e := os.Symlink(filepath.Join(external, "config.toml"), filepath.Join(dir, ".adrian.toml")); e != nil {
					t.Fatal(e)
				}
			case "parent":
				if e := os.Symlink(external, filepath.Join(dir, "doc")); e != nil {
					t.Fatal(e)
				}
			case "record":
				if e := os.MkdirAll(filepath.Join(dir, "doc/adr"), 0755); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(filepath.Join(external, "adr/0001-rule.md"), filepath.Join(dir, "doc/adr/0001-rule.md")); e != nil {
					t.Fatal(e)
				}
			}
			var out, err bytes.Buffer
			if code := Run("adrian", []string{"validate"}, &out, &err); code == 0 || !strings.Contains(err.String(), "symlink") {
				t.Fatalf("accepted %s symlink: %d %s", link, code, err.String())
			}
		})
	}
}
func TestAmbiguousCorpusRequiresConfiguration(t *testing.T) {
	dir := corpusRepo(t)
	corpusWrite(t, dir, "doc/adr/0001-rule.md", corpusRecord)
	if e := os.MkdirAll(filepath.Join(dir, "docs/adr"), 0755); e != nil {
		t.Fatal(e)
	}
	var out, err bytes.Buffer
	if code := Run("adrian", []string{"validate"}, &out, &err); code == 0 || !strings.Contains(err.String(), "ambiguous") {
		t.Fatalf("ambiguity ignored: %d %s", code, err.String())
	}
	corpusWrite(t, dir, ".adrian.toml", "[corpus]\ndir = \"doc/adr\"\n")
	out.Reset()
	err.Reset()
	if code := Run("adrian", []string{"validate"}, &out, &err); code != 0 {
		t.Fatal(err.String())
	}
}

func TestInitCustomCorpusRoundTripAndCreateBootstrap(t *testing.T) {
	for _, command := range []string{"init", "create"} {
		t.Run(command, func(t *testing.T) {
			dir := corpusRepo(t)
			var out, err bytes.Buffer
			args := []string{"init", "--docs-dir", "architecture/records"}
			if command == "create" {
				args = []string{"create", "Fresh decision"}
			}
			if code := Run("adrian", args, &out, &err); code != 0 {
				t.Fatal(err.String())
			}
			out.Reset()
			err.Reset()
			if code := Run("adr-lint", []string{"validate"}, &out, &err); code != 0 {
				t.Fatalf("bootstrap not consumable: %s", err.String())
			}
			if command == "init" {
				config, _ := os.ReadFile(filepath.Join(dir, ".adrian.toml"))
				if !strings.Contains(string(config), `dir = "architecture/records"`) {
					t.Fatalf("selection not persisted: %s", config)
				}
			}
		})
	}
}

func TestValidateInvalidSupersessionInBothLayouts(t *testing.T) {
	for _, layout := range []string{"doc/adr", "docs/adr"} {
		t.Run(layout, func(t *testing.T) {
			dir := corpusRepo(t)
			corpusWrite(t, dir, layout+"/0001-rule.md", "---\nstatus: superseded\nsuperseded_by: ['0099']\n---\n# 1. Invalid supersession\n")
			var out, err bytes.Buffer
			if code := Run("adrian", []string{"validate"}, &out, &err); code == 0 || !strings.Contains(err.String(), "0099") {
				t.Fatalf("invalid record ignored: %d %s %s", code, out.String(), err.String())
			}
		})
	}
}

func TestInitPreservesCorpusWithPatternCharacters(t *testing.T) {
	dir := corpusRepo(t)
	record := "---\nstatus: accepted\n---\n# Existing first decision\n"
	path := "architecture/[special]/0001-record-architecture-decisions.md"
	corpusWrite(t, dir, path, record)
	var out, err bytes.Buffer
	if code := Run("adrian", []string{"init", "--docs-dir", "architecture/[special]"}, &out, &err); code != 0 {
		t.Fatal(err.String())
	}
	got, e := os.ReadFile(filepath.Join(dir, path))
	if e != nil {
		t.Fatal(e)
	}
	if string(got) != record {
		t.Fatalf("existing record overwritten: %s", got)
	}
}
