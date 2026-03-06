# peek

Observe and inspect AI agent sessions in real-time. Tail Claude Code and Codex CLI sessions from another terminal, see every message, tool call, and thinking block as they happen.

## Install

### GitHub with npm or pnpm

Install directly from GitHub without publishing to npm:

```sh
npm install -g github:bskyn/peek#main
# or
pnpm add -g github:bskyn/peek#main
```

If you install a tagged release such as `#v0.1.0`, the installer downloads the matching GitHub Release asset. If you install from a branch or commit, it falls back to `go build`, so Go 1.24+ must be installed.

### Homebrew

```sh
brew install bskyn/tap/peek
```

### Go

```sh
go install github.com/bskyn/peek/cmd/peek@latest
```

### From source

```sh
git clone https://github.com/bskyn/peek.git
cd peek
make build
./bin/peek --version
```

### Agent bootstrap

If you want Claude Code or Codex CLI to install `peek` for you, this is the shortest working sequence:

```sh
npm install -g github:bskyn/peek#main
peek --version
```

## Usage

### Tail a Claude session

Start Claude Code in one terminal, then in another:

```sh
# Auto-discover the latest active session
peek claude

# Tail a specific session by ID
peek claude 75c5194d-ea16-4b91-99cf-3d321d111a51
```

Events stream in real-time with sequential numbering:

```
  [1]  14:32:05  User
     What files are in /tmp?

  [2]  14:32:06  Thinking (142 tokens)
     let me look at the files in /tmp...

  [3]  14:32:06  Claude
     Let me check that for you.

  [4]  14:32:07  Tool: Bash
     > {"command":"ls /tmp"}

  [5]  14:32:08  Result
     file1.txt
     file2.txt
     ... 10 more lines

  [6]  14:32:09  Claude
     Here are the files in /tmp: ...
```

### Tail a Codex session

Start the Codex CLI in one terminal, then in another:

```sh
# Auto-discover the latest active session
peek codex

# Tail a specific session by UUID
peek codex 019cc0a5-6911-7123-b2ff-a4848ccd6e79

# Replay from the beginning
peek codex --replay
```

Codex sessions are discovered from `~/.codex/sessions/`. Set `CODEX_HOME` to override the base directory.

### List stored sessions

```sh
peek sessions list
```

### Replay a session from the beginning

By default, tailing resumes from where you last left off. Use `--replay` to start from the beginning and see the full conversation history:

```sh
peek claude --replay
peek codex --replay
```

### Options

```sh
# Custom database path
peek --db-path /path/to/data.db claude

# Verbose mode (show parse errors, cursor info)
peek --verbose claude
```

## How it works

peek reads session files from Claude Code (`~/.claude/projects/`) and Codex CLI (`~/.codex/sessions/`) and tails them in real-time using filesystem notifications. Each JSONL event is parsed, normalized into a canonical event model, rendered to the terminal, and persisted to a local SQLite database.

Sessions are resumable -- if you stop and restart, it picks up where it left off without duplicating events.

## Development

```sh
# Build
make build

# Run tests
make test

# Install the pinned linter binary, then lint
make lint-install
make lint

# Run from source
make run ARGS="claude"
make run ARGS="codex"
```

## License

MIT
