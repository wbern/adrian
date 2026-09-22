// Command adr-lint preserves the historical ADRian executable name.
package main

import (
	"os"

	"github.com/wbern/adrian/go/internal/cli"
)

func main() { os.Exit(cli.Run("adr-lint", os.Args[1:], os.Stdout, os.Stderr)) }
