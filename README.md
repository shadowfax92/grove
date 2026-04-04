# Grove

**Persistent popup sessions for tmux — vim and shell shadows for any pane.**

Press `Alt+V` for an editor popup, `Alt+B` for a shell popup. The popup follows your pane's working directory.

## Install

Requires Go 1.21+ and tmux 3.3+.

```sh
git clone <repo-url> grove
cd grove
make install    # builds and copies to ~/bin/
```

Make sure `~/bin` is on your `PATH`.

## Quick Start

```sh
# 1. Bind the popup keys in your tmux session
grove start

# 2. Press Alt+V for vim popup, Alt+B for shell popup
```

To make the bindings persist across tmux restarts, add to `~/.tmux.conf`:

```tmux
run-shell 'grove start'
```

## Config

Location: `~/.config/grove/config.yaml` (created automatically on first run)

```yaml
shadow:
  popup:
    width: "80%"
    height: "85%"
  keys:
    vim: M-v
    shell: M-b
```

Edit the config:

```sh
grove config
```

## How It Works

**Shadow sessions** are temporary tmux sessions that "follow" a pane's working directory. When you press the keybinding:

1. Grove finds the current pane's working directory
2. Creates (or reuses) a shadow session named `gs/<type>/<pane_id>`
3. Opens it in a tmux popup overlay

If the pane's directory has changed since the shadow was created, the shadow is recreated in the new directory. When panes are closed, orphaned shadow sessions are automatically cleaned up.

## CLI

```sh
grove start                    # bind popup keys in current tmux server
grove config                   # open config in $EDITOR
grove config --path            # print config file path
grove shadow toggle vim ...    # (internal) toggle a shadow popup
grove shadow cleanup           # (internal) clean up orphaned sessions
grove --version                # print version
```
