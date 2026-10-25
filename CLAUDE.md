# Grove

Grove is a Go CLI that discovers and manages Git worktrees. It is path-first: the binary prints paths or versioned JSON, while shell integration owns `cd`.

## Build and verify

```sh
make build
go test ./...
go vet ./...
```

The module name is `grove`.

## Architecture

- `cmd/` builds the Cobra tree and owns output contracts for `cd`, `new`, `list`, `rm`, `llm`, and `config`.
- `internal/git/` is the Git boundary. It discovers canonical common directories, parses NUL-delimited worktree records, checks status and ancestry, creates `.wt/<branch>`, and removes through Git.
- `internal/config/` parses and appends to `~/.config/grove/config.yaml` without making every configured path a load-time invariant.
- `internal/catalog/` turns valid config rows into physical repositories and alias/setup profiles. Common Git directory is repository identity.
- `internal/inventory/` joins catalog repositories to live Git worktrees and resolves exact selectors.
- `internal/picker/` is the NUL-safe fzf boundary.
- `internal/recency/` stores disposable, hashed navigation markers used only to rank known inventory entries in the picker.
- `internal/panereport/` atomically stores durable worktree-root handles before silently publishing their SHA-256 tokens to the calling Herdr pane. These files are a consumer contract and are independent of recency markers.
- `internal/names/` generates readable default branch names.
- `internal/llmstore/` owns per-repository context stores under `llm.root`: key resolution, the `.llm` and alias symlinks in each worktree, their Git exclusions, and move-only archiving. `cmd` decides when to link and archive; the module never prints.

Git metadata is the only worktree source of truth. Grove has no inventory state file and no tmux or repository-sync lifecycle; optional recency markers under the user state directory affect presentation only.

## Invariants

- New worktrees are `<main-worktree>/.wt/<branch>` and preserve branch slashes.
- `/.wt/` is written to the common `.git/info/exclude`, never tracked `.gitignore`.
- Repository identity comes from the canonical Git common directory, including when invoked inside a linked worktree.
- Multi-profile repositories use the checkout directory name as their canonical name; profile names remain aliases for setup selection.
- Main and locked worktrees are never removable.
- Dirty removal requires the explicit `--discard` flag.
- Age-based cleanup ignores merge state, keeps branches, and preserves the same main, lock, current-worktree, and nesting protections as exact removal.
- Missing cleanup prunes stale Git registrations and keeps branches.
- Missing repository directories are silently skipped on reads. Every successful removal also prunes their config rows under the registration lock; dry runs only preview, and cancellation/failure never changes config.
- Worktree removal never falls back to `os.RemoveAll`.
- Exact selectors never open fzf; omitted selectors require an interactive terminal.
- Navigation recency may reorder only live Git inventory; missing, corrupt, or deleted markers never block navigation or preserve worktrees.
- Path, JSON, and NUL output remain machine-readable. Human presentation may use terminal-aware color. Warnings and setup output use stderr.
- Old external worktrees remain supported during migration and are never moved automatically.
- LLM stores are opt-in through `llm.root`. Store problems warn during `grove new`; they never fail worktree creation.
- `.llm` and each `llm.aliases` name are symlinks at the worktree root, each pointing straight at the store and excluded as `/<name>` (no trailing slash: Git matches symlinks as files) in the common `info/exclude`.
- Grove repoints only symlinks. A real file or directory, or a tracked path, at an anchor name is never replaced; it blocks only that anchor.
- Grove never deletes from a store. Archiving uses a no-clobber rename into `<store>/archive/` and skips entries containing another store's `.grove-llm` marker.
