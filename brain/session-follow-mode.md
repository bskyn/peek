# Session Follow-Mode

## How It Works

`followSessions` loop in `cli/claude.go`:
1. Tails current session via `tailSession`
2. Runs `watchForNewSession` goroutine in parallel
3. When new session detected → cancel tailer, print divider, start new tailer
4. Terminal history preserved across session switches

## Watcher Design (critical gotchas)

### Scope to project directory
The watcher must only watch `~/.claude/projects/{encoded-project-key}/` — the same directory as the current session. Global mtime scanning (`DiscoverByMtime`) will flip-flop between multiple active Claude sessions, causing infinite "new session started" spam.

### Detect new files, not modified files
`/clear` and `/new` create a **new** JSONL file. The watcher snapshots existing files at startup and only triggers on files that didn't exist before. Watching for `fsnotify.Create` (not `Write`) + polling fallback.

### history.jsonl is unreliable for change detection
- May not update immediately on `/new` or `/clear`
- `discoverFromHistory` falls back to older entries if the new session's JSONL file doesn't exist yet — returns the OLD session, masking the change
- Use direct project-dir file scanning instead

## Renderer

- `RenderSessionBanner()` — yellow bold session header, dim file/project info
- `RenderNewSessionDivider()` — yellow `───` bordered separator, printed once per switch
