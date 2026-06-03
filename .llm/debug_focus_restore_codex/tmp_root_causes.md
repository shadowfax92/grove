## Issue

Pressing `Cmd+Y`/`M-y` to restore a centered Grove focus popup can make the Codex pane stop responding or disappear from its original position.

## Root Causes (ranked by probability)

### 1. [Addressed] — Restore closes the popup before moving the real pane home
- **Why**: In focus mode, the real process pane is moved into the temporary `gm/<pane>` session. `restoreMaximizedPane` currently closes the popup before swapping that pane back. If any subsequent step fails, the tmux binding swallows the error and the real Codex pane can remain stranded in `gm/<pane>` while the user sees the placeholder in the original layout.
- **Evidence**: `restoreMaximizedPane` calls `tmux.ClosePopup` before checking pane existence, swapping panes, or killing the temp session. An isolated tmux-socket reproduction showed pane IDs move into `gm/...` on maximize and must be swapped back before killing/losing the temp session.
- **Files**: `cmd/maximize.go`, `internal/tmux/tmux.go`, `cmd/maximize_test.go`
- **Fix**: `restoreMaximizedPane` now validates both panes, swaps the real pane back first, selects it in the original layout, then closes the popup and kills the temporary maximize session.

### 2. [Partially addressed] — Missing-pane fallback kills the temp session without repairing the original slot
- **Why**: If the real pane exits or the placeholder is missing, restore currently kills the `gm/<pane>` session. If the real pane is gone, the original layout can still contain the blank placeholder, which looks like the app stopped working.
- **Evidence**: `restoreMaximizedPane` returns `tmux.KillSession(currentSession)` when either pane ID is missing.
- **Files**: `cmd/maximize.go`
- **Fix**: If the placeholder pane is missing, restore now returns an error without closing the popup or killing `gm/<pane>`, because the real process is still inside that session. If the real origin pane is gone, Grove still cleans up the temporary session because there is no live pane left to restore.

### 3. [Addressed] — Restore does not explicitly focus the restored pane
- **Why**: After swapping the real pane back, tmux may leave selection on a placeholder or another pane. The process may still be alive, but input can appear broken.
- **Evidence**: The maximize path selects the pane after swapping into focus mode; the restore path does not select it after swapping back.
- **Files**: `cmd/maximize.go`, `cmd/maximize_test.go`
- **Fix**: The restore path now calls `select-pane` on the restored real pane before closing the popup.
