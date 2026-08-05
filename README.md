<div align="center">

<img src="assets/grove.svg" width="120" alt="grove">

# grove

**Tmux workspaces powered by git worktrees.**

*Pick a workspace, jump in — one command from anywhere.*

</div>

Grove is path-first by default: commands print a workspace path, and the `gv` fish helper turns that path into a `cd`. Pass `--tmux` or `-t` to `grove new` to open the new workspace in a `gv/<repo>/<branch>` tmux session instead.

- **Picker-first** — bare `grove` fuzzy-finds a workspace and prints its path; `gv` drops you in
- **A worktree per branch** — `grove new` adds a git worktree, so branches are directories instead of stashes
- **Warm worktree recycling** — `grove recycle` rotates a finished slot onto a fresh branch without reinstalling its environment
- **tmux-ready** — `grove new -t` creates and switches to a `gv/<repo>/<branch>` session
- **Shared roots** — every worktree lives under one `~/worktrees` tree, overridable per repo
- **Fresh remote bases & hooks** — reset to the remote default, then run custom prepare and setup commands
- **Fish helper** — `gv`, `gv n`, `gv cd`, `gv dd` wrap the path-printing core into real `cd`s
- **Plain & dir workspaces** — not everything needs a worktree; back one with any directory or just `$HOME`
- **Self-cleaning** — `grove done` retires one workspace, `grove reap` safely retires stale merged workspaces, `grove cleanup` sweeps orphaned worktrees
- **Portable repo fleet** — export a curated `sync.yaml`, clone it onto a fresh machine, and safely fast-forward default branches in parallel

---

## Install

Requires Go 1.24+, git, and [fzf](https://github.com/junegunn/fzf). Built for life inside tmux.

```sh
git clone https://github.com/shadowfax92/grove
cd grove
make install
```

`make install` builds `grove`, installs it to `~/bin/grove`, codesigns it on macOS, and installs the fish `gv` helper to `~/.config/fish/functions/gv.fish`.

## Quick Start

```sh
grove init                        # register the current repo in config
grove new mono feat/build-auth    # create a worktree, print its path
grove new -t mono feat/build-auth # create a worktree and open it in tmux
grove new --here --json             # auto-name e.g. feat/07-30-cozy-otter and print JSON
grove cd mono/feat/build-auth     # find an existing workspace, print its path
grove recycle feat/next-task      # reuse this warm worktree for the next branch
grove list --json                  # print state-backed workspace JSON
grove reap --dry-run             # preview stale merged workspaces safe to retire
grove reap --force -j 10         # force-remove managed worktrees, 10 at a time
grove rm --path ~/worktrees/mono/feat-build-auth --yes
grove done mono/feat/build-auth   # finish it and remove the worktree
grove sync export                # select local clones for the portable manifest
grove sync --dry-run             # preview missing repositories to clone
grove pull                       # select repositories whose defaults should advance
```

Use `gv` instead of `grove` whenever you want the shell to `cd` into the path it prints.

## Paths

Grove lays worktrees out under a shared root so a repo's branches sit together:

```text
<worktree_root>/<repo>/<branch-dashed>/
```

`grove new mono feat/build-auth` creates `~/worktrees/mono/feat-build-auth/` — the branch's `/` becomes `-`, while the session name keeps it: `gv/mono/feat/build-auth`.

## The `gv` helper

`gv` is a fish function that runs a grove command, reads the path it prints, and `cd`s you there — the one thing `grove` can't do from a subprocess.

```fish
gv                          # pick a workspace and cd into it
gv n mono feat/build-auth   # new worktree, then cd in
gv n -t mono feat/build-auth # new worktree, then switch to its tmux session
gv n --here                 # auto-name e.g. feat/07-30-cozy-otter, then cd in
gv cd mono/feat/build-auth  # cd into an existing workspace
gv dd                       # finish the current workspace and cd home
gv ls                       # list workspaces
```

Any other subcommand falls straight through to `grove` (`gv rm`, `gv config`, …).

## Commands

### Navigate

```sh
grove                              # pick a workspace and print its path
grove cd [workspace]               # pick or name a workspace, print its path
grove list                         # show workspaces as a session tree (ls, l)
grove list --json                  # print state-backed workspaces as JSON
grove which                        # print the registered repo name for the cwd
grove which ~/code/mono/pkg        # ...or for a given path
```

### Create

```sh
grove init                         # add the current git repo to config
grove init --name mono --default-branch dev
grove new                          # pick a repo, or type a plain workspace name
grove new mono                     # auto-name feat/<MM-DD>-<adjective>-<animal> in mono
grove new -m                       # prompt for the branch name
grove new mono feat/build-auth     # create or check out a specific branch
grove new --tmux mono feat/build-auth # create and open a gv/ tmux session
grove new mono agent --from feat/base   # branch agent off feat/base
grove new --here                   # auto-name e.g. feat/07-30-cozy-otter for the current repo
grove new --here -m                # prompt for the current repo's branch name
grove new --here fix/local-bug     # use an explicit branch for the current repo
grove new --json mono agent        # print worktree_path, branch, repo, repo_path, created_at
```

Inside tmux, `--tmux` switches the current client to the new session. Outside tmux, it leaves the new session detached.

Before creating a worktree, Grove fetches `origin`, force-switches the base checkout to its configured default branch, hard-resets it to `origin/<default>`, and removes untracked files. Ignored files remain. Grove then runs every configured prepare command. Fresh branches start at that same remote default unless `--from` supplies another start point; existing local or remote branches are reused. This discards uncommitted work in the base checkout.

### Recycle

```sh
grove recycle                      # recycle onto an auto-named feat/<MM-DD>-<adjective>-<animal>
grove recycle feat/next-task       # use an explicit new branch
grove recycle mono/feat/old        # recycle a named workspace with an auto-generated branch
grove recycle mono/feat/old feat/next-task
grove recycle --json               # print the same metadata fields as grove new --json
```

Recycling keeps the worktree directory and its warm environment in place. Grove fetches `origin`, discards tracked and untracked changes in the recycled worktree, and creates the new branch at `origin/<default>`. Ignored files remain, the old local branch is preserved, and no merge check is performed. Recycling never reruns prepare or setup hooks and never pushes.

The directory remains the original slot name even though state and a live tmux session are renamed to `g/<repo>/<new-branch>`. For example, recycling `feat/old` to `feat/next-task` can leave the directory at `~/worktrees/mono/feat-old/`; this is intentional.

### Remove

```sh
grove done [workspace]             # finish a workspace, print $HOME (d)
grove rm [workspace...]            # remove workspaces and their worktrees (remove)
grove rm --path <worktree> --yes   # remove one worktree-backed workspace without prompts
grove rm -j 2                      # cap parallel worktree deletion
grove reap --dry-run                # report stale managed workspaces selected/skipped
grove reap --ttl 12h                # override the configured idle threshold
grove cleanup                      # remove orphaned worktrees under grove roots
grove cleanup --all -f             # remove every orphan, no prompts
```

### Configure

```sh
grove config                       # open config in $EDITOR (cfg)
grove config --path                # print the config path
grove sync export                  # append selected local repositories to sync.yaml
grove sync                         # clone repositories missing from disk
grove sync edit                    # open sync.yaml in $EDITOR
grove sync status                  # show present, missing, and dirty repositories
grove pull                         # pick repositories to safely pull
grove pull --all                   # safely pull the full manifest fleet
```

`grove which` exits non-zero for unregistered paths, so it doubles as a guard in scripts.

## Sync repositories

Grove's repository inventory lives at `~/.config/grove/sync.yaml`. It is separate from `config.yaml`: sync never registers workspace repos, and the manifest contains everything another machine needs to reproduce the selected checkout paths.

```yaml
root: ~/code
groups:
  clis:
    - git@github.com:shadowfax92/grove.git
    - url: git@github.com:acme/release-tool.git
      name: release
      branch: trunk
  browseros-project:
    - url: git@github.com:browseros-ai/BrowserOS.git
      name: mono
  ".":
    - https://github.com/acme/directly-under-code.git
```

Each group becomes a directory below `root`; group `.` means directly below the root. A scalar entry uses the repository URL basename without `.git` as its name and the remote default branch. The mapping form can override `name` (including a nested path) and `branch`. Group keys can also contain slashes. The resulting target is always `<root>/<group>/<name>`.

### Curate and copy the manifest

```sh
grove sync export
scp ~/.config/grove/sync.yaml new-machine:~/.config/grove/sync.yaml
ssh new-machine grove sync
```

`grove sync export` scans the manifest root, skips hidden directories, `node_modules`, worktrees/submodules whose `.git` is a file, and prunes descent when it finds a standalone clone. It resolves each `origin` in parallel, warns about repos without one, then opens fzf: Tab selects, Ctrl-A selects all, and Escape cancels cleanly.

The first export writes only what you select. Later exports show only target paths not already present and append selections into their matching first-level group without rewriting comments or hand edits. Delete a manifest line by hand when you no longer want it; there are no ignore lists or tombstones. Use `grove sync edit` to open the file.

### Converge a machine

```sh
grove sync                         # clone missing entries, 4 at a time
grove sync --dry-run               # print the clone plan
grove sync -j 2                    # lower parallelism
grove sync --only 'clis/*'         # filter by group/name
grove sync -f ~/fleet/sync.yaml    # use another manifest
grove sync status                  # local-only presence and dirty tree
```

Sync is deliberately dumb and non-destructive. A missing target is cloned, with `-b` only for an explicit manifest branch. An existing Git checkout is left untouched. Any other existing path is reported as a failure. Sync never moves or deletes paths, rewrites URLs, finds same-origin clones elsewhere, or adds entries to `config.yaml`. It continues independent clones and exits non-zero after summarizing any failures.

### Fast-forward the fleet

```sh
grove pull                         # fzf multi-select over present manifest repos
grove pull --all                   # include the whole manifest in the report
grove pull --all --dry-run
grove pull --only 'browseros-project/*' -j 4
```

The picker shows `group/name`, the current branch, and `!` for a dirty tree. On a clean checkout of the default branch, Grove runs `git pull --ff-only`. On another branch it runs the non-forced `git fetch origin <default>:<default>`, advancing the local default without switching branches or touching the working tree. An explicit manifest `branch` is the default; otherwise Grove resolves `origin/HEAD` through its normal default-branch helper.

Dirty trees, detached HEADs, merge/rebase state, divergence, occupied non-repo paths, and default branches checked out by another worktree are reported per repo and never forced. Missing repos are skipped with a `grove sync` hint under `--all`. The grouped summary reports updated, already-current, skipped, and failed repositories; any failure makes the command exit non-zero.

## Config

Location: `~/.config/grove/config.yaml`. A fresh config starts with a global worktree root:

```yaml
worktree_root: ~/worktrees

reap:
  ttl: 6h

repos:
  - path: ~/code/mono
    name: mono
    default_branch: main
    prepare:
      - git submodule update --init --recursive
    setup:
      - bun install

  - path: ~/code/special
    name: special
    default_branch: main
    worktree_root: ~/scratch/special-worktrees   # per-repo override

  - path: ~/notes
    name: notes
    type: dir
```

### Repo fields

| Field | Description |
|-------|-------------|
| `path` | Base repo path |
| `name` | Config name used by commands and session names |
| `type` | `worktree` (default), `dir` for a directory-backed workspace, or `plain` for a home-rooted one |
| `default_branch` | Branch the base checkout is hard-reset to from `origin` before worktree creation |
| `worktree_root` | Per-repo override for where this repo's worktrees land |
| `workdir` | Subdirectory to print and enter after the worktree is created |
| `prepare` | Commands run in the base repo after its built-in remote reset and before workspace creation |
| `setup` | Commands run in the new workspace after the worktree exists |

### Reap

`grove reap` removes only Grove-managed worktree workspaces that are stale and independently proven safe:

- idle longer than `reap.ttl` (default `6h`, override with `grove reap --ttl 1h`)
- no live tmux session for the stored Grove session name
- current branch still matches Grove state
- clean git status, including no untracked files
- worktree HEAD is already reachable from `origin/<default_branch>` or the local default branch

Anything recent, dirty, unmerged, active, non-worktree, missing, malformed, or otherwise ambiguous is preserved and reported. Use `grove reap --dry-run` for a full selected/skipped report before deletion.

`grove reap --force` (or `-f`) bypasses the age, activity, branch, cleanliness, default-branch, and merge checks for structurally valid managed worktrees. This discards uncommitted changes and unmerged work. Combine it with `--dry-run` to preview the forced target set, or with `--jobs` / `-j` to set deletion parallelism; `-j` alone never relaxes the safety checks. Reaping uses the same bounded removal and state-restore path as `grove rm`, so state is restored for any worktree that fails to remove. Forced reap also removes an exact live tmux session stored for a selected workspace.

### Worktree roots

Where a worktree lands depends on which `worktree_root` is set:

| Setting | Layout |
|---------|--------|
| Top-level `worktree_root` | `<worktree_root>/<repo>/<branch-dashed>/` |
| Repo-level `worktree_root` | `<repo worktree_root>/<branch-dashed>/` — no repo name added |
| Neither | `<repo>/.grove/worktrees/<branch>/` — legacy layout |

`grove init` appends a worktree entry for the current git root: it infers the name from the directory, the default branch from `origin/HEAD` → `main` → `master` → the current branch, and leaves `prepare: []` and `setup: []` for you to fill in.

## Machine-readable automation

`grove new --json` keeps creation behavior unchanged but writes this JSON object to stdout instead of the bare path:

```json
{
  "worktree_path": "/Users/me/worktrees/mono/feat-json",
  "branch": "feat/json",
  "repo": "mono",
  "repo_path": "/Users/me/code/mono",
  "created_at": "2026-07-01T18:06:00Z"
}
```

`grove list --json` writes a JSON array from Grove state. Each object contains `name`, `repo`, `repo_path`, `branch`, `worktree_path`, `session_name`, `created_at`, and `last_used_at`.

`grove rm --path <worktree_path> --yes` is non-interactive: it never opens fzf and never prompts. It exits with:

- `0`: removed, or state existed and the worktree was already gone.
- `3`: no Grove state entry matched the path.
- `4`: Grove found the state entry but failed to remove the worktree; the state entry is restored.

`grove reap --dry-run` is also non-interactive and reports both selected and skipped managed workspaces with reasons. Add `--force` to preview or remove the forced target set. Without `--force`, a non-dry run removes only selected safe workspaces. Partial failures leave failed workspaces in state and return exit code `4`.

## Workspaces

Grove tracks three kinds of workspace:

- **Worktree** — a git worktree per branch, the default. The directory uses the dashed branch (`feat/build-auth` → `feat-build-auth`); the session name keeps the slashes (`gv/mono/feat/build-auth`).
- **Dir** — reuses a configured directory instead of creating a worktree, for notes or non-git folders.
- **Plain** — a standalone workspace rooted at `$HOME`, named `gv/<name>`.

<div align="center">
<img src="assets/grove.png" width="700" alt="grove workspaces in a tmux session picker">
</div>

`grove cleanup` finds git worktrees that exist on disk under grove-owned roots but are no longer in grove's state, and offers to remove them — it understands the global root, per-repo overrides, and the legacy `.grove/worktrees` layout. It is intentionally separate from `grove reap`, which only considers managed state entries and stays safe by default unless `--force` is explicit.

---

> Personal tool built for my own workflow. Feel free to fork and adapt.
