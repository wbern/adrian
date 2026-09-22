// Package rejectcmd implements `adr-lint reject <id>`, flipping an ADR's
// frontmatter status to "rejected".
package rejectcmd

import (
	"io"

	"github.com/wbern/adrian/go/internal/adr"
	"github.com/wbern/adrian/go/internal/statuscmd"
)

// Run flips the ADR identified by args[0] to status "rejected".
func Run(args []string, dir string, out io.Writer) error {
	return statuscmd.SetStatus(args, dir, out, statuscmd.Spec{
		Status: adr.StatusRejected,
		Usage:  "adr-lint reject <id>",
		Verb:   "Rejected",
	})
}
