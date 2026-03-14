# Architecture

## Project Structure

```
cmd/peek/main.go                — entry point
internal/cli/                   — cobra commands (root, claude, codex, run, workspace, sessions)
internal/connector/claude/      — Claude-specific parsing + discovery
internal/connector/codex/       — Codex CLI parsing + discovery
internal/event/                 — canonical event model (8 types) + payload extractors
internal/managed/               — managed runtime, checkpoint engine, branch/switch/merge orchestrator
internal/store/                 — SQLite persistence (sessions, events, cursors, workspaces, checkpoints)
internal/tailer/                — cursor-based JSONL file tailing (fsnotify + poll)
internal/renderer/              — terminal output with ANSI colors + diff rendering
internal/viewer/                — embedded web UI (Vite build output in dist/)
internal/workspace/             — workspace domain types (status, snapshot kind, lineage)
web/                            — React web UI source (see [[web-ui]])
```

## CLI Pattern

Subcommand-based: `peek run claude|codex` (managed), `peek claude|codex [session-id]` (passive), `peek workspace` (branching)

Originally tried `--claude` flag with optional value but cobra's `NoOptDefVal` doesn't work well — positional args get swallowed. Switched to subcommand pattern.

## Key Design Decisions

- **Passive tailing only** (Plan 1) — observes existing Claude sessions, does not spawn new ones
- **Pure Go SQLite** (`ncruces/go-sqlite3` WASM) — no CGo, enables clean cross-compilation
- **Follow-mode** — auto-switches to new sessions when Claude creates them (on `/clear` or `/new`)
- **Cursor-based resume** — `--replay` flag to override and replay from beginning
- **Content block splitting** — one Claude assistant message becomes multiple events (thinking, text, tool_use each get own seq)
- **Edit/Write diff rendering** — LCS-based inline diffs for file modification tool calls (capped at 500 combined lines to avoid O(n·m) stalls)
- **Cursor integrity** — cursor only advances when all events in a batch were persisted successfully. Prevents silent data loss on resume.
- **Payload schema** — all event payloads use `"text"` as the standard field for displayable content (including tool_result). Renderer reads via `PayloadText()`.
- **Payload extractors in `event` package** — `PayloadText`, `PayloadThinking`, `PayloadToolCall`, `PayloadEditCall`, `PayloadWriteCall` live in `event/payload.go`, shared across connectors.
- **Independent connector pattern** — each connector is a separate package (`claude/`, `codex/`) with matching function signatures (`Discover`, `ParseLine`, `SessionFile`). No shared interface — wait for 3+ connectors before abstracting.
- **Renderer source-awareness** — `TerminalRenderer.Source` field controls assistant message label ("Claude", "Codex", etc.). Set by CLI command.
- **Codex encrypted reasoning** — Codex reasoning tokens are encrypted and unreadable. Rendered as placeholder text.
- **Managed runtime** (Plan 6) — `peek run claude|codex` launches the native CLI as a subprocess, creates a workspace with session linkage, and owns the live terminal while `peek workspace branch|switch` control it from another shell.
- **Checkpoint engine** — pre-tool and post-tool snapshots stored as hidden git refs (`refs/peek/<ws>/<seq>/<kind>`) via synthetic commits. No visible branches polluting the user's git surface.
- **Branch semantics** — branching from a `tool_call` resolves to the pre-result snapshot. Source workspace freezes on branch, child materializes as a git worktree.
- **Resume planning** — new child workspaces relaunch from a synthesized transcript seed, while previously discovered provider sessions switch back via provider-native resume (`claude --resume`, `codex resume`) when possible.
- **Cold worktrees** — inactive workspaces are dematerialized to ref-only storage. Switch re-materializes on demand via `git worktree add --detach`.
- **Workspace graph + control plane** — 6 tables (workspaces, workspace_sessions, checkpoints, branch_path_segments, managed_runtimes, managed_runtime_requests) store lineage, snapshots, and same-terminal runtime control independently from raw session metadata.
- **Live merge + exit propagation** — merge snapshots a warm child worktree before merging, conflict handoff marks the branch as conflicted, and wrapped provider exit codes bubble back out of `peek run`.

## Distribution

- GoReleaser for multi-platform builds (darwin/linux/windows, amd64/arm64)
- npm wrapper pattern (like esbuild/turbo): `npm i -g peek`
- curl install script from GitHub Releases
- `go install` for Go users
