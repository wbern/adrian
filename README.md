<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="./logo-dark.svg">
    <img src="./logo.svg" width="160" alt="ADRian logo">
  </picture>
</p>

<h1 align="center">ADRian</h1>

<p align="center">Turn Architecture Decision Records into validated, executable review policy.</p>

<p align="center">
  <a href="https://github.com/wbern/adrian/actions/workflows/ci.yml"><img src="https://github.com/wbern/adrian/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <br>
  <a href="https://claude.ai/code"><img src="https://img.shields.io/badge/Made%20with-Claude%20Code-blueviolet" alt="Made with Claude Code"></a>
  <a href="https://github.com/wbern/adrian/graphs/contributors"><img src="https://img.shields.io/github/contributors/wbern/adrian" alt="Contributors"></a>
  <a href="https://github.com/wbern/adrian/pulls"><img src="https://img.shields.io/badge/PRs-welcome-brightgreen" alt="PRs Welcome"></a>
</p>

<p align="center"><img src="docs/demo-duet.gif" alt="demo"></p>

ADRian has two complementary modes: `adrian plan` computes review requirements
from committed ADR policy without a model or credentials; `adrian check` uses
Claude Code to evaluate code changes against architectural decisions.

Previously called **adr-lint**. Existing recordings below show that command
name; current demo scripts use `adrian`.

(Two terminals, one ADR. The architect (left) gets fed up with `meow_meow_count`
in code review and ships an ADR banning animal sounds in identifiers. Bob (right),
oblivious, writes `zoo.go` full of `moo_moo_handler` — `adrian` catches it
before the commit lands. Reproduce with
[`./scripts/demo/record_demo.sh duet`](scripts/demo/record_demo.sh) — needs
`asciinema`, `agg`, `tmux`, and the `claude` CLI on PATH.)

## What it solves

ADRs are how teams record architectural decisions, but they drift out of
sync with the code as soon as the ink dries — nothing checks new diffs
against the rules. `adrian` closes that loop: every commit (or PR) is
read against the ADRs in `doc/adr/`, and violations come back with the
file, line, and a suggested fix.

## How it works

For each `adrian check` run:

1. **Collect a diff** — staged files by default, the full branch diff vs
   `main` with `--branch`, or specific paths with `--files`.
2. **Match `applies_to`** — each ADR's glob list decides whether it cares
   about any of the changed paths. Non-matching ADRs are skipped.
3. **Apply `pre_filter`** — a substring shortcut. If none of the ADR's
   pre-filter strings appear anywhere in the diff, the LLM call is skipped
   entirely and the ADR is reported as skipped. This is the difference between a free
   re-run and a paid one.
4. **Ask Claude** — surviving ADRs are sent to the Claude Code CLI with
   the diff. The model returns pass/fail + location + a fix.

The Claude CLI is the code-checking backend — there's no API key plumbing, the
tool shells out to `claude` and inherits whatever auth your account
already has. Deterministic planning never invokes it and does not use these
pre-filter shortcuts.

## Quickstart

```bash
# 1. For model-backed checks, install + log in to the Claude Code CLI
#    (not needed for plan, validate, or ADR lifecycle commands)
#    https://claude.com/claude-code

# 2. Install adrian (pick one)
brew install wbern/tap/adrian
# or:
go install github.com/wbern/adrian/go/cmd/adrian@latest

# 3. Write your first ADR
adrian create "Use the logger package instead of fmt.Println"
# Edit doc/adr/0001-*.md: tighten applies_to globs, add pre_filter
# substrings, write the decision body. See "ADR file format" below
# for what each frontmatter field does.
adrian accept 1

# 4. Try it against a staged change before wiring it as a hook
#    (stage a file whose path matches your ADR's applies_to glob,
#    otherwise the ADR won't load and you'll see nothing)
git add some-file.go
adrian                              # one-shot check, prints violations

# 5. Wire it into git so it runs on every commit
cat > .git/hooks/pre-commit <<'EOF'
#!/usr/bin/env bash
set -e
command -v adrian >/dev/null || exit 0    # no-op for collaborators without it
adrian
EOF
chmod +x .git/hooks/pre-commit
```

### Migrating from adr-lint

`adrian` is the canonical command. The deprecated `adr-lint` binary remains
available with the same implementation and command behavior; it does not emit
a migration message into machine-readable output. There is no removal date yet.

Existing Homebrew users can continue `brew upgrade adr-lint`. To adopt the new
name, run `brew install wbern/tap/adrian`, update scripts, and remove the old
formula when it is no longer needed. Both formulas can coexist: each installs
only its own binary. Releases publish both `adrian_*` and `adr-lint_*` archives.

Go installation uses the new module path:
`go install github.com/wbern/adrian/go/cmd/adrian@latest`. The old Go module
path is not a compatibility interface; a repository redirect cannot replace
Go's module identity. Legacy environment variables such as `ADR_LINT_SKIP`
remain unchanged.

## Deterministic review planning

Start with a repository-owned capability registry:

```yaml
# .adrian/review-capabilities.yml
version: 1
capabilities:
  visual-product-judgment:
    evidence: [rendered-preview]
  test-quality:
    evidence: []
```

Then declare the obligations on the relevant scopes of an accepted ADR:

```yaml
# doc/adr/0014-mobile-first.md (frontmatter)
status: accepted
tags: [ux]
applies_to:
  - paths: ["src/**/*.tsx", "!src/**/*.test.tsx", "!src/**/*.spec.tsx"]
    review:
      requires: [visual-product-judgment]
  - paths: ["src/**/*.test.tsx", "src/**/*.spec.tsx"]
    review:
      requires: [test-quality]
```

This is an illustration of a single ADR covering related implementation and
test files; a repository can instead keep its test obligation in a separate
testing ADR. Descriptive `tags` supply context, not routing. Capabilities name
the required expertise; the consuming workflow maps them to people or agents.

Commit the registry and ADR policy to the trusted base branch before using
them for admission. Resolve the PR base and head from trusted CI or repository
metadata, then run:

```bash
review_base=$(git rev-parse origin/main)
review_head=$(git rev-parse HEAD)
adrian plan --base "$review_base" --head "$review_head" \
  --policy "$review_base" --format json
```

The planner reads policy from Git objects at `--policy`, which must be in the
history of `--base`. It enumerates the complete diff from the merge base to
the supplied head, including both names of a rename and deleted paths. An ADR
change on the PR branch cannot change that PR's trusted review requirements.

For this example:

| Changed files | Semantic requirements |
| --- | --- |
| `src/routes/login.test.tsx` | `test-quality` |
| `src/routes/login.tsx` | `visual-product-judgment`, with `rendered-preview` evidence |
| Both | Both requirements |

The JSON includes `schema`, pinned commits, `changes`, `requirements` with
their ADR/scope/path reasons, `applicable_adrs`, `uncovered_paths`, and a
`fingerprint` identifying the complete plan. Only accepted ADRs create
requirements. Exclusions apply within their own scope; another matching scope
can still require review. `pre_filter` and `enforced_by` never waive obligations.

Malformed metadata, unknown capabilities, missing policy, and Git failures
produce a nonzero exit and no plan. The planner does not dispatch reviewers,
publish GitHub statuses, or supply a baseline code-review rule: those belong to
the consumer. Consumers must reject errors, unsupported schemas, and stale
heads before acting on a plan.

`uncovered_paths` means no accepted ADR matched those paths. It does **not**
prove routing migration is complete: an unannotated ADR can match a path
without creating a requirement. Introduce routing in shadow mode and retain
existing review requirements until the relevant policy coverage is verified.
The plan fingerprint includes provenance commits; it is not a selective cache
key for reusing reviews across unrelated policy changes.

## Picking your first ADRs

The single test that decides whether a decision belongs in an
adrian-enforced ADR (vs. a design doc, RFC, or runtime check) is:

> Could a reviewer who only sees this PR's diff, with no broader
> context, catch a violation of this rule?

If yes, it's a fit. If no, the rule belongs somewhere else — writing
it as an ADR will produce silent false negatives (real violations
slip through, since the diff alone can't reveal them) or noisy false
positives (the linter flags benign code).

### Three ways to surface candidates

1. **Cluster your `fix:` commits.** Each fix is a lesson the project
   already paid for; recurring patterns are exactly the rules worth
   crystallizing.

   ```bash
   git log --grep='^fix' --oneline | head -50
   ```

   Three separate fixes for panics on unchecked map lookups? That's
   an ADR ("require ok-form for map access in `pkg/cache`").

2. **The rules you keep typing in code review.** Any nit you've left
   on three different PRs is a candidate. If someone needed to be
   told, the project needs to write it down.

3. **Hotspot analysis.** Files that are both high-complexity *and*
   high-churn (per Adam Tornhill's *Your Code as a Crime Scene*) are
   where architectural decisions matter most — that's where you've
   been paying the cost of *not* having a rule. Surface them with
   [obscene](https://github.com/wbern/obscene):

   ```bash
   pnpm dlx @wbern/obscene --format table     # no install needed
   # or: pnpm add -g @wbern/obscene
   ```

   `obscene` combines `scc` cyclomatic complexity with git churn to
   rank files that are both complex and actively modified. The top
   of that list is where new ADRs land highest-leverage. Needs `scc`
   on PATH (`brew install scc` on macOS; `choco install scc` or
   `scoop install scc` on Windows; see [scc install docs](https://github.com/boyter/scc#installation)
   for Linux).

### Shapes that play well with adrian

ADRs the linter can mechanically enforce share a shape: a specific,
diff-visible rule in active voice. Good patterns:

- **Forbidden imports / packages.** "Don't import `openai` — use the
  Claude Code CLI."
- **Required wrapper functions.** "Use `logger.Info`, not `fmt.Println`."
- **File-location rules.** "HTTP handlers live in `internal/api`."
- **API-shape rules.** "Controllers must not return database models —
  wrap in DTOs first."
- **Required error-handling idioms.** "Errors from `db.*` calls must
  be wrapped with `errors.Wrap`."

All of these answer the diff-visibility test: a reviewer staring at
the unified diff alone could spot a violation. For a real worked
example in this repo, see
[ADR-0001](doc/adr/0001-claude-is-the-only-llm-provider.md) — the
forbidden-imports pattern applied to LLM providers.

### Shapes that don't

Skip ADRs for rules that require whole-program or runtime context:

- **"Services should be loosely coupled"** — needs system-wide view.
- **"Prefer eventual consistency where possible"** — depends on
  end-to-end data flow.
- **"Minimize blast radius"** — operational, not diff-visible.

These deserve to be documented (in an RFC or design doc), but
adrian can't enforce them.

### Keep runs cheap

Every ADR whose `applies_to` glob matches the staged diff potentially
triggers a Claude Code call. Two levers keep that cost down:

1. **Tight `applies_to` globs.** Don't write `["**/*"]` if the rule
   only governs `go/**/*.go`. ADRs whose globs miss the diff are
   skipped at zero cost.
2. **Meaningful `pre_filter` substrings.** Two or three keywords from
   the rule's vocabulary. If none appear in the diff, the LLM call is
   skipped and the ADR is reported as skipped, not checked or passed.

This repo's own three ADRs are tight by design and good references:

| ADR | `applies_to` | `pre_filter` |
|---|---|---|
| [0001](doc/adr/0001-claude-is-the-only-llm-provider.md) | `go/**/*.go` | `gemini`, `vertex`, `openai` |
| [0002](doc/adr/0002-trunk-based-releases-via-release-please-auto-merge.md) | workflow + goreleaser files | `release-please`, `goreleaser` |
| [0003](doc/adr/0003-dogfood-adr-lint-locally-not-in-ci.md) | workflows + `lefthook.yml` | `adrian`, `adr-lint`, `adr_lint` |

A commit that doesn't touch any of those surfaces costs nothing.

### Going deeper on ADR craft

adrian is opinionated toward *enforceable* rules. Classic ADR
practice is broader — decision archaeology, alternatives auditing,
team communication. For that side of the discipline:

- [Michael Nygard's original 2011 post](https://cognitect.com/blog/2011/11/15/documenting-architecture-decisions)
  — the foundational write-up; start here if ADRs are new to you.
- [MADR](https://adr.github.io/madr/) — the most widely-used ADR
  template; emphasizes considered alternatives.
- [joelparkerhenderson/architecture-decision-record](https://github.com/joelparkerhenderson/architecture-decision-record)
  — curated examples and a dozen template variants.

## ADR file format

ADRs live in `doc/adr/NNNN-slug.md` with YAML frontmatter that controls
how the linter treats them. The full annotated template is at
[`doc/adr/templates/template.md`](doc/adr/templates/template.md) — copy
it into your project to customize the scaffold.

| Field           | Purpose                                                                                  |
| --------------- | ---------------------------------------------------------------------------------------- |
| `status`        | `proposed` / `accepted` / `rejected` / `withdrawn` / `deprecated` / `superseded`         |
| `applies_to`    | Doublestar globs; `!`-prefix negates. Defaults to `["**/*"]`.                            |
| `pre_filter`    | Substrings that must appear in the diff for the LLM to be invoked. Reported as skipped otherwise. |
| `complexity`    | `lite` / `standard` / `complex` — controls chunking and how much context Claude sees.    |
| `enforced_by`   | Marks the ADR as covered by external tooling (eslint rule, type check). LLM skips it.    |
| `diff_context`  | `false` evaluates each file in isolation. Defaults to `true`.                            |
| `superseded_by` | Set automatically by `adrian supersede`; points at the replacement.                    |
| `review`        | Typed `requires` capabilities and optional `evidence` for deterministic planning.       |

The legacy checker accepts ADRs without frontmatter and supplies defaults.
The planner is deliberately stricter: frontmatter, an explicit valid status,
and a nonempty scope for active ADRs are required. It accepts the existing flat
`applies_to` list with a top-level `review`, or typed scope entries with their
own `review`. A top-level `review` cannot be mixed with typed scopes.

## Commands

### Managing ADRs

```bash
adrian create "Use Testify for tests"  # scaffold doc/adr/NNNN-*.md from template
adrian list                            # id, status, title (one per line)
adrian show 1                          # raw file contents
adrian accept 1                        # flip status: accepted
adrian reject 1                        # status: rejected
adrian withdraw 1                      # status: withdrawn
adrian deprecate 1                     # status: deprecated
adrian supersede 1 2                   # 0001 → superseded; writes superseded_by: "0002"
adrian validate                        # cross-refs, IDs, status invariants
adrian version                         # print binary version
adrian help                            # subcommand reference
```

`supersede` writes both halves of the link so `list` can surface the
relationship without you opening the file.

### Running the lint

```bash
adrian                       # check staged files (default; matches the pre-commit hook)
adrian check                 # explicit spelling of the same operation
adrian --branch              # check the full diff vs main (PR-review mode)
adrian --files pkg/foo.go    # check specific paths
adrian --dry-run             # show which ADRs would run; skip LLM calls
adrian --verbose             # print provider, mode, and applicable ADRs
adrian --no-cache            # bypass the result cache
adrian --per-file            # one chunk per file (slower, more precise)
```

## Integration

### Pre-commit hook (adopting adrian in your project)

Drop this into `.git/hooks/pre-commit` in your repo — it runs
`adrian` on staged files and exits cleanly if the binary isn't on
PATH, so collaborators without it aren't blocked:

```bash
cat > .git/hooks/pre-commit <<'EOF'
#!/usr/bin/env bash
set -e
command -v adrian >/dev/null || exit 0
adrian
EOF
chmod +x .git/hooks/pre-commit
```

The same script lives at [`scripts/pre-commit`](scripts/pre-commit) in
this repo for reference.

This is the zero-dependency option for using adrian in **your** repo.
For working on adrian itself, see [CONTRIBUTING.md](CONTRIBUTING.md) —
this repo uses lefthook to orchestrate adrian alongside gofmt,
golangci-lint, and gitleaks.

### CI (PR review)

`adrian plan` is suitable for an ordinary CI runner with Git and the binary;
it needs no model credentials. Treat `--base` and `--policy` as trusted inputs,
and obtain the complete repository history required to resolve the merge base.
The code and binary executing this gate must also come from a trusted source.

`adrian --branch` is designed for CI: it lints the entire diff that
would land in the PR, no staging required. The runner needs the Claude
Code CLI installed and authenticated, which in practice means a
self-hosted runner. The `.github/workflows/ci.yml` in this repo only
runs the Go test suite, linters, packaging validation and both binary smoke
checks — it's not a reference for running
`adrian` itself in CI (see [ADR-0003](doc/adr/0003-dogfood-adr-lint-locally-not-in-ci.md)
for why this repo doesn't dogfood adrian in CI).

## Focused demos

The hero GIF above is the full tour. The per-section ones under
[`docs/`](docs/) are shorter and useful for pointing a colleague at a
single slice:

- [`demo-create.gif`](docs/demo-create.gif) — author your first ADR
- [`demo-lint.gif`](docs/demo-lint.gif) — violation caught, fix, re-run hits the pre-filter shortcut
- [`demo-branch.gif`](docs/demo-branch.gif) — PR review with `--branch`
- [`demo-lifecycle.gif`](docs/demo-lifecycle.gif) — supersede a decision

For a guided discovery flow that also drafts the ADR body, the
`/create-adr` Claude Code slash command remains available alongside the
CLI scaffold.

## Contributing

Working on adrian itself? See [CONTRIBUTING.md](CONTRIBUTING.md) for
local setup (lefthook, golangci-lint, gitleaks), commit message rules,
and how this repo dogfoods its own linter.
