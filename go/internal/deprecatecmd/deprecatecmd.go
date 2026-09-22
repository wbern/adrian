// Package deprecatecmd implements the `adr-lint deprecate <id>` subcommand,
// which flips an ADR's frontmatter status to "deprecated".
package deprecatecmd

import (
	"io"

	"github.com/wbern/adrian/go/internal/adr"
	"github.com/wbern/adrian/go/internal/statuscmd"
)

// Run flips the ADR identified by args[0] to status "deprecated".
func Run(args []string, dir string, out io.Writer) error {
	return statuscmd.SetStatus(args, dir, out, statuscmd.Spec{
		Status: adr.StatusDeprecated,
		Usage:  "adr-lint deprecate <id>",
		Verb:   "Deprecated",
	})
}
