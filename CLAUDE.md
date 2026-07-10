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

**CLI layer** (`cmd/`): Cobra commands — workspace commands plus `sync` (`export`, `edit`, `status`) and `pull`. Each command lives in its own file and registers via `init()` → `rootCmd.AddCommand()` (sync children register on `syncCmd`).

**Internal packages** (`internal/`):
- `config/` — YAML config at `~/.config/grove/config.yaml`. `Load()` validates repo paths; `LoadFast()` skips validation for fast-path command reads.
- `state/` — JSON state at `~/.local/state/grove/state.json`. File-locked via `flock()`. This is the source of truth for what workspaces exist. Atomic writes via rename.
- `tmux/` — Thin wrappers around `tmux` CLI commands (no library).
- `git/` — Git worktree operations. `AddWorktree` tries `-b` first, falls back to existing branch. Worktrees live under `<repo>/.grove/worktrees/<name>/`.
- `names/` — Random animal name generator (~200 names). Checks against existing names for uniqueness.
- `syncfile/` — Independent `~/.config/grove/sync.yaml` inventory, comment-preserving export append, pruned repo scanning, clone planning/execution, and local pull inspection/state transitions. It does not read or update config repos.

**Data flow**: Config defines workspace repos → `grove new` / `grove done` / `grove rm` update state → workspace navigation reads state. Separately, sync.yaml defines clone target paths → `grove sync` fills only missing paths → `grove pull` safely advances default refs → `grove sync status` inspects local presence/dirty state without network.

**Session naming**: `g/<repo>/<branch>` for worktree workspaces, `g/<name>` for plain workspaces. (Changed from `grove/` prefix to `g/` for brevity.)

## Key Patterns

- State manager must be locked (`mgr.Lock()`) before mutating state, unlocked after save.
- `tmux.IsInsideTmux()` checks `$TMUX` env var to decide between `switch-client` (inside tmux) vs `attach-session` (outside).
- Workspace creation: git worktree add → run setup commands → add workspace to state → either print the path (default) or create/switch tmux with `--tmux`.
- Sync identity is `group/name` (the target path), never the origin URL. Duplicate origins at different paths are valid.
- Sync operations are non-destructive: no moves/deletes, `git pull --ff-only` on checked-out defaults, and a non-forced `<default>:<default>` fetch from feature branches. Per-repo failures must not stop the rest of a parallel run.
- Export treats fzf selection as curation and appends YAML without re-marshalling existing hand edits. `.git` files, hidden directories, and `node_modules` are pruned from scans.
