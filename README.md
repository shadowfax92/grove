# Grove

Tmux workspaces powered by git worktrees.

Grove keeps workspace state, creates git worktrees, and prints paths so shell wrappers can move you there. The core CLI is path-first: commands print the destination path, and the `gv` fish helper turns those paths into `cd`.

## Install

Requires Go 1.21+, git, tmux, and [fzf](https://github.com/junegunn/fzf).

```sh
git clone <repo-url> grove
cd grove
make install
```

`make install` builds `grove`, installs it to `~/bin/grove`, signs it on macOS, and installs the fish `gv` helper to `~/.config/fish/functions/gv.fish`.

Useful fish flows:

```fish
gv                          # pick an existing workspace and cd into it
gv n mono feat/build-auth    # create a worktree and cd into it
gv n --here fix/local-bug    # worktree the current git repo and cd into it
gv cd mono/feat/build-auth   # cd into an existing workspace
gv dd                        # finish the cwd-backed workspace and cd home
gv ls                        # list workspaces
```

## Quick Start

```sh
# 1. Add the current repo to config
grove init

# 2. Create worktrees
grove new mono feat/build-auth          # prints ~/worktrees/mono/feat-build-auth
grove new mono agent --from feat/base   # create agent from feat/base
grove new --here chore/readme           # add cwd repo if needed, then create worktree

# 3. Find and remove workspaces
grove cd mono/feat/build-auth
grove done mono/feat/build-auth
grove cleanup
```

Use `gv` instead of `grove` when you want the shell to `cd` into the printed path.

## Config

Location: `~/.config/grove/config.yaml`

New config files include a global worktree root:

```yaml
worktree_root: ~/worktrees

repos:
  - path: ~/code/mono
    name: mono
    default_branch: main
    prepare:
      - git diff --quiet && git diff --cached --quiet || (echo "uncommitted changes in base repo" && exit 1)
      - git checkout main
      - git pull
    setup:
      - bun install

  - path: ~/code/special
    name: special
    default_branch: main
    worktree_root: ~/scratch/special-worktrees

  - path: ~/notes
    name: notes
    type: dir
```

`worktree_root` at the top level is the shared base for normal repo worktrees. Grove creates worktrees at:

```text
<worktree_root>/<repo-name>/<branch-dashed>/
```

For example, `grove new mono feat/build-auth` creates:

```text
~/worktrees/mono/feat-build-auth/
```

Repo-level `worktree_root` is an override for that repo. With `worktree_root: ~/scratch/special-worktrees` on `special`, Grove creates `~/scratch/special-worktrees/<branch-dashed>/` without adding the repo name again.

If neither top-level nor repo-level `worktree_root` is set, Grove preserves the legacy location:

```text
<repo>/.grove/worktrees/<branch>/
```

### Repo Fields

- `path`: base repo path.
- `name`: config name used by commands and session names.
- `type`: `worktree` by default. Use `dir` for directory-backed workspaces or `plain` for home-rooted plain workspaces.
- `default_branch`: branch checked out by the default prepare commands.
- `worktree_root`: optional per-repo override.
- `workdir`: subdirectory to print and enter after a worktree is created.
- `prepare`: commands run in the base repo before workspace creation.
- `setup`: commands run in the new workspace after worktree creation.

`grove init` appends a worktree repo entry for the current git root. It infers the config name from the directory, infers the default branch from `origin/HEAD`, `main`, `master`, or the current branch, and leaves `setup: []` for later.

## Commands

```sh
grove                              # pick an existing workspace and print its path
grove cd [workspace]               # pick or print an existing workspace path
grove list                         # show Grove workspaces

grove init                         # add the current git repo to config
grove init --name mono --default-branch dev
grove config                       # open config in $EDITOR
grove config --path                # print config path

grove new                          # pick repo or type a plain workspace name
grove new mono                     # auto-create a branch in mono
grove new -m                       # prompt for branch name
grove new mono feat/build-auth     # create or check out a specific branch
grove new mono agent --from feat/base
grove new --here fix/local-bug     # worktree the current git root
grove new --no-prepare mono agent  # skip prepare commands

grove which                        # print registered repo name for cwd
grove which ~/code/mono/pkg        # print registered repo name for a path

grove done [workspace]             # remove a workspace and print the next path
grove rm [workspace...]            # remove workspaces and their worktrees
grove rm -j 2                      # lower concurrent worktree deletion
grove cleanup                      # remove orphaned worktrees under Grove roots
grove cleanup --all -f             # remove all orphaned worktrees without prompts
```

`grove which [path]` exits non-zero for unregistered paths. It matches configured repo paths and Grove-managed worktrees.

## Workspaces

Worktree workspaces are git worktrees. Session and workspace names use `g/<repo>/<branch>`, while the directory name under shared roots uses a dashed branch name so `feat/build-auth` becomes `feat-build-auth`.

Dir workspaces reuse a configured directory instead of creating a worktree. Plain workspaces are standalone sessions rooted at home.

`grove cleanup` finds git worktrees that exist on disk under Grove-owned roots but are no longer tracked in Grove state. It understands the global root layout, repo-level root overrides, and the legacy `.grove/worktrees` layout.
