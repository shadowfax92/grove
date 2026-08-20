<div align="center">

<img src="assets/grove.svg" width="120" alt="grove">

# grove

**Git worktrees that stay with their repository.**

</div>

Grove is a small, path-first wrapper around `git worktree`. It gives humans an fzf picker and gives agents exact selectors, stable JSON, and stdout that is safe to capture.

There is no Grove state database. Git owns the worktree inventory.

## Install

Grove requires Go 1.24+ and Git. [fzf](https://github.com/junegunn/fzf) is required only when you omit a selector and ask Grove to open a picker.

```sh
git clone https://github.com/shadowfax92/grove
cd grove
make install
```

`make install` installs the binary to `~/bin/grove` and the Fish helper to `~/.config/fish/functions/gv.fish`.

## The short version

```sh
grove                         # pick any worktree with fzf; print its path
grove new auth                # create feat/auth in the current repo
grove new fix/login           # preserve an explicit branch name
grove cd feat/auth            # current repo
grove cd browseros:feat/auth  # configured repo alias
grove list                    # Git-backed repository/worktree tree
grove rm .                    # remove the current clean worktree
grove rm                      # pick one or more worktrees with fzf
grove rm feat/auth fix/login  # remove several exact worktrees
grove rm --discard .          # explicitly discard dirty files
grove rm --merged --dry-run   # preview conservative bulk cleanup
grove rm --merged             # remove clean merged worktrees
grove rm --older-than 14d     # remove old clean worktrees, merged or not
grove rm --missing            # prune registrations for deleted directories
```

Use `gv` when you want Fish to change directory:

```fish
gv                    # pick, then cd
gv new auth           # create, then cd to the worktree root
gv cd feat/auth       # resolve, then cd
gv rm .               # remove, then cd to the repository root
gv rm --older-than 14d  # bulk cleanup; stay in the current directory
```

## Layout

New worktrees live inside the repository and preserve the branch hierarchy:

```text
/code/A
├── .git
├── .wt
│   ├── feat/auth
│   ├── fix/login
│   └── chore/deps
└── ...
```

Grove adds `/.wt/` to the shared `.git/info/exclude`. It never changes the tracked `.gitignore`.

Each directory under `.wt` is a normal linked worktree with its own `.git` file. Opening Neovim there, running an agent from that root, and using `git status` all behave normally. The main checkout stays clean because `.wt` is excluded locally.

The worktree returned by `grove new` is always its root. A configured `workdir` changes only where setup commands run; it never changes the path printed for an agent.

Existing worktrees outside `.wt` remain visible and removable. Grove does not move them.

## Commands

### Pick and navigate

`grove` and `grove cd` open the same fzf picker and print one absolute path. An exact selector bypasses fzf.

```sh
grove
grove cd
grove cd .
grove cd feat/auth
grove cd browseros:
grove cd browseros:feat/auth
grove cd /absolute/path/to/a/worktree
```

A process cannot change its parent's working directory, so the binary only prints paths. The `gv` Fish function captures that path and calls `cd` in the shell.

Use `-0` or `--null` when a path may contain newlines. It terminates path output with NUL, and the Fish helper uses it internally.

Selectors are exact:

| Selector | Meaning |
|---|---|
| `feat/auth` | Branch in the repository containing `-C` or the current directory |
| `browseros:feat/auth` | Branch in configured repository/profile `browseros` |
| `browseros:` | That repository's main worktree |
| `.`, `../x`, or an absolute path | The containing known worktree |

Git branch names cannot contain `:`, so `repo:branch` is unambiguous.

### Create

```sh
grove new                    # generated feat/<date>-<words>
grove new auth               # feat/auth
grove new fix/login          # fix/login
grove new agent:auth         # feat/auth using the agent profile
```

`grove new`:

- infers the current Git repository and registers it if needed;
- returns an existing checked-out branch instead of duplicating it;
- reuses an existing local or `origin` branch;
- creates a new branch from the local configured default branch, falling back to its `origin` ref;
- never fetches, pulls, resets, cleans, or switches the main checkout;
- runs setup commands only when it created a worktree.

### List

```sh
grove list
grove list --status
grove --json list
grove --json list --status
```

The default list uses only `git worktree list --porcelain -z`. `--status` opts into the more expensive dirty and ahead/behind checks.

Human output shows each repository path once, then a compact branch tree with creation ages and optional status. Worktree paths are omitted because selectors are enough for navigation. Color is automatic on a terminal; use `--color=always`, `--color=never`, or the `NO_COLOR` environment variable to control it. Paths, `--json`, and `--null` output are never colored.

`--json` emits a versioned document. The same global flag also gives structured output for `cd`, `new`, and `rm`.

### Remove

```sh
grove rm feat/auth
grove rm feat/auth fix/login
grove rm .
grove rm --discard .
grove rm --merged --dry-run
grove rm --merged
grove rm --older-than 14d --dry-run
grove rm --older-than 14d
grove rm --older-than 14d --discard
grove rm --missing --dry-run
grove rm --missing
```

Removal refuses:

- the main worktree;
- locked worktrees;
- dirty worktrees unless `--discard` is present;
- targets that contain another Git repository or worktree, registered or not.

With no selector, `grove rm` opens a multi-select picker. Use Tab or Shift-Tab to select worktrees and Enter to confirm. Grove validates the entire selection before deleting any target. Multiple exact selectors use the same all-target preflight.

`--discard` deliberately has no shorthand. Grove keeps the branch after removing its worktree.

Bulk removal considers every configured repository and always protects main, locked, current, detached, and nested worktrees:

- `--merged` removes clean worktrees whose branch tip is an ancestor of the configured default branch. It is intentionally conservative: squash-merged branches may remain because Git ancestry cannot prove that merge.
- `--older-than 14d` removes worktrees by creation age without considering merge state. Supported units are minutes (`m`), hours (`h`), days (`d`), and weeks (`w`). Dirty worktrees are skipped unless `--discard` is present.
- `--missing` prunes stale Git registrations for worktree directories that no longer exist. It does not delete directories.

Use `--dry-run` with any bulk mode to inspect the exact candidates first. Age cleanup shows the same creation ages as `grove list` and summarizes protected worktrees it skipped. All removal modes keep the underlying branches.

Deletion goes through `git worktree remove`. Grove never falls back to recursive filesystem deletion.

### Configure

```sh
grove config
grove config --path
```

Configuration lives at `~/.config/grove/config.yaml`:

```yaml
repos:
  - path: ~/code/browseros
    name: browseros
    default_branch: main
    setup:
      - bun install

  - path: ~/code/browseros
    name: agent
    default_branch: main
    workdir: packages/browseros-agent
    setup:
      - bun install
      - bun run codegen:agent
```

Rows that resolve to the same Git common directory are one repository with multiple aliases/setup profiles. A multi-profile repository uses its checkout directory name as the canonical display name and selector; every configured profile name remains a valid alias. A root-level row without `workdir` becomes the default setup profile. This keeps existing multi-profile configs working without pretending they are separate repositories.

Missing, deleted, non-directory, and non-Git paths produce warnings on stderr and are skipped; they do not break valid repositories. Legacy `dir` and `plain` entries are ignored.

Legacy top-level `worktree_root`, `reap`, and row-level `prepare` fields may remain during migration but no longer control Grove.

## Agents and scripts

There is no `--agent` mode. The normal CLI composes cleanly:

```sh
path=$(grove --no-input cd browseros:feat/auth)
codex -C "$path"

grove -C "$path" --no-input rm .
grove --json list
```

- `-C, --directory` sets repository context without changing the caller's cwd.
- `--no-input` guarantees that Grove will not open fzf.
- `--json` selects a versioned schema.
- `-0, --null` makes path output NUL-terminated for unusual filesystem names.
- `--color=auto|always|never` controls presentation output without affecting paths or JSON.
- stdout contains the requested path or data.
- warnings and setup progress go to stderr.

## Safety note

Because `.wt` contains nested Git repositories, this command can destroy the entire worktree tree:

```sh
git clean -ffdx
```

The second `-f` deliberately permits deleting nested repositories. See the [`git clean` documentation](https://git-scm.com/docs/git-clean.html).

Missing worktrees are shown as `[missing]` until `grove rm --missing` prunes their stale Git registrations. Grove will not invent a filesystem deletion fallback for them.

Ignored files do not make a worktree dirty. Removing a worktree therefore also removes ignored caches and build output inside it; use `--dry-run` for bulk cleanup when you want to inspect candidates first.
