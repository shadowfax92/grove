# Approaches: AI Session Closer

## Option A: Extend Grove With `grove ai` and Keep `tclaude`/`tcodex` Shims

Add a new Grove command group: `grove ai <claude|codex>`. It owns listing, selecting, archiving, and closing AI sessions. The existing user-facing `tclaude`, `tclod`, and `tcodex` commands become thin shell wrappers around the Grove subcommands. The implementation ports the existing Python session discovery behavior into Go: list tmux panes, inspect process descendants, match Claude/Codex processes, capture pane output, and extract prompt/recent text.

Advantages:
- Fits the current Grove architecture and install path.
- Preserves existing command muscle memory.
- Can be tested with fake `tmux` and process-table fixtures.
- Keeps `layouts` focused on layouts, not lifecycle state.
- One durable state file under Grove ownership.

Disadvantages:
- Requires porting useful Python parsing behavior to Go.
- `make install` must learn to install/update shim scripts if full replacement is desired.

Complexity: Medium
Risk: Low-Medium

## Option B: Keep Python Listing Script and Add a Separate Go Closer

Leave `~/.local/bin/tmux-ai-sessions` as the source of truth for listing. Add a small Go CLI that shells out to the Python script for selection/listing and handles archiving/closing. `tclaude` and `tcodex` call the new CLI only for close/history subcommands.

Advantages:
- Fastest path to v1.
- Avoids porting prompt extraction right away.
- Lower amount of Go code.

Disadvantages:
- Splits behavior across untracked scripts and the repo.
- Harder to test and install reproducibly.
- The current Python script lives outside this repository, so the repo would not fully explain the feature.

Complexity: Low
Risk: Medium

## Option C: Add Lifecycle Commands to `layouts`

Add `layouts agents` or `layouts close-ai` commands to the tmux layout manager, using its tmux package and process inspection.

Advantages:
- `layouts` already models Claude/Codex panes in example configs.
- Layout users may expect agent panes to be managed with layout commands.

Disadvantages:
- Contradicts `layouts` zero-state, layout-only product boundary.
- Requires duplicating session discovery and archive state there.
- User pointed at Grove/tmux-manager first and asked for a short CLI, not layout schema changes.

Complexity: Medium
Risk: Medium-High

## Decision

Choose Option A: extend Grove with `grove ai` and keep the `tclaude`/`tcodex` shims. It best matches the existing Grove responsibility for tmux actions, preserves the current commands, and creates one testable Go implementation instead of layering another untracked script on top of the Python helper.
