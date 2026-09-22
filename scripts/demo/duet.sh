#!/usr/bin/env bash
# Dual-pane "duet" demo for the README hero gif.
#
# A tmux session is set up with a horizontal split. The LEFT pane is the
# architect who's fed up with onomatopoeic identifiers and writes an ADR
# to ban them. The RIGHT pane is Bob, oblivious, who pushes a feature with
# meow_meow_count and watches adrian catch it.
#
# Usage: duet.sh <workdir>
#
# The workdir must already be an initialized git repo. record_demo.sh
# handles that bootstrap silently before invoking us inside asciinema.
#
# Design notes:
# - Choreography runs in a backgrounded subshell while the foreground
#   `tmux attach` lets asciinema capture live rendering as keys arrive.
#   The driver kills the session at the end, which detaches us cleanly.
# - Each `run_cmd` types char-by-char (visual typing), presses Enter,
#   then sleeps long enough for the command to finish. adrian calls
#   Claude and is highly variable; LINT_BEAT is sized generously.
# - Uses a private tmux socket (`-L adr_duet`) so it never shares state
#   with the user's other tmux sessions.
# - No `-e`: adrian exits non-zero on Bob's violation, which is the
#   point of the scene — we must not abort on it.
set -uo pipefail

WORK="${1:?workdir required}"
SESSION="${ADR_DUET_SESSION:-adr_duet}"

# Use a private socket so we can't possibly target / be targeted by
# unrelated tmux sessions on the user's default socket.
TMX=(tmux -L "$SESSION")

# Typing speed (seconds per char) for the visible command animation.
TYPE_SPEED="${TYPE_SPEED:-0.025}"
# How long to wait after a normal command finishes before the next beat.
BEAT="${BEAT:-1.4}"
# How long to wait after `adrian` — Claude can take a while.
LINT_BEAT="${LINT_BEAT:-20}"
# Slight pause when switching focus between panes.
SWITCH_BEAT="${SWITCH_BEAT:-1.2}"

LEFT_TARGET="$SESSION:0.0"
RIGHT_TARGET="$SESSION:0.1"

LEFT_PS1='\[\033[1;32m\]architect@laptop\[\033[0m\]:\[\033[1;34m\]\W\[\033[0m\]$ '
RIGHT_PS1='\[\033[1;35m\]bob@desktop\[\033[0m\]:\[\033[1;34m\]\W\[\033[0m\]$ '

cleanup() { "${TMX[@]}" kill-server 2>/dev/null || true; }
trap cleanup EXIT

# Belt-and-braces: nuke any previous server on our private socket.
"${TMX[@]}" kill-server 2>/dev/null || true

# ===== build the session =====
"${TMX[@]}" new-session  -d -s "$SESSION" -x 170 -y 30 \
    "env PS1='$LEFT_PS1'  bash --norc --noprofile -i"
"${TMX[@]}" split-window -h -t "$LEFT_TARGET" \
    "env PS1='$RIGHT_PS1' bash --norc --noprofile -i"

"${TMX[@]}" set -t "$SESSION" status off
"${TMX[@]}" set -t "$SESSION" pane-border-style 'fg=brightblack'
"${TMX[@]}" set -t "$SESSION" pane-active-border-style 'fg=brightblack'

"${TMX[@]}" select-pane -t "$LEFT_TARGET"  -P 'bg=default'
"${TMX[@]}" select-pane -t "$RIGHT_TARGET" -P 'bg=#1a1f3a'

# Give the spawned bashes a moment to print their first prompt.
sleep 0.5

# cd both panes silently into the workdir and clear scrollback so the
# first user-visible frame is two pristine prompts.
"${TMX[@]}" send-keys -t "$LEFT_TARGET"  "cd '$WORK' && clear" Enter
"${TMX[@]}" send-keys -t "$RIGHT_TARGET" "cd '$WORK' && clear" Enter
sleep 0.4

# ===== helpers =====

# Type `cmd` char-by-char into the target pane (no Enter).
type_cmd() {
    local target="$1" cmd="$2" i ch
    for ((i=0; i<${#cmd}; i++)); do
        ch="${cmd:i:1}"
        "${TMX[@]}" send-keys -t "$target" -l -- "$ch"
        sleep "$TYPE_SPEED"
    done
}

# Type a command, press Enter, then sleep `beat` to let it finish.
run_cmd() {
    local target="$1" cmd="$2" beat="${3:-$BEAT}"
    type_cmd "$target" "$cmd"
    "${TMX[@]}" send-keys -t "$target" Enter
    sleep "$beat"
}

# Drop a narrator comment (typed as a bash comment, so it doesn't execute).
say() {
    local target="$1" text="$2"
    type_cmd "$target" "# $text"
    "${TMX[@]}" send-keys -t "$target" Enter
    sleep 1.0
}

# ===== choreography =====
# Runs as a backgrounded subshell so the foreground can `tmux attach` and
# let asciinema (or whoever invoked us) capture the live rendering as
# keys are sent. The driver kills the session at the end, which causes
# the foreground attach to exit cleanly.

(
    # Brief intro so the viewer registers the layout before action starts.
    sleep 1.5

    # --- Beat 1: architect ships the ADR ---
    say "$LEFT_TARGET" "third PR with meow_meow_count this sprint. enough."
    sleep 0.4

    run_cmd "$LEFT_TARGET" "adrian create 'Ban animal sounds in identifiers'" 2.5

    # Tighten the ADR off-camera so the lint on the right is fast and
    # surgical. We never `cat` it back — the title carries the meaning.
    # Resolve via glob so we don't couple to adrian's exact slugifier.
    adr_path=("$WORK"/doc/adr/0001-*.md)
    cat > "${adr_path[0]}" <<'EOF'
---
status: proposed
applies_to:
  - "**/*.go"
pre_filter:
  - "meow"
  - "moo"
  - "woof"
  - "oink"
  - "quack"
---

# 1. Ban animal sounds in identifiers

## Decision

Identifiers must not be animal sounds. Use names that describe intent.
EOF

    run_cmd "$LEFT_TARGET" "adrian accept 1" 2.0

    sleep "$SWITCH_BEAT"

    # --- Beat 2: Bob, oblivious ---
    say "$RIGHT_TARGET" "shipping the zoo handlers"

    cat > "$WORK/zoo.go" <<'EOF'
package main

import "fmt"

func meow_meow_count(cats int) int { return cats * 9 }

func moo_moo_handler() { fmt.Println("moo") }
EOF

    run_cmd "$RIGHT_TARGET" "cat zoo.go" 2.0
    run_cmd "$RIGHT_TARGET" "git add zoo.go && adrian" "$LINT_BEAT"

    sleep 0.8
    say "$RIGHT_TARGET" "...oh."
    sleep 2.5

    "${TMX[@]}" kill-session -t "$SESSION" 2>/dev/null || true
) &
DRIVER_PID=$!

"${TMX[@]}" attach -t "$SESSION" 2>/dev/null || true
wait "$DRIVER_PID" 2>/dev/null || true
