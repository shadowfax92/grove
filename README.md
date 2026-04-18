# Grove

**Persistent popup sessions for tmux — vim and shell shadows for any pane.**

Press `Alt+I` for an editor popup, `Alt+O` for a shell popup, `Alt+D` to delete both shadow sessions for the current pane, and `Ctrl+Shift+Y` to maximize or restore. The popup follows your pane's working directory.

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

# 2. Press Alt+I for vim popup, Alt+O for shell popup, Alt+D to delete both,
#    Ctrl+Shift+Y to maximize or restore
```

To make the bindings persist across tmux restarts, add to your `.zshrc`:

```sh
if [ -n "$TMUX" ]; then
  grove start >/dev/null 2>&1
fi
```

Or add to `~/.tmux.conf`:

```tmux
run-shell 'grove start'
```

## Config

Location: `~/.config/grove/config.yaml` (created automatically on first run)

```yaml
shadow:
  popup:
    width: "80%"
    height: "95%"
    max_width: "50%"
    max_height: "100%"
  keys:
    vim: M-i
    shell: M-o
    delete: M-d
    maximize: M-y      # tmux can't see Ctrl+Shift+<letter>; see Kitty mapping below
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

`Ctrl+Shift+Y` opens a centered maximize popup using `max_width` and `max_height`. For normal panes, Grove swaps the pane with a temporary placeholder before opening the popup, then swaps it back on restore so the original layout slot is preserved.

### Ctrl+Shift+Y in Kitty

tmux cannot distinguish `Ctrl+Shift+<letter>` from plain `Ctrl+<letter>` in legacy terminal mode — the shift bit never makes it over the wire. Grove's default binding is therefore `M-y` (Alt+y), and Kitty is configured to translate the familiar `Ctrl+Shift+Y` chord into that. Add this to `~/.config/kitty/kitty.conf`:

```conf
map ctrl+shift+y send_text all \x1by
```

If you don't use Kitty, either bind `M-y` directly or set `maximize:` in `~/.config/grove/config.yaml` to any tmux-supported key (e.g. `F12`).

## Usage with Layouts

Grove pairs well with [layouts](https://github.com/shadowfax92/layouts) for tmux pane management:

```sh
# See available layouts
layouts list

# Apply a layout to your current tmux session
layouts apply dev          # 3 windows: claude+editor+shell, test, codex
layouts apply simple       # 3 windows: editor, claude, shell

# Create a NEW tmux session with a layout pre-applied
layouts new myproject dev

# Then use grove popups in any pane
# Alt+I → vim popup, Alt+O → shell popup, Alt+D → delete both,
# Ctrl+Shift+Y → maximize/restore
```

## CLI

```sh
grove start                    # bind popup keys in current tmux server
grove maximize ...             # (internal) toggle centered maximize popup
grove config                   # open config in $EDITOR
grove config --path            # print config file path
grove shadow toggle vim ...    # (internal) toggle a shadow popup
grove shadow delete ...        # (internal) delete current pane's shadow popups
grove shadow cleanup           # (internal) clean up orphaned sessions
grove --version                # print version
```
