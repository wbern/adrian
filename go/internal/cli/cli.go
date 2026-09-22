// Package cli shares the ADRian and legacy adr-lint entrypoints.
package cli

import (
	"fmt"
	"io"
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
	git := gitcontext.NewDefaultClient()
	adrDir := filepath.Join(git.GitRoot(), adr.DirName)
	commands := map[string]dispatcher.Command{}
	for key, command := range subcommands {
		command.Usage = strings.ReplaceAll(command.Usage, "adr-lint", name)
		commands[key] = command
	}
	commands["plan"] = dispatcher.Command{Run: plancmd.Run, Usage: plancmd.Usage}
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
