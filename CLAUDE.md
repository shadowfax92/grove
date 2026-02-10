# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Grove is a Go CLI (`grove`) that manages tmux sessions organized around git worktrees. It provides a popup sidebar TUI for navigating repos, worktrees, and plain workspaces.

## Build & Run

```sh
make build          # builds ./grove binary with version stamped
make install        # builds and copies to ~/bin/grove
```

There are no tests. The module name is `grove` (not a URL-style module path).

## Architecture

**CLI layer** (`cmd/`): Cobra commands — `start`, `new`, `rm`, `list`, `config`, `notify`, `sidebar`. Each file registers its command via `init()` → `rootCmd.AddCommand()`.

**Internal packages** (`internal/`):
- `config/` — YAML config at `~/.config/grove/config.yaml`. `Load()` validates repo paths; `LoadFast()` skips validation (used by sidebar for speed).
- `state/` — JSON state at `~/.local/state/grove/state.json`. File-locked via `flock()`. This is the source of truth for what workspaces exist. Atomic writes via rename.
- `tmux/` — Thin wrappers around `tmux` CLI commands (no library).
- `git/` — Git worktree operations. `AddWorktree` tries `-b` first, falls back to existing branch. Worktrees live under `<repo>/.grove/worktrees/<name>/`.
- `names/` — Random animal name generator (~200 names). Checks against existing names for uniqueness.
- `tui/` — Bubble Tea sidebar. `sidebar.go` is the main model with browse/create/delete/filter/rename modes. `tree.go` builds the node list and handles visibility/filtering. `create.go` is the inline create form. `styles.go` defines lipgloss styles.

**Data flow**: Config defines repos → `grove start` creates default-branch workspaces in state → state drives tmux session creation → sidebar reads state for display, writes state for mutations.

**Session naming**: `g/<repo>/<branch>` for worktree workspaces, `g/<name>` for plain workspaces. (Changed from `grove/` prefix to `g/` for brevity.)

**Two creation paths**: CLI (`cmd/new.go`) and TUI (`internal/tui/create.go`) both create worktrees and sessions but are independent implementations — changes to creation logic must be applied to both.

## Key Patterns

- State manager must be locked (`mgr.Lock()`) before mutating state, unlocked after save. The sidebar reads state without locking for speed.
- `tmux.IsInsideTmux()` checks `$TMUX` env var to decide between `switch-client` (inside tmux) vs `attach-session` (outside).
- Workspace creation: git worktree add → run setup commands → create tmux session → add to state → switch client.
- The sidebar TUI runs inside a tmux `display-popup` — it's not a standalone app.
