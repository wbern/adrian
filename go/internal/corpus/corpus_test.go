package corpus

import (
	"testing"
)

func TestCanonicalRepositoryPaths(t *testing.T) {
	for _, p := range []string{"", ".", "..", "../adr", "a/../adr", "a/./adr", "a//adr", "a/", "/adr", `C:\adr`, `a\adr`, "a\nadr", "a\x00adr"} {
		t.Run(p, func(t *testing.T) {
			if err := ValidatePath(p); err == nil {
				t.Fatalf("accepted %q", p)
			}
		})
	}
	for _, p := range []string{"doc/adr", "docs/adr", "architecture/decision records"} {
		if err := ValidatePath(p); err != nil {
			t.Fatal(err)
		}
	}
}
func TestTOMLDocumentValidation(t *testing.T) {
	for _, raw := range []string{"[corpus]\n", "[corpus]\ndir = 1\n", "[corpus]\ndir = \"doc/adr\"\ndir = \"docs/adr\"", "[corpus]\ndir = \"doc/adr\"\nextra = true", "[corpus]\ndir = \"doc/adr\"\n[corpus]\n", "corpus = \"doc/adr\"", "[budget\n"} {
		if _, err := ConfigDir([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid TOML/config: %q", raw)
		}
	}
	dir, err := ConfigDir([]byte("version=1\n[budget]\nmode='auto'\n[corpus]\ndir='docs/adr'\n[other]\narr=[1,2]\n"))
	if err != nil || dir != "docs/adr" {
		t.Fatalf("valid unrelated config rejected: %s %v", dir, err)
	}
}
