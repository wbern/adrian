// Package showcmd implements the `adr-lint show <id>` subcommand, which
// prints the raw contents of one ADR.
package showcmd

import (
	"fmt"
	"io"
	"os"

	"github.com/wbern/adrian/go/internal/adr"
)

// Run finds the ADR whose ID matches args[0] and writes its file contents
// to out.
func Run(args []string, dir string, out io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("expected 1 id: usage: adr-lint show <id>")
	}
	want := adr.NormalizeID(args[0])
	adrs, err := adr.LoadADRs(dir)
	if err != nil {
		return err
	}
	for _, a := range adrs {
		if adr.NormalizeID(a.ID) == want {
			body, err := os.ReadFile(a.FilePath)
			if err != nil {
				return err
			}
			_, err = out.Write(body)
			return err
		}
	}
	return fmt.Errorf("ADR %s not found", args[0])
}
