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

## react-diff-view parseDiff

`createTwoFilesPatch` from the `diff` library produces a format that `react-diff-view`'s `parseDiff` cannot handle — it crashes reading `.changes` on undefined. Use `structuredPatch` instead and manually build the unified diff string with proper `--- a` / `+++ b` / `@@ -x,y +x,y @@` headers. Always wrap `parseDiff` in try/catch as a safety net.

## Codex apply_patch Payload Shape

Codex `apply_patch` tool calls are split by the Go parser into per-file events with `file_path`, `operation`, and `diff` fields directly on `payload_json` (not nested under `input`). This differs from Claude's Edit/Write which store `old_string`/`new_string` under `payload_json.input`. Web UI diff rendering must handle both paths.

## macOS sed

- `sed -i ''` (empty string for backup suffix) is required on macOS
- `cat -A` doesn't exist on macOS — use `cat -vet` for visible whitespace
- Always use `/g` flag for global replacement — default is first match per line only

## Cross-Provider Bug Auditing

When fixing a bug in one provider connector (Claude/Codex), proactively audit the other connector for the same class of issue. Cache token handling was broken in both Claude and Codex independently — discovered only because the user explicitly cross-checked after fixing one.

## Claude Streaming Usage Deduplication

Claude Code writes multiple assistant JSONL lines per streaming API response, each carrying cumulative (not incremental) usage. Summing all of them double/triple counts tokens (~2.9x observed). Deduplicate by `message_id`: only use the delta between consecutive usage reports for the same message.

## SSE + Fetch Race

When a React hook does an initial HTTP fetch and simultaneously opens an SSE stream, events can arrive via SSE while the fetch is still loading. The stream handler must merge (not overwrite) using a Map keyed by seq/id so both sources coexist without duplication or dropped events. Pattern: `setEvents((current) => mergeEvents(page.events, current))`.

## Model Pricing: Match Versioned IDs First

`hasModelPrefix` matches greedily — `"gpt-5"` matches `"gpt-5.4"`, and `"claude-opus-4"` matches `"claude-opus-4-6"`. Model pricing cases must be ordered most-specific-first to prevent sub-models from falling through to base model rates. Opus 4.6 ($5/$25) fell through to Opus 4.0 ($15/$75) — a 3x overcharge.

## Worktree Subdirectory Path Matching

When checking if a shell's cwd is "in" a worktree, use `strings.HasPrefix(cwd, worktreePath+"/")` — not exact equality. A user in `/repo/internal/cli` is still in the `/repo` worktree. The trailing `/` prevents false matches like `/repo-backup` matching `/repo`.
