# Self-Answered Questions: AI Session Closer

## Batch 1

### 1. Should this live in Grove, layouts, or a standalone Go CLI?

**Answer:** [grounded] Put the core implementation in Grove. Grove is already the repository the user pointed to, already owns tmux key bindings and tmux process/session helpers, and already has commands that act on live tmux sessions (`shadow`, `maximize`, `start`). The `layouts` repo is declarative layout application and split/maximize management; its README explicitly describes zero state files and no lifecycle database. This feature is about live AI session lifecycle plus persistent close notes, so it belongs in Grove. A standalone Go CLI is viable, but Grove gives the least install/config surface because `make install` already deploys one binary to `~/bin/grove`.

### 2. What existing behavior must be preserved from `tclaude` and `tcodex`?

**Answer:** [grounded] `~/.zshrc` defines `tclaude` as `~/.local/bin/tclod`, and `tcodex` as `~/.local/bin/tcodex`. Those scripts call `~/.local/bin/tmux-ai-sessions claude|codex`. That Python script finds tmux panes, walks process trees to detect Claude/Codex child processes, captures pane output, extracts prompt/recent context, and prints a useful list. The new design should preserve "no args = list active sessions" and the same high-signal metadata.

### 3. What does "close" mean for Claude Code and Codex sessions?

**Answer:** [default] Use a safe two-stage close by default: send `C-c` to the target pane, record the archive note and metadata, then leave the pane/session intact unless the user opts into killing the pane/session. The user phrased closing as "like do control c", not "kill the tmux pane". A destructive kill should be explicit via `--kill-pane` or `--kill-session` because many active sessions may contain useful shell state after the agent exits/intercepts.

## Batch 2

### 4. Where should the remembered session notes be stored?

**Answer:** [default] Use XDG state: `${XDG_STATE_HOME:-~/.local/state}/grove/ai-sessions.jsonl`. This is a separate durable file, easy to append to, inspect with `jq`, back up, or migrate. It should not live in `~/.config/grove/config.yaml` because this is user activity state, not configuration.

### 5. How should the user provide the string/note for closed sessions?

**Answer:** [default] Support both `--note "..."` and an interactive prompt. If `--note` is omitted and stdin is a terminal, prompt once: `Note for archived sessions:`. Apply the same note to all selected sessions, while allowing future `--note-file` or per-session editing if needed. Non-interactive usage should require `--note` or default to an empty note with a clear warning.

### 6. How should target sessions be selected?

**Answer:** [grounded] Reuse the current listing model and add a picker mode. The existing `tclaude`/`tcodex` output enumerates sessions with indexes, pane IDs, cwd, title, prompt, and recent output. The new close command should support `--pane %123` for scripts, `--all` for batch cleanup, and interactive `fzf --multi` when available. If `fzf` is missing, fall back to printing numbered sessions and accepting comma-separated indexes.

## Batch 3

### 7. What data is needed to resume or understand a closed session later?

**Answer:** [grounded] Existing panes expose `session_name`, `window_index`, `pane_index`, `pane_id`, `pane_pid`, `pane_current_path`, `pane_title`, `window_name`, and process command. Existing `tmux-ai-sessions` also extracts `prompt` and `recent` from `capture-pane`. Archive records should include all of that plus tool (`claude` or `codex`), close time, note, action taken, and a best-effort resume hint. Resume hints can be precise only when the process command already contains Claude `-r <id>` or Codex `resume <id>`; otherwise the record should suggest reopening the tool in the same cwd with the note.

### 8. Should the feature try to infer Claude/Codex conversation IDs?

**Answer:** [default] Only infer IDs from visible process arguments and captured output; do not scrape private vendor state files in v1. Process args sometimes include `claude -r <uuid>` or `codex resume <id>`. Fresh sessions often do not expose a stable ID. The design should avoid brittle filesystem archaeology and instead store human-readable notes plus captured context so the user can decide how to resume.

### 9. How should closing handle active/busy agents?

**Answer:** [default] Treat close as an intentional interrupt. Send `C-c` once, wait a short configurable grace period, then optionally escalate only when the user supplied `--kill-pane` or `--kill-session`. Always archive metadata before sending keys, so a failed close still leaves a record. Report per-pane success/failure.

## Batch 4

### 10. Should the `tclaude` and `tcodex` commands remain external scripts?

**Answer:** [default] Keep shell wrapper commands but point them at Grove subcommands. This preserves muscle memory and tmux config (`bind C ... "$HOME/.local/bin/tclod"`, `bind O ... "$HOME/.local/bin/tcodex"`) while moving logic into tested Go code. `make install` can install or update `tclod`, `tclaude`, and `tcodex` shims later.

### 11. What command shape is easiest to remember?

**Answer:** [default] `tclaude` and `tcodex` stay as top-level user commands. Internally they map to `grove ai claude ...` and `grove ai codex ...`. Examples: `tclaude` lists, `tclaude close`, `tclaude close --note "done with referral investigation"`, `tclaude history`, `tclaude show <id>`. The direct Grove form is also available for composability.

### 12. What tests are needed before implementation?

**Answer:** [grounded] Grove already has fake-`tmux` command tests in `cmd/shadow_test.go` and config defaults tests. Add unit tests for process-tree matching, pane-list parsing, prompt/recent extraction, archive record serialization, and command-level close behavior using fake `tmux`. Avoid tests that require a live tmux server.
