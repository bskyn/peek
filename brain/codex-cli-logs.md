# Codex CLI Session Logs

## Storage Location

- Base directory: `~/.codex/` (configurable via `CODEX_HOME`)
- Session rollouts: `~/.codex/sessions/YYYY/MM/DD/rollout-<timestamp>-<thread-id>.jsonl`
- Diagnostic logs: `~/.codex/log/codex-tui.log`
- State DB (SQLite): configured via `sqlite_home`
- Config: `~/.codex/config.toml`

## Filename Format

```
rollout-YYYY-MM-DDThh-mm-ss-<uuid>.jsonl
```

- Timestamp uses `-` instead of `:` for filesystem compatibility
- UUID is the `ThreadId` (conversation ID), e.g. `5973b6c0-94b8-487b-a530-2aeb6098ae0e`
- Example: `rollout-2025-05-07T17-24-21-5973b6c0-94b8-487b-a530-2aeb6098ae0e.jsonl`

## File Format: JSONL

Each line is a JSON object representing a `RolloutLine`:

```json
{"timestamp":"...","type":"<variant>","payload":{...}}
```

The `type`/`payload` envelope comes from `RolloutItem` (tagged enum with `#[serde(tag = "type", content = "payload")]`).

## RolloutItem Variants

1. **`session_meta`** - First line; session-level metadata
2. **`response_item`** - Model response items (messages, tool calls, etc.)
3. **`compacted`** - Compacted conversation summary
4. **`turn_context`** - Per-turn context snapshot
5. **`event_msg`** - Event messages (the bulk of entries)

## SessionMeta Fields (line 1 payload)

| Field | Type | Description |
|-------|------|-------------|
| `id` | UUID | ThreadId - the session identifier |
| `forked_from_id` | UUID? | Parent thread if forked |
| `timestamp` | string | ISO 8601 |
| `cwd` | path | Working directory |
| `originator` | string | Client identifier |
| `cli_version` | string | Codex version |
| `source` | enum | `cli`, `vscode`, `exec`, `mcp`, `subagent_*` |
| `agent_nickname` | string? | Sub-agent name |
| `agent_role` | string? | Sub-agent role |
| `model_provider` | string? | e.g. "openai" |
| `base_instructions` | object? | System prompt |
| `dynamic_tools` | array? | Dynamic tool specs |
| `memory_mode` | string? | Memory configuration |
| `git` | object? | `{commit_hash, branch, ...}` |

## TurnContext Fields

| Field | Type |
|-------|------|
| `turn_id` | string? |
| `trace_id` | string? |
| `cwd` | path |
| `current_date` | string? |
| `timezone` | string? |
| `approval_policy` | enum |
| `sandbox_policy` | enum |
| `network` | object? |
| `model` | string |
| `personality` | enum? |
| `user_instructions` | string? |

## EventMsg Types (subset)

- `turn_started` / `turn_complete` (task_started/task_complete on wire)
- `token_count` - cumulative token usage
- `agent_message` / `agent_message_delta`
- `user_message`
- `exec_command_begin` / `exec_command_output_delta` / `exec_command_end`
- `exec_approval_request`
- `agent_reasoning` / `agent_reasoning_delta`
- `mcp_tool_call_begin` / `mcp_tool_call_end`
- `web_search_begin` / `web_search_end`
- `error` / `warning`
- `context_compacted`
- `plan_update`

## Session Identification

- Sessions identified by `ThreadId` (UUID v7-style)
- Embedded in both the filename and the `session_meta` payload
- `codex resume <SESSION_ID>` to resume by ID
- `codex resume --last` for most recent

## Source Code

- Repo: `github.com/openai/codex` (Rust, `codex-rs/`)
- Protocol types: `codex-rs/protocol/src/protocol.rs`
- Rollout recorder: `codex-rs/core/src/rollout/recorder.rs`
- Rollout module: `codex-rs/core/src/rollout/mod.rs`

## JSON Output (--json flag)

`codex exec --json` streams to stdout with event types:
- `thread.started` (with `thread_id`)
- `turn.started`
- `item.completed` (with `item.id`, `item.type`, `item.text`)
- `turn.completed` (with `usage.input_tokens`, etc.)
- `error`
