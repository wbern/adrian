// Package withdrawcmd implements `adr-lint withdraw <id>`, flipping an
// ADR's frontmatter status to "withdrawn".
package withdrawcmd

import (
	"io"

	"github.com/wbern/adrian/go/internal/adr"
	"github.com/wbern/adrian/go/internal/statuscmd"
)

// Run flips the ADR identified by args[0] to status "withdrawn".
func Run(args []string, dir string, out io.Writer) error {
	return statuscmd.SetStatus(args, dir, out, statuscmd.Spec{
		Status: adr.StatusWithdrawn,
		Usage:  "adr-lint withdraw <id>",
		Verb:   "Withdrawn",
	})
}
