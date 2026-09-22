# Contributing to ADRian

## Local setup

Install the local checks on top of the Go toolchain:

```bash
brew install lefthook gitleaks golangci-lint

# Wire the git hooks into this clone (idempotent)
lefthook install
```

That's it. The hooks live in [`lefthook.yml`](lefthook.yml) and run on commit
and push.

## Commit messages

This repo uses [Conventional Commits](https://www.conventionalcommits.org/).
The `commit-msg` hook rejects anything else; CI re-validates PR titles since
squash-merges adopt the PR title as the commit message.

Format:

```
<type>(<optional scope>)<optional !>: <subject>
```

Allowed types:

| Type       | Use for                                                  | Triggers release? |
| ---------- | -------------------------------------------------------- | ----------------- |
| `feat`     | New feature                                              | minor             |
| `fix`      | Bug fix                                                  | patch             |
| `perf`     | Performance improvement                                  | patch             |
| `refactor` | Internal change, no behavior change                      | no                |
| `docs`     | Documentation only                                       | no                |
| `test`     | Test-only changes                                        | no                |
| `build`    | Build system, dependencies                               | no                |
| `ci`       | CI configuration                                         | no                |
| `chore`    | Tooling, repo housekeeping                               | no                |
| `style`    | Whitespace, formatting (no logic change)                 | no                |
| `revert`   | Revert a previous commit                                 | varies            |

Breaking changes: append `!` after the type/scope or add a `BREAKING CHANGE:`
footer. Either bumps the major version.

Examples:

```
feat(create): add --template flag
fix(runner): handle empty diff without panicking
feat(api)!: rename --files to --paths
docs: clarify pre_filter semantics in README
chore(deps): bump golangci-lint to v1.62
```

## Releases

Driven by [release-please](https://github.com/googleapis/release-please) +
[goreleaser](https://goreleaser.com/). Trunk-based — no manual tagging,
no review gate.

1. Conventional Commits land on `main`.
2. release-please opens a "Release PR" with the proposed next version and
   an auto-generated changelog.
3. The `auto-merge` job in `release-please.yml` squash-merges that PR
   immediately. release-please then tags `vX.Y.Z` and creates the GitHub
   Release.
4. The tag push triggers `release.yml`, which runs goreleaser to build
   cross-platform binaries and update the Homebrew tap.

See [ADR-0002](doc/adr/0002-trunk-based-releases-via-release-please-auto-merge.md)
for the design rationale.

## Tests

```bash
cd go && go test ./...
```

The `pre-push` hook runs the full suite before letting you push.

## Dogfooding ADRian

This repo is its own first user. The check is **off in CI by design**
(see [ADR-0003](doc/adr/0003-dogfood-adr-lint-locally-not-in-ci.md))
but **on by default locally** as a lefthook pre-commit step.

### Default flow

```bash
git add <files>
git commit -m "feat: ..."         # lefthook runs adrian + gofmt/golangci-lint/gitleaks
```

`adrian` operates on the staged diff. It picks up only the ADRs
whose `applies_to` globs match the staged files, then for each one
either short-circuits via `pre_filter` (zero cost) or calls Claude to
evaluate. Output is file:line for each violation, with a concrete fix.

If a check fails: edit the file, `git add` again, then `git commit`
again — the hook re-runs against the new staged diff.

### Skipping the ADR check

Each `adrian` run makes a Claude call per applicable ADR (minus
pre-filter hits). For changes that obviously can't violate an
architectural constraint — pure formatting/whitespace, README/docs
typos, comment-only edits — skip the ADR check for a single commit:

```bash
ADR_LINT_SKIP=1 git commit -m "..."
# or, equivalently, the lefthook built-in:
LEFTHOOK_EXCLUDE=adr-lint git commit -m "..."
```

Both leave the other pre-commit hooks (gofmt, golangci, gitleaks)
running — they're free and always worth it. Use judgement: the ADRs
themselves are short, and if you've read them recently and the change
clearly doesn't touch the surface they govern, just commit.

The lefthook step keeps its historical `adr-lint` name so existing skip
commands continue to work. It prefers `adrian check` and falls back to an
installed `adr-lint` binary.

## Packaging and compatibility

`adrian` and `adr-lint` share the Go CLI implementation. Keep both entrypoints
working while the old name is deprecated; do not add notices to JSON stdout.
The Go module is `github.com/wbern/adrian/go` and old module-path installation
is not supported by the compatibility binary.

GoReleaser publishes separate archives and Homebrew formulas for the two
binary names. Each formula installs one binary, so existing `adr-lint` users
can upgrade and both formulas can coexist. Keep the legacy archive naming
stable for scripted downloads.

```bash
goreleaser check
goreleaser release --snapshot --skip=before
```

The formula publisher (`brews`) is deprecated upstream, so current GoReleaser
`check` reports an otherwise-valid config with exit code 2. We retain it for
the existing macOS/Linux formula upgrade path. The snapshot command exercises
both archive families and generates formulas without publishing anything;
`--skip=before` avoids modifying dependency files during validation. Use a
fresh checkout or preserve an existing `dist/` before a second snapshot run.

CI builds and smoke-tests both commands and runs the release snapshot.
Model-backed code checking remains local; deterministic planner tests and
packaging checks need no Claude authentication and are allowed in CI.

### Other modes

```bash
adrian --verbose                # show every applicable ADR (passed + failed),
                                  # with pre-filter reasons
adrian --branch                 # lint everything on the current branch that
                                  # has diverged from main (merge-base..HEAD)
                                  # — useful before pushing a long-running branch
adrian --branch feat/other      # same, but for an explicit ref instead of HEAD
adrian --files path/to/file.go  # lint a specific file even if not staged
```
