// Command adrian validates architecture decisions and computes review policy.
package main

import (
	"os"

	"github.com/wbern/adrian/go/internal/cli"
)

func main() { os.Exit(cli.Run("adrian", os.Args[1:], os.Stdout, os.Stderr)) }
