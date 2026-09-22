#!/usr/bin/env bash
# Drives the README demo: pretends to be a developer adopting adrian in a
# fresh repo. Run inside an asciinema recording — every command is "typed"
# char-by-char so the cast looks like a real session.
#
# Usage: scenario.sh <section>
#   create    - create + accept + list + validate
#   lint      - stage a violating file, run --dry-run, then real adrian
#   lifecycle - supersede, list final state
#   all       - run every section back-to-back (the README hero gif)
#
# The recorder is responsible for bootstrapping the workspace (mkdir +
# git init) silently before invoking this script — repo plumbing is not
# part of what the viewer needs to see.
#
# Note: -e is intentionally NOT set. `adrian` exits non-zero when it
# finds a violation, which is the point of the lint section — we don't
# want that to abort subsequent sections in the `all` walkthrough.
set -uo pipefail

SECTION="${1:-all}"

# Typing speed (seconds per char). Lower = faster.
TYPE_SPEED="${TYPE_SPEED:-0.025}"
# Pause after each command's output before the next prompt.
BEAT="${BEAT:-1.0}"

GREEN=$'\033[1;32m'
DIM=$'\033[2m'
BOLD=$'\033[1m'
CYAN=$'\033[1;36m'
RESET=$'\033[0m'

prompt() { printf '%s$%s ' "$GREEN" "$RESET"; }

# Type a command char-by-char and then run it through eval. The command
# should be a single shell line — heredocs are awkward because the typing
# animation spans newlines. Use write_file() for multi-line content.
do_cmd() {
    local cmd="$1"
    prompt
    local i ch
    for ((i=0; i<${#cmd}; i++)); do
        ch="${cmd:i:1}"
        printf '%s' "$ch"
        sleep "$TYPE_SPEED"
    done
    printf '\n'
    eval "$cmd"
    sleep "$BEAT"
}

# Inline narrator comment — appears as a # comment on its own line.
say() {
    printf '%s# %s%s\n' "$DIM" "$1" "$RESET"
    sleep 0.6
}

# Section header.
banner() {
    printf '\n%s━━ %s ━━%s\n\n' "$CYAN" "$1" "$RESET"
    sleep 0.6
}

# Write content silently, then show it via `cat`. Looks like the dev opened
# their editor and dropped a file in without burning real recording time on
# a multi-line typing animation.
write_file() {
    local path="$1"
    local content="$2"
    printf '%s' "$content" > "$path"
    printf '%s# wrote %s%s\n' "$DIM" "$path" "$RESET"
    sleep 0.4
    do_cmd "cat $path"
}

section_create() {
    banner "Author your first ADR"
    say "Capture the decision: no fmt.Println for logging."
    do_cmd "adrian create 'Use the logger package instead of fmt.Println'"
    say "The scaffold gives us minimal frontmatter — applies to everything by default."
    do_cmd "cat doc/adr/0001-use-the-logger-package-instead-of-fmt-println.md"
    say "Tighten frontmatter: applies_to + pre_filter so the lint is cheap."
    write_file doc/adr/0001-use-the-logger-package-instead-of-fmt-println.md '---
status: proposed
applies_to:
  - "**/*.go"
pre_filter:
  - "fmt.Println"
---

# 1. Use the logger package instead of fmt.Println

## Decision

All runtime logging goes through the project logger. New code must not
use `fmt.Println` to surface diagnostics — it bypasses log levels and
structured fields.
'
    do_cmd "adrian accept 1"
    do_cmd "adrian list"
    do_cmd "adrian validate"
}

section_lint() {
    banner "The lint pipeline catches a violation"
    say "Add a handler that prints debug output the wrong way."
    write_file handler.go 'package main

import "fmt"

func handle() {
    fmt.Println("received request")
}
'
    do_cmd "git add handler.go"
    say "Preview: which ADRs apply? (--dry-run skips the LLM call)"
    do_cmd "adrian --dry-run"
    say "Now run for real. --verbose shows what's being sent to Claude."
    do_cmd "adrian --verbose"
    say "Fix the violation: switch to the project logger."
    write_file handler.go 'package main

import "myapp/logger"

func handle() {
    logger.Info("received request")
}
'
    do_cmd "git add handler.go"
    say "Re-run: pre_filter no longer matches the diff → instant PASS, no LLM call."
    do_cmd "adrian"
}

section_branch() {
    banner "PR review mode: --branch checks against main"
    say "We're on a feature branch with one commit ahead of main."
    do_cmd "git --no-pager log --oneline main..HEAD"
    say "--branch lints the entire diff that would land in the PR (no staging required)."
    do_cmd "adrian --branch"
}

section_lifecycle() {
    banner "Lifecycle: supersede a decision"
    say "We've changed our mind — slog is the new standard."
    do_cmd "adrian create 'Use log/slog for structured logging'"
    do_cmd "adrian accept 2"
    do_cmd "adrian supersede 1 2"
    do_cmd "adrian list"
    say "supersede wrote both halves of the link — proof in ADR 0001's frontmatter:"
    do_cmd "adrian show 1 | head -8"
}

case "$SECTION" in
    create)    section_create ;;
    lint)      section_lint ;;
    branch)    section_branch ;;
    lifecycle) section_lifecycle ;;
    all)
        section_create
        section_lint
        section_lifecycle
        ;;
    *) echo "unknown section: $SECTION" >&2; exit 2 ;;
esac

# Final beat so the GIF doesn't end mid-prompt.
printf '\n%sDone.%s\n' "$BOLD" "$RESET"
sleep 1.5
