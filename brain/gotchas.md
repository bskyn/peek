# Gotchas

## JSON Truncation

Never byte-slice marshaled JSON to truncate payloads — it produces invalid JSON that silently fails to unmarshal downstream. Truncate string *values* before marshaling instead (via `truncateString`).

## Cross-Boundary Payload Contracts

Parser and renderer communicate through `PayloadJSON` with no compile-time contract. When adding a new event type or changing payload shape, verify both sides agree on field names. The `tool_result` bug (parser wrote `"content"`, renderer read `"text"`) went undetected because tests only checked event type, not payload round-trip through rendering.

## Cursor Save Safety

The cursor byte offset must only advance when all events between old and new offset were successfully persisted. Otherwise, a restart skips unpersisted events permanently. Use an atomic failure flag in the processing goroutine.

## LCS Diff Scaling

`computeLCS` allocates an O(n·m) matrix. On large Edit tool calls (thousands of lines), this can stall real-time tailing and spike memory. Capped at `maxDiffLines = 500` with a raw fallback.

## Codex Duplicate Messages (event_msg vs response_item)

Codex emits user and assistant messages in **two** line types: `event_msg` (`user_message`/`agent_message`) and `response_item/message` (`role: "user"`/`role: "assistant"`). Parsing both produces doubled output. The fix: skip `response_item/message` entirely — `event_msg` is the canonical source for text messages. `response_item` is only needed for `function_call`, `function_call_output`, and `reasoning` which have no `event_msg` equivalent.

## Codex Date-Tree Watching

Codex stores sessions in `sessions/YYYY/MM/DD/` date directories. A new day creates a new directory that didn't exist when the watcher started. The watcher must dynamically add new directories to fsnotify when `Create` events fire on directories, not just files.

## macOS sed

- `sed -i ''` (empty string for backup suffix) is required on macOS
- `cat -A` doesn't exist on macOS — use `cat -vet` for visible whitespace
- Always use `/g` flag for global replacement — default is first match per line only
