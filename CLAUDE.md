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

- `cmd/` builds the Cobra tree and owns output contracts for `cd`, `new`, `list`, `rm`, and `config`.
- `internal/git/` is the Git boundary. It discovers canonical common directories, parses NUL-delimited worktree records, checks status and ancestry, creates `.wt/<branch>`, and removes through Git.
- `internal/config/` parses and appends to `~/.config/grove/config.yaml` without making every configured path a load-time invariant.
- `internal/catalog/` turns valid config rows into physical repositories and alias/setup profiles. Common Git directory is repository identity.
- `internal/inventory/` joins catalog repositories to live Git worktrees and resolves exact selectors.
- `internal/picker/` is the NUL-safe fzf boundary.
- `internal/names/` generates readable default branch names.

Git metadata is the only worktree source of truth. Grove has no state file and no tmux or repository-sync lifecycle.

## Invariants

- New worktrees are `<main-worktree>/.wt/<branch>` and preserve branch slashes.
- `/.wt/` is written to the common `.git/info/exclude`, never tracked `.gitignore`.
- Repository identity comes from the canonical Git common directory, including when invoked inside a linked worktree.
- Main and locked worktrees are never removable.
- Dirty removal requires the explicit `--discard` flag.
- Worktree removal never falls back to `os.RemoveAll`.
- Exact selectors never open fzf; omitted selectors require an interactive terminal.
- Normal stdout remains machine-readable. Warnings and setup output use stderr.
- Old external worktrees remain supported during migration and are never moved automatically.
