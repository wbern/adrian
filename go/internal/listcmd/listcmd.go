// Package listcmd implements the `adr-lint list` subcommand, which prints
// a one-line summary of every ADR found under the given directory.
package listcmd

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/wbern/adrian/go/internal/adr"
)

// Run prints id, status, and title for each ADR under dir. It takes no
// positional args; any extras are rejected.
func Run(args []string, dir string, out io.Writer) error {
	if len(args) > 0 {
		return fmt.Errorf("unexpected args: usage: adr-lint list")
	}
	adrs, err := adr.LoadADRs(dir)
	if err != nil {
		return err
	}
	if len(adrs) == 0 {
		fmt.Fprintln(out, "No ADRs found")
		return nil
	}
	for _, a := range adrs {
		id := a.ID
		if n, err := strconv.Atoi(id); err == nil {
			id = fmt.Sprintf("%04d", n)
		}
		title := a.Title
		if len(a.SupersededBy) > 0 {
			title = fmt.Sprintf("%s (by %s)", title, strings.Join(a.SupersededBy, ", "))
		}
		fmt.Fprintf(out, "%s  %-10s  %s\n", id, a.Status, title)
	}
	return nil
}
