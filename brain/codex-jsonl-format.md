# Codex JSONL Format

Codex CLI stores session rollouts at `~/.codex/sessions/YYYY/MM/DD/rollout-<timestamp>-<uuid>.jsonl`. Respects `CODEX_HOME` env var.

## Top-Level Line Structure

Every line has `{timestamp, type, payload}`.

## Line Types

### session_meta
First line. Contains `id`, `cwd`, `cli_version`, `model_provider`, `base_instructions`.

### event_msg
Wrapper with `payload.type` subtypes:
- `user_message` — user input (`message` field)
- `agent_message` — assistant commentary (`message` + `phase`: "commentary" or "final")
- `task_started` / `task_complete` — turn lifecycle
- `token_count` — usage stats in `info.total_token_usage`
- `error` / `warning` — error messages

### response_item
Model response objects with `payload.type`:
- `message` — content blocks (`role`: user/assistant/developer, `content[]` with `output_text` or `input_text`). **Duplicates `event_msg` for user/assistant — skip these to avoid doubled output.** Only `function_call`, `function_call_output`, and `reasoning` are unique to `response_item`.
- `function_call` — tool invocation (`name`, `arguments` as JSON string, `call_id`)
- `function_call_output` — tool result (`call_id`, `output` as string)
- `reasoning` — encrypted reasoning (`encrypted_content`, `summary[]`, no readable content)

### turn_context
Session context snapshot (model, cwd, approval policy). Skipped in parsing.

### compacted
Summarized context. Out of scope currently.

## Key Differences from Claude Format

| Aspect | Claude | Codex |
|--------|--------|-------|
| File location | `~/.claude/projects/<key>/<session-id>.jsonl` | `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl` |
| Session ID | Filename stem | UUID in filename suffix |
| User messages | `type: "user"` with `message.content` | `event_msg` with `type: "user_message"` |
| Tool calls | `tool_use` content block in assistant | `response_item` with `type: "function_call"` |
| Tool results | `tool_result` content block in user | `response_item` with `type: "function_call_output"` |
| Thinking | `thinking` content block (readable) | `reasoning` response_item (encrypted) |
| Tool arguments | `input` as JSON object | `arguments` as JSON string |
