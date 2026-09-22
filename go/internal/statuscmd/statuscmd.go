// Package statuscmd contains the shared implementation used by the
// single-arity status-setting subcommands (accept, reject, withdraw,
// deprecate). It looks up the ADR by id, flips the YAML status line,
// and writes the file atomically.
package statuscmd

import (
	"fmt"
	"io"
	"os"

	"github.com/wbern/adrian/go/internal/adr"
)

// Spec parametrizes SetStatus for each lifecycle verb. Verb is the
// past-tense word used in the success message (e.g. "Accepted").
type Spec struct {
	Status adr.Status
	Usage  string
	Verb   string
}

// SetStatus rewrites the YAML status line of the ADR identified by
// args[0]. Returns an error on arity mismatch, unknown id, or malformed
// frontmatter.
func SetStatus(args []string, dir string, out io.Writer, spec Spec) error {
	if len(args) != 1 {
		return fmt.Errorf("expected 1 id: usage: %s", spec.Usage)
	}
	want := adr.NormalizeID(args[0])
	adrs, err := adr.LoadADRs(dir)
	if err != nil {
		return err
	}
	for _, a := range adrs {
		if adr.NormalizeID(a.ID) != want {
			continue
		}
		body, err := os.ReadFile(a.FilePath)
		if err != nil {
			return err
		}
		updated, ok := adr.SetStatus(string(body), string(spec.Status))
		if !ok {
			return fmt.Errorf("ADR %s has no status line in frontmatter", args[0])
		}
		if err := adr.WriteFileAtomic(a.FilePath, []byte(updated), 0644); err != nil {
			return err
		}
		fmt.Fprintf(out, "%s %s\n", spec.Verb, adr.DisplayPath(a.FilePath))
		return nil
	}
	return fmt.Errorf("ADR %s not found", args[0])
}
