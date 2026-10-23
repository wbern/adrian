// Package cli shares the ADRian and legacy adr-lint entrypoints.
package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wbern/adrian/go/internal/acceptcmd"
	"github.com/wbern/adrian/go/internal/adr"
	"github.com/wbern/adrian/go/internal/auditcmd"
	"github.com/wbern/adrian/go/internal/cache"
	"github.com/wbern/adrian/go/internal/claudeclient"
	"github.com/wbern/adrian/go/internal/cliparser"
	"github.com/wbern/adrian/go/internal/createcmd"
	"github.com/wbern/adrian/go/internal/deprecatecmd"
	"github.com/wbern/adrian/go/internal/dispatcher"
	"github.com/wbern/adrian/go/internal/dotenv"
	"github.com/wbern/adrian/go/internal/finalizecmd"
	"github.com/wbern/adrian/go/internal/gitcontext"
	"github.com/wbern/adrian/go/internal/listcmd"
	"github.com/wbern/adrian/go/internal/plancmd"
	"github.com/wbern/adrian/go/internal/planexec"
	"github.com/wbern/adrian/go/internal/promptbuilder"
	"github.com/wbern/adrian/go/internal/rejectcmd"
	"github.com/wbern/adrian/go/internal/resolvecmd"
	"github.com/wbern/adrian/go/internal/responseparser"
	"github.com/wbern/adrian/go/internal/runner"
	"github.com/wbern/adrian/go/internal/showcmd"
	"github.com/wbern/adrian/go/internal/supersedecmd"
	"github.com/wbern/adrian/go/internal/types"
	"github.com/wbern/adrian/go/internal/validatecmd"
	"github.com/wbern/adrian/go/internal/versioncmd"
	"github.com/wbern/adrian/go/internal/withdrawcmd"
)

var subcommands = map[string]dispatcher.Command{
	"create":    {Run: createcmd.Run, Usage: "adrian create <title>"},
	"show":      {Run: showcmd.Run, Usage: "adrian show <id>"},
	"accept":    {Run: acceptcmd.Run, Usage: "adrian accept <id>"},
	"reject":    {Run: rejectcmd.Run, Usage: "adrian reject <id>"},
	"withdraw":  {Run: withdrawcmd.Run, Usage: "adrian withdraw <id>"},
	"deprecate": {Run: deprecatecmd.Run, Usage: "adrian deprecate <id>"},
	"supersede": {Run: supersedecmd.Run, Usage: "adrian supersede <old-id> <new-id>"},
	"version":   {Run: versioncmd.Run, Usage: "adrian version"},
	"list":      {Run: listcmd.Run, Usage: "adrian list"},
	"validate":  {Run: validatecmd.Run, Usage: "adrian validate"},
	"audit":     {Run: auditcmd.Run, Usage: "adrian audit [--strict] [--format text|json] [--all]"},
	"doctor":    {Run: auditcmd.Run, Usage: "adrian doctor [--strict] [--format text|json] [--all]"},
}

// Run returns an exit code and keeps machine-readable plan output isolated
// from the legacy linter's environment loading and provider construction.
func Run(name string, args []string, out, errOut io.Writer) int {
	if len(args) > 0 && (args[0] == "check" || args[0] == "review") {
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
	if len(args) > 0 && (args[0] == "resolve" || args[0] == "for-files") {
		usage := "adrian resolve [files...] [--base <ref>] [--staged] [--tag <name>] [--ranked] [--top <N>] [--format text|json|pr-block]"
		if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
			fmt.Fprintln(out, strings.ReplaceAll(usage, "adrian", name))
			return 0
		}
		git := gitcontext.NewDefaultClient()
		adrDir := filepath.Join(git.GitRoot(), adr.DirName)
		err := resolvecmd.Run(args[1:], adrDir, out, errOut)
		if err != nil {
			if errors.Is(err, resolvecmd.ErrSyntax) {
				return 2
			}
			fmt.Fprintln(errOut, err)
			return 1
		}
		return 0
	}
	if len(args) > 0 && args[0] == "finalize" {
		usage := "adrian finalize --plan <plan.json> --body-file <body.md> [--segments-covered <covered.json> | --receipts <receipts.json>] [--out-body <file>]"
		if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
			fmt.Fprintln(out, strings.ReplaceAll(usage, "adrian", name))
			return 0
		}
		if err := finalizecmd.Run(args[1:], out, errOut); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		return 0
	}
	git := gitcontext.NewDefaultClient()
	adrDir := filepath.Join(git.GitRoot(), adr.DirName)
	commands := map[string]dispatcher.Command{}
	for key, command := range subcommands {
		command.Usage = strings.ReplaceAll(command.Usage, "adrian", name)
		commands[key] = command
	}
	commands["check"] = dispatcher.Command{Usage: name + " check [--branch <ref>] [--files <path...>] [--diff <path>] [--format text|json|markdown]"}
	commands["review"] = dispatcher.Command{Usage: name + " review [--branch <ref>] [--files <path...>] [--diff <path>] [--format text|json|markdown]"}
	commands["plan"] = dispatcher.Command{Run: plancmd.Run, Usage: plancmd.Usage}
	commands["code-plan"] = dispatcher.Command{Run: plancmd.RunCode, Usage: plancmd.CodeUsage}
	commands["execute-plan"] = dispatcher.Command{Run: planexec.RunCommand, Usage: planexec.Usage}
	commands["resolve"] = dispatcher.Command{Usage: name + " resolve [files...] [--base <ref>] [--staged] [--tag <name>] [--ranked] [--top <N>] [--format text|json|pr-block]"}
	commands["for-files"] = dispatcher.Command{Usage: name + " for-files [files...] [--base <ref>] [--staged] [--tag <name>] [--ranked] [--top <N>] [--format text|json|pr-block]"}
	commands["finalize"] = dispatcher.Command{Usage: name + " finalize --plan <plan.json> --body-file <body.md> [--segments-covered <covered.json> | --receipts <receipts.json>] [--out-body <file>]"}
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
	if opts.AgentCmd != "" {
		lintFns[types.ProviderCustom] = makeCommandLintFn(opts.AgentCmd)
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

func sanitizeEnvVal(s string) string {
	s = strings.ReplaceAll(s, "\x00", "")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

func makeCommandLintFn(cmdStr string) cache.LintFn {
	return func(a adr.ADR, diff string) (types.LintResult, error) {
		if strings.TrimSpace(diff) == "" {
			return types.LintResult{
				ADR:         a,
				Status:      types.StatusSKIPPED,
				Explanation: "No changes to lint",
			}, nil
		}
		prompt := promptbuilder.BuildPrompt(a, diff)
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", cmdStr)
		cmd.Stdin = strings.NewReader(prompt)
		var outBuf, errBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &errBuf

		complexity := string(a.Complexity)
		if complexity == "" {
			complexity = string(adr.ComplexityStandard)
		}
		diffContext := string(a.DiffContextMode)
		if diffContext == "" {
			diffContext = string(adr.DiffContextFunction)
		}

		var baseEnv []string
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "ADRIAN_") {
				baseEnv = append(baseEnv, kv)
			}
		}

		env := append(baseEnv,
			"ADRIAN_ADR_ID="+sanitizeEnvVal(a.ID),
			"ADRIAN_ADR_TITLE="+sanitizeEnvVal(a.Title),
			"ADRIAN_COMPLEXITY="+complexity,
			"ADRIAN_DIFF_CONTEXT="+diffContext,
			"ADRIAN_MATCHED_FILES_COUNT="+strconv.Itoa(len(a.MatchedFiles)),
		)

		if len(a.MatchedFiles) > 0 {
			tmp, err := os.CreateTemp("", "adrian-matched-files-*.txt")
			if err == nil {
				manifestPath := tmp.Name()
				defer func() { _ = os.Remove(manifestPath) }()
				for _, f := range a.MatchedFiles {
					_, _ = tmp.WriteString(f + "\n")
				}
				_ = tmp.Close()
				env = append(env, "ADRIAN_MATCHED_FILES_FILE="+manifestPath)
			}
			joined := strings.Join(a.MatchedFiles, ",")
			if len(joined) <= 2048 && len(a.MatchedFiles) <= 50 {
				env = append(env, "ADRIAN_MATCHED_FILES="+sanitizeEnvVal(joined))
			} else {
				env = append(env, "ADRIAN_MATCHED_FILES=")
			}
		} else {
			env = append(env, "ADRIAN_MATCHED_FILES=", "ADRIAN_MATCHED_FILES_FILE=")
		}

		cmd.Env = env

		err := cmd.Run()
		if err != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return types.LintResult{
					ADR:         a,
					Status:      types.StatusERROR,
					Explanation: "Agent command timeout after 120s",
				}, nil
			}
			msg := strings.TrimSpace(errBuf.String())
			if msg != "" {
				return types.LintResult{
					ADR:         a,
					Status:      types.StatusERROR,
					Explanation: fmt.Sprintf("Agent command error: %s", msg),
				}, nil
			}
			return types.LintResult{
				ADR:         a,
				Status:      types.StatusERROR,
				Explanation: fmt.Sprintf("Agent command error: %v", err),
			}, nil
		}

		res := responseparser.ParseResponse(a, outBuf.String(), nil)
		res.PromptBytes = len(prompt)
		return res, nil
	}
}
