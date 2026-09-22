// Package createcmd implements the `adr-lint create <title>` subcommand,
// which scaffolds a new ADR markdown file.
package createcmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/wbern/adrian/go/internal/adr"
)

// Run writes a new ADR under dir using the title joined from args, and
// prints the created file's path to out.
func Run(args []string, dir string, out io.Writer) error {
	title := strings.TrimSpace(strings.Join(args, " "))
	if title == "" {
		return fmt.Errorf("expected a title: usage: adr-lint create <title>")
	}
	path, err := adr.Create(dir, title)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Created %s\n", adr.DisplayPath(path))
	return nil
}
