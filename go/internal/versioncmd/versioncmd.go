// Package versioncmd implements the `adr-lint version` subcommand, which
// prints the binary's module version as embedded by the Go toolchain.
package versioncmd

import (
	"fmt"
	"io"
	"runtime/debug"
)

// version is set at build time via -ldflags "-X .../versioncmd.version=...".
// goreleaser injects the release tag here; for `go install` builds it stays
// empty and we fall back to runtime/debug.BuildInfo which embeds the module
// version automatically.
var version = ""

// Run prints a single "adr-lint <version>" line to out. It takes no args.
func Run(args []string, _ string, out io.Writer) error {
	return RunNamed("adr-lint", args, "", out)
}

// RunNamed reports the canonical or compatibility executable name.
func RunNamed(name string, args []string, _ string, out io.Writer) error {
	if len(args) > 0 {
		return fmt.Errorf("unexpected args: usage: %s version", name)
	}
	fmt.Fprintf(out, "%s %s\n", name, buildVersion())
	return nil
}

func buildVersion() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return "(unknown)"
	}
	return info.Main.Version
}
