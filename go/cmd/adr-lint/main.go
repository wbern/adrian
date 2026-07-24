// Command adr-lint is the CLI entrypoint. It wires real os/exec-backed
// clients into runner.Run.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/wbern/adr-lint/go/internal/acceptcmd"
	"github.com/wbern/adr-lint/go/internal/adr"
	"github.com/wbern/adr-lint/go/internal/cache"
	"github.com/wbern/adr-lint/go/internal/claudeclient"
	"github.com/wbern/adr-lint/go/internal/cliparser"
	"github.com/wbern/adr-lint/go/internal/createcmd"
	"github.com/wbern/adr-lint/go/internal/deprecatecmd"
	"github.com/wbern/adr-lint/go/internal/dispatcher"
	"github.com/wbern/adr-lint/go/internal/dotenv"
	"github.com/wbern/adr-lint/go/internal/gitcontext"
	"github.com/wbern/adr-lint/go/internal/listcmd"
	"github.com/wbern/adr-lint/go/internal/planexec"
	"github.com/wbern/adr-lint/go/internal/rejectcmd"
	"github.com/wbern/adr-lint/go/internal/runner"
	"github.com/wbern/adr-lint/go/internal/showcmd"
	"github.com/wbern/adr-lint/go/internal/supersedecmd"
	"github.com/wbern/adr-lint/go/internal/types"
	"github.com/wbern/adr-lint/go/internal/validatecmd"
	"github.com/wbern/adr-lint/go/internal/versioncmd"
	"github.com/wbern/adr-lint/go/internal/withdrawcmd"
)

var subcommands = map[string]dispatcher.Command{
	"create":       {Run: createcmd.Run, Usage: "adr-lint create <title>"},
	"show":         {Run: showcmd.Run, Usage: "adr-lint show <id>"},
	"accept":       {Run: acceptcmd.Run, Usage: "adr-lint accept <id>"},
	"reject":       {Run: rejectcmd.Run, Usage: "adr-lint reject <id>"},
	"withdraw":     {Run: withdrawcmd.Run, Usage: "adr-lint withdraw <id>"},
	"deprecate":    {Run: deprecatecmd.Run, Usage: "adr-lint deprecate <id>"},
	"supersede":    {Run: supersedecmd.Run, Usage: "adr-lint supersede <old-id> <new-id>"},
	"version":      {Run: versioncmd.Run, Usage: "adr-lint version"},
	"list":         {Run: listcmd.Run, Usage: "adr-lint list"},
	"validate":     {Run: validatecmd.Run, Usage: "adr-lint validate"},
	"execute-plan": {Run: planexec.RunCommand, Usage: "adr-lint execute-plan --plan <plan.json> --adapter <executable>"},
}

func main() {
	git := gitcontext.NewDefaultClient()
	adrDir := filepath.Join(git.GitRoot(), adr.DirName)

	handled, err := dispatcher.Dispatch(os.Args[1:], adrDir, os.Stdout, subcommands)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if handled {
		return
	}

	opts, err := cliparser.ParseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if err := dotenv.Load(filepath.Join(git.GitRoot(), ".env.local")); err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not load .env.local:", err)
	}
	claude := claudeclient.NewDefaultClient()

	lintFns := map[types.Provider]cache.LintFn{
		types.ProviderClaude: claude.Lint,
	}

	code, err := runner.Run(opts, runner.RunDeps{
		Out:     os.Stdout,
		Err:     os.Stderr,
		Git:     git,
		LintFns: lintFns,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "ADR Lint failed:", err)
		os.Exit(1)
	}
	os.Exit(code)
}
