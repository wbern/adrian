---
status: accepted
date: 2026-05-13
applies_to:
  - ".github/workflows/**/*.yml"
  - "lefthook.yml"
pre_filter:
  - "adrian"
  - "adr-lint"
  - "adr_lint"
---

# 3. Dogfood model-backed ADR checks locally, not in CI

## Context

adr-lint is the project's own architectural linter. Each run shells
out to the Claude Code CLI, which authenticates against the user's
Claude Code subscription. The subscription is **flat-rate** — there's
no per-call dollar charge — but every run does consume that account's
rate-limit quota.

Three enforcement loci to consider:

- **In CI** — every PR triggers `adr-lint`. To work, CI needs an auth
  token for *someone's* Claude Code subscription stashed as a repo
  secret. That account's quota is then burned by every push: feature
  branches, dependabot bumps, docs typo fixes, force-pushes during PR
  iteration. PR latency goes up by however long the Claude calls take.
  Also: external contributors' PRs would consume the maintainer's
  quota, with no rate limiting beyond what the subscription enforces.
- **Locally (manual)** — the maintainer runs `adr-lint` against
  staged changes before committing. Uses their own logged-in Claude
  Code session. Tight feedback loop, no shared secrets, but relies on
  discipline.
- **Locally (lefthook pre-commit hook)** — same as manual but
  automatic. Same auth model. Every commit pays a quota cost, but
  only for the committer's own work.

## Decision

We will run **model-backed ADRian code checks locally only**, never in CI.
The canonical command is `adrian check`; `adr-lint` remains a compatibility
command. Deterministic `plan` and `validate` commands, Go tests, and binary
smoke tests do not invoke Claude and are allowed in CI without model credentials.

Locally it is **on by default** as a lefthook pre-commit step — this
repo dogfoods its own linter, and the maintainer's Claude Code
subscription is the intended quota source. A single commit can be
exempted with `ADR_LINT_SKIP=1 git commit ...` (or the lefthook
built-in `LEFTHOOK_EXCLUDE=adr-lint git commit ...`) for changes that
obviously can't violate an ADR (whitespace, docs typos, comment-only).

**Forbidden:**

```yaml
# Do not add adr-lint as a CI job.
jobs:
  adr-lint:
    steps:
      - env:
          ANTHROPIC_API_KEY: ${{ secrets.MAINTAINER_CLAUDE_TOKEN }}
        run: adr-lint
```

**Acceptable:**

```bash
# Default flow — adr-lint runs as part of git commit via lefthook.
git add <files>
git commit -m "feat: ..."

# One-off skip for trivial changes:
ADR_LINT_SKIP=1 git commit -m "docs: fix typo"
```

If this project ever gains real contributors, supersede this ADR
rather than silently adding a CI workflow — the auth-token-in-secret
question deserves a fresh decision.

## Consequences

**Positive:**
- No shared Claude Code auth token in CI secrets.
- No PR latency from Claude calls.
- Dependabot / docs / chore pushes don't burn quota.
- Maintainer keeps full control of when their subscription is consumed.

**Negative:**
- A violation introduced in a commit can still land on `main` if the
  committer uses `ADR_LINT_SKIP=1` for a change that wasn't actually
  trivial.
- Anyone who clones the repo and runs `lefthook install` must have
  the `adrian` or compatibility `adr-lint` binary and Claude Code CLI available, or every
  commit fails. Acceptable for now since this repo has no external
  contributors — revisit if that changes.

**Neutral:**
- Most commits cost nothing. Two skip mechanisms compose:
  ADRs whose `applies_to` globs don't match the staged diff are
  never loaded; ADRs that do match but whose `pre_filter` substrings
  don't appear in the diff are reported as skipped without an LLM call. A Claude
  Code call only fires for an ADR that matches *and* pre-filters in.

## References

- ADR-0001 (Claude is the only LLM provider) — establishes the backend
  for model-backed checks; planning and lifecycle commands need no model.
