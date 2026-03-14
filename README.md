# Peek

Observe and inspect AI agent sessions in real-time. Launch managed sessions with branching, checkpoints, and merge-back, or monitor existing Claude and Codex CLI sessions from another terminal.

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

### Managed sessions

Launch a Peek-managed agent session with full workspace lifecycle control:

```sh
# Launch a managed Claude session
peek run claude

# Launch a managed Codex session
peek run codex

# Pass extra arguments to the underlying CLI
peek run claude -- --model sonnet
peek run codex -- --model o4-mini

# Disable the web viewer
peek run claude --no-web
```

Managed mode creates a workspace, tracks checkpoints around tool execution, and enables branching, freezing, switching, and merging.

### Branching and workspaces

Branch from any event sequence to explore alternate paths. The source workspace freezes and a new workspace materializes from the pre-tool code snapshot:

```sh
# List workspaces
peek workspace list

# Branch from workspace ws-abc123 at event sequence 5
peek workspace branch ws-abc123 5

# Switch to a workspace (re-materializes if frozen)
peek workspace switch ws-def456

# Freeze a workspace
peek workspace freeze ws-abc123

# Merge a branch back into its parent
peek workspace merge ws-def456

# Dematerialize a frozen workspace to ref-only storage
peek workspace cool ws-abc123

# Show workspace details, lineage, and children
peek workspace status ws-abc123
```

The `workspace` command is aliased as `ws` for convenience:

```sh
peek ws list
peek ws branch ws-abc123 5
peek ws status ws-abc123
```

#### Branch semantics

- **Branch from a `tool_call`**: resolves to the pre-result code snapshot, so the child workspace starts from the state before the tool modified files.
- **Freeze/switch**: the source workspace freezes on branch. Switch back re-materializes it from its git ref.
- **Merge**: merges branch code into the parent workspace. On conflict, Peek stops and reports the target worktree path for manual resolution.
- **Cool**: dematerializes inactive worktrees down to hidden git refs. Switch re-materializes on demand.

### Monitoring existing sessions

Monitor sessions started outside of Peek. Start the agent in one terminal, then in another:

<details>
<summary>Passive monitoring commands</summary>

```sh
# Auto-discover the latest active Claude or Codex session
peek claude
peek codex

# Monitor a specific session by ID
peek claude 75c5194d-ea16-4b91-99cf-3d321d111a51
peek codex 019cc0a5-6911-7123-b2ff-a4848ccd6e79

# Reload all sessions from disk
peek claude load --all
peek codex load --all

# Replay from the beginning
peek claude --replay
peek codex --replay

# Disable the web viewer
peek claude --no-web
peek codex --no-web

# Custom viewer port
peek claude --replay --web-port 4317
peek codex --open-browser=false --web-port 4317
```

Codex sessions are discovered from `~/.codex/sessions/`. Set `CODEX_HOME` to override the base directory.

</details>

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

### Session management

```sh
peek sessions list

# Delete one session by ID
peek sessions delete aa961bad-c727-4479-ac42-8d1db8bdf261

# Delete all sessions
peek sessions delete --all

# Reload all Claude and Codex sessions from disk
peek sessions load --all
```

## How it works

Peek has two modes:

- **Managed mode** (`peek run claude|codex`) launches the native CLI in a Peek-owned workspace. Peek captures pre-tool and post-tool code snapshots as hidden git refs, enabling branching from any point in the conversation. Workspaces can be frozen, switched, merged, and cooled to ref-only storage.

- **Passive mode** (`peek claude|codex`) reads session files from Claude and Codex CLI and monitors them in real-time using filesystem notifications. Each JSONL event is parsed, normalized into a canonical event model, rendered to the terminal, and persisted to a local SQLite database.

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
make run ARGS="run claude"
make run ARGS="claude"
```

## License

MIT
