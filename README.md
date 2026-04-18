# Grove

Tmux workspaces powered by git worktrees.

Grove manages repo worktrees, plain workspaces, and tmux sessions, but the default interaction is now picker-first and path-first:

- `grove` picks an existing workspace and prints its path
- `grove new` creates a workspace and prints its path
- `grove new --tmux` creates the workspace and a tmux session
- `grove rm` starts with Grove-managed workspaces and lets `gs/` searches reach shadow sessions

## Install

Requires Go 1.21+, tmux 3.3+, [fzf](https://github.com/junegunn/fzf), and optionally [`layouts`](https://github.com/shadowfax92/layouts) for session layouts.

```sh
git clone <repo-url> grove
cd grove
make install
```

For fish, install the `gv` helper:

```sh
make fish
```

Useful fish flows:

```fish
gv                     # pick an existing workspace and cd into it
gv n mono feat-auth    # create a workspace and cd into it
gv nt mono feat-auth   # create a workspace and tmux session
gv cd mono/feat-auth   # cd into an existing workspace
gv dd                  # finish the cwd-backed workspace and cd home
```

## Quick Start

```sh
# 1. Edit config to add your repos
grove config

# 2. Start grove — reconcile sessions and attach to tmux
grove start

# 3. Create workspaces
grove new mono feat-auth        # create worktree and print its path
grove new --tmux mono feat-auth # create worktree and tmux session
grove new notes                 # plain workspace
```

If you want shell `cd` behavior, use `gv` instead of `grove`.

## Config

Location: `~/.config/grove/config.yaml`

```yaml
notify:
  forward:
    - mac-notify send "$MESSAGE" --source "$SESSION" --id "$SESSION"

shadow:
  popup:
    width: "80%"
    height: "85%"
  keys:
    vim: "M-v"
    shell: "M-b"

repos:
  - path: ~/code/mono
    name: mono
    layout: dev
    setup:
      - bun install
      - cp .env.example .env

  - path: ~/code/workers
    name: workers
    setup:
      - npm install
```

`notify.forward` runs shell commands when a workspace notification is sent. `$SESSION` and `$MESSAGE` are substituted.

`repos` defines the repositories Grove can create workspaces for. Worktree repos create git worktrees under `<repo>/.grove/worktrees/<name>/`. Plain repos create plain tmux workspaces rooted at home. Dir repos create named workspaces rooted inside a configured directory.

## CLI

```sh
grove                        # pick an existing workspace and print its path
grove new                    # pick repo or type a workspace name, then print its path
grove new mono               # pick or auto-generate branch in mono, then print its path
grove new mono feat-auth     # create specific workspace and print its path
grove new --tmux mono feat-auth
grove cd                     # pick an existing workspace and print its path
grove cd mono/feat-auth      # print a specific workspace path
grove done --tmux            # finish current tmux workspace and switch to the next one
grove done --cd              # finish cwd-backed workspace and print home
grove rm                     # interactive remove picker
grove rm mono/feat-auth      # remove specific workspace
grove list                   # show all Grove workspaces and status
grove switch                 # pick workspace via fzf and switch tmux
grove config                 # open config in $EDITOR
grove config --path          # print config path
grove notify "build done"    # send notification to current workspace
grove notify clear           # clear notifications interactively
```

## Interaction Model

### Workspaces

- Repo workspaces are git worktrees created under `<repo>/.grove/worktrees/<name>/`.
- Plain workspaces are standalone Grove sessions rooted at home.
- Session names use `g/<repo>/<branch>` for repo workspaces and `g/<name>` for plain workspaces.

### Start

`grove start` reads config and state, recreates missing tmux sessions for existing workspaces, installs the shadow-session keybindings, and attaches to tmux. It no longer binds a sidebar popup.

### Remove Picker

`grove rm` starts by showing Grove-managed workspaces only. When you begin filtering with `gs/`, the picker expands so shadow sessions are removable without cluttering the blank-state list.

## Notifications

Any process running inside a Grove workspace can send a notification:

```sh
grove notify "build complete"
```

Notifications are stored in Grove state and shown in `grove list`. Switching to a workspace or clearing the notification removes the badge.
