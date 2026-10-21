// Package cli shares the ADRian and legacy adr-lint entrypoints.
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/wbern/adrian/go/internal/acceptcmd"
	"github.com/wbern/adrian/go/internal/adr"
	"github.com/wbern/adrian/go/internal/cache"
	"github.com/wbern/adrian/go/internal/claudeclient"
	"github.com/wbern/adrian/go/internal/cliparser"
	"github.com/wbern/adrian/go/internal/createcmd"
	"github.com/wbern/adrian/go/internal/deprecatecmd"
	"github.com/wbern/adrian/go/internal/dispatcher"
	"github.com/wbern/adrian/go/internal/dotenv"
	"github.com/wbern/adrian/go/internal/gitcontext"
	"github.com/wbern/adrian/go/internal/listcmd"
	"github.com/wbern/adrian/go/internal/plancmd"
	"github.com/wbern/adrian/go/internal/planexec"
	"github.com/wbern/adrian/go/internal/rejectcmd"
	"github.com/wbern/adrian/go/internal/runner"
	"github.com/wbern/adrian/go/internal/showcmd"
	"github.com/wbern/adrian/go/internal/supersedecmd"
	"github.com/wbern/adrian/go/internal/types"
	"github.com/wbern/adrian/go/internal/validatecmd"
	"github.com/wbern/adrian/go/internal/versioncmd"
	"github.com/wbern/adrian/go/internal/withdrawcmd"
)

var subcommands = map[string]dispatcher.Command{
	"create":    {Run: createcmd.Run, Usage: "adr-lint create <title>"},
	"show":      {Run: showcmd.Run, Usage: "adr-lint show <id>"},
	"accept":    {Run: acceptcmd.Run, Usage: "adr-lint accept <id>"},
	"reject":    {Run: rejectcmd.Run, Usage: "adr-lint reject <id>"},
	"withdraw":  {Run: withdrawcmd.Run, Usage: "adr-lint withdraw <id>"},
	"deprecate": {Run: deprecatecmd.Run, Usage: "adr-lint deprecate <id>"},
	"supersede": {Run: supersedecmd.Run, Usage: "adr-lint supersede <old-id> <new-id>"},
	"version":   {Run: versioncmd.Run, Usage: "adr-lint version"},
	"list":      {Run: listcmd.Run, Usage: "adr-lint list"},
	"validate":  {Run: validatecmd.Run, Usage: "adr-lint validate"},
}

// Run returns an exit code and keeps machine-readable plan output isolated
// from the legacy linter's environment loading and provider construction.
func Run(name string, args []string, out, errOut io.Writer) int {
	if len(args) > 0 && args[0] == "check" {
		args = args[1:]
	}
	// Plan commands resolve Git only through the sanitized immutable reader.
	// Do not obtain their repository root through legacy environment-sensitive
	// discovery or load the checkout's dotenv/provider configuration.
	if len(args) > 0 && (args[0] == "plan" || args[0] == "code-plan") {
		usage := plancmd.Usage
		if args[0] == "code-plan" {
			usage = plancmd.CodeUsage
		}
		if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
			fmt.Fprintln(out, strings.ReplaceAll(usage, "adrian", name))
			return 0
		}
		dir, err := os.Getwd()
		if err == nil {
			if args[0] == "code-plan" {
				err = plancmd.RunCodeCLI(args[1:], dir, out)
			} else {
				err = plancmd.RunCLI(args[1:], dir, out)
			}
		}
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		return 0
	}
	if len(args) > 0 && args[0] == "execute-plan" {
		if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
			fmt.Fprintln(out, strings.ReplaceAll(planexec.Usage, "adrian", name))
			return 0
		}
		if err := planexec.RunCommand(args[1:], "", out); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		return 0
	}
	git := gitcontext.NewDefaultClient()
	adrDir := filepath.Join(git.GitRoot(), adr.DirName)
	commands := map[string]dispatcher.Command{}
	for key, command := range subcommands {
		command.Usage = strings.ReplaceAll(command.Usage, "adr-lint", name)
		commands[key] = command
	}
	commands["plan"] = dispatcher.Command{Run: plancmd.Run, Usage: plancmd.Usage}
	commands["code-plan"] = dispatcher.Command{Run: plancmd.RunCode, Usage: plancmd.CodeUsage}
	commands["execute-plan"] = dispatcher.Command{Run: planexec.RunCommand, Usage: planexec.Usage}
	commands["version"] = dispatcher.Command{Run: func(args []string, dir string, w io.Writer) error { return versioncmd.RunNamed(name, args, dir, w) }, Usage: name + " version"}

	handled, err := dispatcher.DispatchNamed(name, args, adrDir, out, commands)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if handled {
		return 0
	}

	opts, err := cliparser.ParseArgs(args)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}

	if err := dotenv.Load(filepath.Join(git.GitRoot(), ".env.local")); err != nil {
		fmt.Fprintln(errOut, "warning: could not load .env.local:", err)
	}
	claude := claudeclient.NewDefaultClient()

	lintFns := map[types.Provider]cache.LintFn{
		types.ProviderClaude: claude.Lint,
	}

	code, err := runner.Run(opts, runner.RunDeps{
		Out:     out,
		Err:     errOut,
		Git:     git,
		LintFns: lintFns,
	})
	if err != nil {
		fmt.Fprintln(errOut, "ADR Lint failed:", err)
		return 1
	}
	return code
}
