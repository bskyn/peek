# Claude Code Session JSONL Format

Claude Code stores session data at `~/.claude/projects/<encoded-project-key>/<session-id>.jsonl`.

## Discovery

- `~/.claude/history.jsonl` has `{sessionId, project, timestamp}` entries — use last entry for latest session
- Fallback: scan `~/.claude/projects/*/` for `*.jsonl` sorted by mtime
- Encoded project key: path with `/` replaced by `-` (roughly)
- Files prefixed `agent-` are subagent files — skip for auto-discovery
- **Gotcha**: `history.jsonl` may not update immediately on `/new` or `/clear`, and the new JSONL file may not exist yet — see [[session-follow-mode]]

## Event Types

| `type` field | Description |
|---|---|
| `user` | User messages; `message.content` is string or array of content blocks |
| `assistant` | Assistant responses; `message.content` is array of `{type, text/thinking/name/input}` blocks |
| `progress` | Streaming progress updates |
| `system` | System events (has `subtype`, `content`, `level`) |
| `file-history-snapshot` | File state snapshots — skip |
| `last-prompt` | Last prompt metadata — skip |

## Content Block Types (in assistant messages)

- `thinking` — extended thinking with `.thinking` text field
- `text` — assistant text response with `.text` field
- `tool_use` — tool invocation with `.name`, `.id`, `.input` fields

## User Content Blocks

- Plain string content
- Array with `tool_result` blocks (from tool executions): `{type: "tool_result", tool_use_id, content}`
- Array with `text` blocks

## Token Estimation

No per-block token count in JSONL. We estimate: `len(thinking_text) / 4`.
`message.usage` has `input_tokens`/`output_tokens` but not per-block.
