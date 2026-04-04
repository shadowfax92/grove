# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Grove is a minimal Go CLI that provides persistent popup shadow sessions (vim and shell) for any tmux pane. Press a key to get an editor or shell popup that follows your pane's working directory.

## Build & Run

```sh
make build          # builds ./grove binary with version stamped
make install        # builds and copies to ~/bin/grove
```

There are no tests. The module name is `grove` (not a URL-style module path).

## Architecture

**CLI layer** (`cmd/`): Cobra commands — `start`, `config`, `shadow`. Each file registers its command via `init()` → `rootCmd.AddCommand()`.

**Internal packages** (`internal/`):
- `config/` — YAML config at `~/.config/grove/config.yaml`. Shadow popup dimensions and keybindings.
- `tmux/` — Thin wrappers around `tmux` CLI commands (no library).
- `shadow/` — Persistent popup session management. Shadow sessions named `gs/<type>/<pane_id>`.

**Data flow**: Config defines popup keys/dimensions → `grove start` binds tmux keys → pressing key triggers `grove shadow toggle` → shadow session created/toggled in popup.

## Key Patterns

- Shadow sessions follow the pane's cwd. If the cwd changes, the shadow is recreated.
- `grove start` must be run once per tmux server to bind the keys. Can also be added to `.tmux.conf`.
- Shadow cleanup hook runs automatically when panes die.
