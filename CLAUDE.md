# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Grove is a Go CLI (`grove`) that manages tmux sessions organized around git worktrees. It is picker-first: `grove` and `grove cd` print workspace paths via fzf, `grove new` creates a workspace and prints its path by default, and `grove new --tmux` creates a tmux session explicitly.

## Build & Run

```sh
make build          # builds ./grove binary with version stamped
make install        # builds and copies to ~/bin/grove
```

Use `go test ./...` for verification. The module name is `grove` (not a URL-style module path).

## Architecture

**CLI layer** (`cmd/`): Cobra commands — `start`, `new`, `cd`, `rm`, `list`, `switch`, `done`, `config`, `notify`, `shadow`. Each file registers its command via `init()` → `rootCmd.AddCommand()`. Layouts are managed by the separate `layouts` CLI — grove shells out to `layouts new <session> <layout> -d <dir>` when creating sessions with layouts.

**Internal packages** (`internal/`):
- `config/` — YAML config at `~/.config/grove/config.yaml`. `Load()` validates repo paths; `LoadFast()` skips validation for fast-path command reads.
- `state/` — JSON state at `~/.local/state/grove/state.json`. File-locked via `flock()`. This is the source of truth for what workspaces exist. Atomic writes via rename.
- `tmux/` — Thin wrappers around `tmux` CLI commands (no library).
- `git/` — Git worktree operations. `AddWorktree` tries `-b` first, falls back to existing branch. Worktrees live under `<repo>/.grove/worktrees/<name>/`.
- `names/` — Random animal name generator (~200 names). Checks against existing names for uniqueness.

**Data flow**: Config defines repos → `grove new` / `grove done` / `grove rm` update state → `grove start` reconciles tmux sessions from state → `grove cd` / `grove switch` / `grove list` read state for interaction.

**Session naming**: `g/<repo>/<branch>` for worktree workspaces, `g/<name>` for plain workspaces. (Changed from `grove/` prefix to `g/` for brevity.)

## Key Patterns

- State manager must be locked (`mgr.Lock()`) before mutating state, unlocked after save.
- `tmux.IsInsideTmux()` checks `$TMUX` env var to decide between `switch-client` (inside tmux) vs `attach-session` (outside).
- Workspace creation: git worktree add → run setup commands → add workspace to state → either print the path (default) or create/switch tmux with `--tmux`.
