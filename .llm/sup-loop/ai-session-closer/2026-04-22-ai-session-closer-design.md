# Design: AI Session Closer and Archive

## Summary

Add a Grove-owned AI session manager that preserves the current `tclaude` and `tcodex` workflow while adding a safe close/archive path. The user can list active Claude Code or Codex panes, select sessions to close, enter a short note describing what is being closed, and later review a durable history file to know what can be resumed.

The core implementation should live in this Grove repo as a new `grove ai` command group. The existing `tclaude`, `tclod`, and `tcodex` commands should remain as small shell shims that call Grove. The layout manager should not own this feature because it is layout configuration and pane arrangement tooling, while this feature is live process lifecycle plus persistent user activity state.

## Assumptions

- "Close like control-c" means the default action should send `C-c` to the selected pane. It should not kill panes or tmux sessions unless the user explicitly asks for that with a flag.
- Resume support means "remember enough context and a best-effort resume hint", not guaranteed reconstruction of every Claude/Codex internal conversation. Fresh sessions may not expose a stable resume ID.
- It is acceptable for v1 to use one note for all sessions selected in a single close operation.
- `fzf` can be used when available, but the command must have a non-fzf fallback.

## Existing Context

Grove is a small Go/Cobra CLI. Commands live in `cmd/`; tmux wrappers live in `internal/tmux`; configuration lives in `internal/config`; shadow session lifecycle lives in `internal/shadow`. Existing command patterns register with `init()` and use thin tmux CLI wrappers.

The current user commands are outside the repo:

- `~/.zshrc` maps `tclaude()` to `~/.local/bin/tclod`.
- `~/.zshrc` maps `tcodex()` to `~/.local/bin/tcodex`.
- `~/.local/bin/tclaude` runs `tmux-ai-sessions claude`.
- `~/.local/bin/tcodex` runs `tmux-ai-sessions codex`.
- `~/.tmux.conf` opens the tools in popups with prefix bindings:
  - `bind C display-popup -E -w 80% -h 60% "$HOME/.local/bin/tclod"`
  - `bind O display-popup -E -w 80% -h 60% "$HOME/.local/bin/tcodex"`

The Python `tmux-ai-sessions` script already has the right discovery model:

- Query `tmux list-panes -a` with session, window, pane, pid, cwd, title, and active flag.
- Read the process table with `ps`.
- Walk process descendants from each pane pid.
- Match Claude and Codex processes by command.
- Capture pane output with `tmux capture-pane`.
- Extract a high-signal prompt and recent output line.
- Print active sessions in a sorted list.

The design should preserve that behavior while moving it into the repo and adding close/archive actions.

## Goals

- `tclaude` with no arguments still lists active Claude sessions.
- `tcodex` with no arguments still lists active Codex sessions.
- `tclaude close` and `tcodex close` let the user select one or more active sessions, enter a note, archive metadata, and send `C-c`.
- A history command shows archived sessions and notes.
- The archive file is durable, inspectable, and separate from config.
- The implementation is testable without a live tmux server.

## Non-Goals

- Do not kill tmux panes by default.
- Do not scrape Claude or Codex private vendor state files for hidden conversation IDs in v1.
- Do not change layout YAML or `layouts apply/new/split` behavior.
- Do not build a full dashboard or database.
- Do not attempt automatic semantic summarization with an LLM.

## User-Facing Commands

Primary shims:

```sh
tclaude                       # list active Claude sessions
tclaude close                 # select Claude sessions, prompt for note, send C-c
tclaude close --note "done"   # select sessions, save note, send C-c
tclaude close --pane %319 --note "done"
tclaude history
tclaude show <archive-id>

tcodex                        # list active Codex sessions
tcodex close --note "stale review sessions"
tcodex history
tcodex show <archive-id>
```

Equivalent Grove commands:

```sh
grove ai claude list
grove ai claude close
grove ai claude history
grove ai claude show <archive-id>

grove ai codex list
grove ai codex close
grove ai codex history
grove ai codex show <archive-id>
```

Default subcommand behavior:

- `grove ai claude` aliases to `grove ai claude list`.
- `grove ai codex` aliases to `grove ai codex list`.
- `tclod` remains an alias to Claude listing/closing for compatibility with the existing zsh function.

## Command Semantics

### List

`list` prints the same style of information as the existing Python script:

- number
- tmux location (`session:window.pane`)
- pane ID
- elapsed process age
- active marker
- cwd
- title
- prompt if found
- recent useful output if found

The list command should have an optional machine-readable format later, but v1 can keep human output and test internal records directly.

### Close

`close` follows this sequence:

1. Discover active sessions for the selected tool.
2. Select targets:
   - `--pane %id` may be repeated and bypasses interactive selection.
   - `--all` selects every active session for that tool.
   - otherwise use `fzf --multi` if available.
   - if `fzf` is unavailable, print numbered sessions and read comma-separated indexes.
3. Build archive records before sending keys.
4. Resolve the note:
   - `--note "..."` wins.
   - `--note-file path` reads note text.
   - if neither is supplied and stdin is a terminal, prompt once.
   - if non-interactive, use an empty note and print a warning.
5. Append records to the archive JSONL file.
6. Send `tmux send-keys -t <pane_id> C-c` for each target.
7. Report per-session result.

Optional escalation flags:

- `--kill-pane`: after sending `C-c`, wait a short grace period and kill the pane.
- `--kill-session`: after sending `C-c`, wait a short grace period and kill the tmux session. This should require an exact flag and should not be implied by `close`.
- `--dry-run`: show selected sessions and archive preview without writing or sending keys.

### History

`history` reads the JSONL archive and prints recent records newest-first:

- archive ID
- closed timestamp
- tool
- note
- cwd
- title
- prompt/recent
- action taken
- resume hint

Useful filters:

- `--tool claude|codex`
- `--cwd contains`
- `--since duration`
- `--limit n`

### Show

`show <archive-id>` prints one full archive record, including captured command, tmux identifiers, note, and resume hint.

## Archive Storage

Use XDG state:

```text
${XDG_STATE_HOME:-~/.local/state}/grove/ai-sessions.jsonl
```

Each line is one JSON object. Use append-only writes so records are easy to audit. Ensure the directory exists before writing.

Record schema:

```json
{
  "id": "20260422T143015-claude-319",
  "tool": "claude",
  "closed_at": "2026-04-22T14:30:15-07:00",
  "note": "done with referral investigation",
  "action": "interrupt",
  "tmux": {
    "session": "BOS-REFERRAL_SYSTEM",
    "window_index": "2",
    "pane_index": "1",
    "pane_id": "%232",
    "pane_pid": 38857,
    "active": true,
    "window_name": "agent"
  },
  "process": {
    "pid": 40995,
    "etime": "05-16:52:23",
    "command": "claude --effort xhigh --dangerously-skip-permissions"
  },
  "context": {
    "cwd": "/Users/felarof01/Workspaces/build/browseros-main/packages/browseros-agent",
    "title": "Debug credits system Twitter URL in production",
    "prompt": "",
    "recent": "Next action: ship the agent extension release."
  },
  "resume": {
    "kind": "best_effort",
    "command": "cd /Users/felarof01/Workspaces/build/browseros-main/packages/browseros-agent && claude",
    "reason": "no explicit Claude resume id was visible in process args"
  }
}
```

Resume hint rules:

- If Claude command contains `-r <id>` or `--resume <id>`, set `kind: "exact"` and command `claude -r <id>` from the recorded cwd.
- If Codex command contains `resume <id>`, set `kind: "exact"` and command `codex resume <id>` from the recorded cwd.
- Otherwise set `kind: "best_effort"` with `claude` or `codex` from the recorded cwd and include the note/prompt/recent context for manual recovery.

## Architecture

### `cmd/ai.go`

New Cobra command group:

- `ai`
- `ai claude`
- `ai codex`
- nested `list`, `close`, `history`, `show`

The command layer should only parse flags, call internal packages, and format output.

### `internal/ai/session`

Responsible for discovering active AI sessions:

- tmux pane parsing
- process table parsing
- descendant walking
- tool matching
- capture-pane prompt/recent extraction

Core types:

```go
type Tool string

const (
    ToolClaude Tool = "claude"
    ToolCodex  Tool = "codex"
)

type ActiveSession struct {
    Tool       Tool
    Pane       tmux.PaneProcessInfo
    Process    ProcessInfo
    Prompt     string
    Recent     string
}
```

### `internal/ai/archive`

Responsible for append-only JSONL archive storage:

- resolve state path
- append records
- read records
- filter/sort
- generate archive IDs

This package should not call tmux.

### `internal/ai/close`

Responsible for close execution:

- select sessions from user inputs
- create archive records
- call archive writer
- send `C-c`
- optionally kill pane/session when explicit escalation flags are set

This package can depend on `internal/tmux`, `internal/ai/session`, and `internal/ai/archive`.

### `internal/tmux`

Extend the existing thin wrapper package with the smallest needed helpers:

- `ListPaneProcessInfo()`
- `CapturePane(paneID string, lines int)`
- `SendKeys(target string, keys ...string)`
- `KillPane(target string)`
- optionally `KillSession` already exists

Keep wrapper behavior consistent with existing code: call `tmux`, trim output, return wrapped errors.

### Shims

Install shell scripts:

```sh
#!/usr/bin/env bash
exec "$HOME/bin/grove" ai claude "$@"
```

```sh
#!/usr/bin/env bash
exec "$HOME/bin/grove" ai codex "$@"
```

Install names:

- `~/.local/bin/tclaude`
- `~/.local/bin/tclod`
- `~/.local/bin/tcodex`

If the repo keeps `PREFIX ?= $(HOME)/bin`, add a separate `AI_SHIMS ?= $(HOME)/.local/bin` variable in the Makefile.

## Data Flow

### Listing

1. User runs `tclaude`.
2. Shim runs `grove ai claude`.
3. Grove queries tmux panes.
4. Grove reads process table.
5. Grove matches Claude descendants under pane pids.
6. Grove captures pane output for matching panes.
7. Grove prints sorted active sessions.

### Closing

1. User runs `tclaude close --note "old research thread"`.
2. Grove discovers active Claude sessions.
3. User selects sessions with fzf or indexes.
4. Grove creates archive records from current metadata.
5. Grove appends JSONL records.
6. Grove sends `C-c` to each selected pane.
7. Grove reports what was archived and interrupted.

### Reviewing Later

1. User runs `tclaude history`.
2. Grove reads JSONL records.
3. Grove prints recent records with note, cwd, prompt/recent, and resume hint.
4. User runs `tclaude show <id>` for full metadata.

## Error Handling

- If tmux is not running, list should print "No active Claude sessions found" or "No active Codex sessions found", matching current behavior.
- If archive append fails, do not send `C-c`; report the error so the user does not lose context.
- If archive succeeds but `C-c` fails for one pane, keep the archive record and report the failed pane.
- If a selected pane disappears between selection and close, archive the record with action `missing_before_interrupt` and continue with the next pane.
- If process matching sees multiple matching descendants, use priority rules like the Python script: prefer concrete Claude/Codex binaries over wrapper processes.
- If capture-pane fails, still list/archive the session with empty prompt/recent and include a warning in verbose mode.

## Testing Plan

Unit tests:

- Parse tmux pane lines with tabs and malformed rows.
- Parse process table rows and descendant trees.
- Match Claude commands:
  - `claude`
  - paths containing `/claude/versions/`
  - commands with `-r <id>`
- Match Codex commands:
  - `codex`
  - vendor binary ending `/codex/codex`
  - `node .../codex`
  - `codex resume <id>`
- Extract prompt/recent from captured pane lines using fixtures from Claude and Codex output.
- Generate exact and best-effort resume hints.
- Append/read JSONL archive records.

Command tests with fake `tmux`:

- `grove ai claude` lists matching panes.
- `grove ai codex` lists matching panes.
- `grove ai claude close --pane %1 --note "done"` writes JSONL then sends `C-c`.
- Archive write failure prevents `send-keys`.
- `--dry-run` writes nothing and sends no keys.
- `history` reads and prints records newest-first.

Manual verification:

- Run `tclaude` and compare output with the current Python helper.
- Run `tcodex` and compare output with the current Python helper.
- Close one harmless shell-launched Claude/Codex test pane with `--note`.
- Verify the archive line is written.
- Verify `history` and `show` make the record easy to resume from.

## Implementation Notes

- Port the current Python script behavior incrementally. First implement session discovery and list output, then close/archive.
- Keep all persistent records in JSONL. Do not introduce SQLite or YAML for session history.
- Use stable archive IDs derived from timestamp, tool, and pane number. If there is a collision, append a short suffix.
- Do not update `~/.zshrc` or `~/.tmux.conf` in the core implementation. Existing config already calls `tclod` and `tcodex`; installing compatible shims is enough.
- Keep destructive actions explicit. The default `close` action is `interrupt`, not kill.

## Open Tradeoff

There is one deliberate product tradeoff: v1 will not guarantee exact resume for every session. It records the current pane context, prompt/recent output, cwd, process command, and user note. Exact resume is only offered when the process command exposes a resume ID. This keeps the implementation reliable and avoids brittle scraping of tool-private state.

## Self-Review

- Placeholder scan: no TBD/TODO placeholders remain.
- Internal consistency: commands, storage path, data flow, and tests all describe the same Grove-owned implementation.
- Scope check: this is one focused implementation plan: session discovery, archive storage, close action, and shims.
- Ambiguity check: default close behavior is explicitly `C-c` only; pane/session killing requires explicit flags.
