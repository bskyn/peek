# Peek

Observe and inspect AI agent sessions in real-time. Monitors Claude and Codex CLI sessions from another terminal, see every message, tool call, and thinking block as they happen.

The CLI also starts a local browser viewer by default, so terminal streaming and the session timeline stay in sync.

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

## Usage

### Monitor a Claude session

Start Claude in one terminal, then in another:

```sh
# Auto-discover the latest active session
peek claude

# Monitor a specific session by ID
peek claude 75c5194d-ea16-4b91-99cf-3d321d111a51

# Reload every Claude session from disk
peek claude load --all

# Disable the web viewer and keep terminal output only
peek claude --no-web

# Replay from the beginning and serve the viewer on port 4317
peek claude --replay --web-port 4317
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

### Monitor a Codex session

Start the Codex CLI in one terminal, then in another:

```sh
# Auto-discover the latest active session
peek codex

# Monitor a specific session by UUID
peek codex 019cc0a5-6911-7123-b2ff-a4848ccd6e79

# Reload every Codex session from disk
peek codex load --all

# Replay from the beginning
peek codex --replay

# Keep terminal output only
peek codex --no-web

# Print the viewer URL but do not open a browser
peek codex --open-browser=false --web-port 4317

# Replay from the beginning and keep the viewer enabled on a fixed port
peek codex --replay --web-port 4317
```

Codex sessions are discovered from `~/.codex/sessions/`. Set `CODEX_HOME` to override the base directory.

### List stored sessions

```sh
peek sessions list

# Delete one session by ID
peek sessions delete aa961bad-c727-4479-ac42-8d1db8bdf261

# Delete all sessions
peek sessions delete --all

# Reload all Claude and Codex sessions from disk
peek sessions load --all
```

### Replay a session from the beginning

By default, tracing resumes from where you last left off. Use `--replay` to start from the beginning and see the full conversation history:

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

# Viewer controls
peek claude --no-web
peek codex --open-browser=false
peek claude --web-port 0

# Flags can be chained
peek claude --replay --web-port 4317
peek codex --replay --open-browser=false --web-port 4317
```

## How it works

Peek reads session files from Claude and Codex CLI and monitors them in real-time using filesystem notifications. Each JSONL event is parsed, normalized into a canonical event model, rendered to the terminal, and persisted to a local SQLite database.

## Development

```sh
# Install frontend dependencies once
make install

# Build the embedded web app and CLI
make build

# Run tests
make test

# Lint (installs the pinned golangci-lint version into ./bin if needed)
make lint

# Run from source
make run ARGS="claude"
make run ARGS="codex"
```

## License

MIT
