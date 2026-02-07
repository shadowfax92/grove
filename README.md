# Grove

A tmux workspace manager built around git worktrees.

You work across multiple repos, each with several worktrees for feature branches and agent tasks, plus standalone tmux sessions for scratch work. Navigating all of this is manual and chaotic. Grove gives you a single popup sidebar to see everything and switch instantly.

```
┌───────────────────────────────────────────────┐
│ tmux session: grove/mono/feat-auth            │
│                                               │
│  ┌─────────┐                                  │
│  │  grove   │                                 │
│  │         │  Window 1: vim                   │
│  │ ▾ mono  │  Window 2: shell                 │
│  │   main  │  Window 3: tests                 │
│  │  ●feat  │                                  │
│  │ ▾ tools │                                  │
│  │   main  │                                  │
│  │ scratch │                                  │
│  └─────────┘                                  │
│                                               │
│  Ctrl+S toggles the sidebar                   │
└───────────────────────────────────────────────┘
```

**What it does:**

- **One-key sidebar** (`Ctrl+S`) to see and switch between all workspaces
- **Worktree lifecycle** — create and remove git worktrees with per-repo setup commands (`bun install`, etc.)
- **Session persistence** — if a tmux session dies, grove recreates it on next start
- **Auto-generated names** — press Enter with an empty name and get a random animal (`mono/beluga`, `workers/pangolin`)
- **Plain workspaces** — standalone sessions for scratch, notes, anything not tied to a repo

---

## Install

Requires Go 1.21+ and tmux 3.3+.

```sh
git clone <repo-url> grove
cd grove
make install    # builds and copies to ~/bin/
```

Make sure `~/bin` is on your `PATH`. Then disable terminal flow control so `Ctrl+S` passes through to tmux:

```sh
# add to .zshrc / .bashrc
stty -ixon
```

Optionally, add the sidebar keybinding to `~/.tmux.conf` so it survives config reloads without needing to re-run `grove start`:

```tmux
bind-key -n C-s display-popup -x 0 -y 0 -w "30%" -h "100%" -E "grove sidebar"
```

## Quick Start

```sh
# 1. Edit config to add your repos
grove config

# 2. Start grove — creates sessions, binds Ctrl+S, attaches to tmux
grove start
```

Once attached, press `Ctrl+S` to open the sidebar.

## Config

Location: `~/.config/grove/config.yaml` (created automatically on first run)

```yaml
prefix: "C-s"

sidebar:
  width: "30%"
  position: "left"    # left | right

repos:
  - path: ~/code/mono
    name: mono
    default_branch: main
    setup:
      - bun install
      - cp .env.example .env

  - path: ~/code/workers
    name: workers
    default_branch: main
    setup:
      - npm install

auto_start:
  - repo: mono
    worktrees: [main]
  - repo: workers
    worktrees: [main]
  - workspace: scratch
    path: ~/
```

**`repos`** — git repositories to manage. Each gets its own group in the sidebar. `setup` commands run in new worktrees after creation.

**`auto_start`** — workspaces created automatically on `grove start`. Repo worktrees and plain workspaces.

## CLI

```sh
grove start                    # start grove, attach to tmux
grove new mono feat-auth       # create worktree + session in mono
grove new --plain notes        # create standalone session
grove rm mono/feat-auth        # kill session + remove worktree
grove list                     # show all workspaces and status
grove config                   # open config in $EDITOR
grove config --path            # print config file path
```

## Sidebar Keybindings

| Key | Action |
|-----|--------|
| `j` / `k` | Move cursor down / up |
| `Enter` | Switch to workspace |
| `c` | Create new workspace |
| `d` | Delete workspace (with confirmation) |
| `R` | Rename workspace |
| `o` | Collapse/expand repo group |
| `/` | Filter workspaces |
| `r` | Refresh |
| `q` / `Esc` / `Ctrl+S` | Close sidebar |

## How It Works

**Workspaces** are tmux sessions managed by grove. Two kinds:

- **Repo workspace** — tied to a git worktree. Created under `<repo>/.grove/worktrees/<name>/`. The tmux session starts in the worktree directory.
- **Plain workspace** — standalone session at any directory. Not tied to a repo.

**Session names** follow the pattern `grove/<repo>/<branch>` for repo workspaces and `grove/<name>` for plain workspaces.

**State** lives at `~/.local/state/grove/state.json`. This is the source of truth for what workspaces exist. It's locked with `flock()` to prevent corruption from concurrent commands.

**On startup**, grove reads config and state, creates any missing tmux sessions, binds the sidebar keybinding, and attaches. If a session was killed externally, grove recreates it — the worktree on disk is unaffected.

## Worktree Layout

```
~/code/mono/                       # main checkout
~/code/mono/.grove/
  └── worktrees/
      ├── feat-auth/               # git worktree
      ├── bugfix-perf/             # git worktree
      └── beluga/                  # auto-named worktree
```

Grove adds `.grove/` to the repo's `.gitignore` automatically.
