// Package acceptcmd implements `adr-lint accept <id>`, flipping an ADR's
// frontmatter status to "accepted".
package acceptcmd

import (
	"io"

	"github.com/wbern/adrian/go/internal/adr"
	"github.com/wbern/adrian/go/internal/statuscmd"
)

// Run flips the ADR identified by args[0] to status "accepted".
func Run(args []string, dir string, out io.Writer) error {
	return statuscmd.SetStatus(args, dir, out, statuscmd.Spec{
		Status: adr.StatusAccepted,
		Usage:  "adr-lint accept <id>",
		Verb:   "Accepted",
	})
}
