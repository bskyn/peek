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

Managed mode only accepts reusable provider options after `--`. Do not pass an initial prompt or provider subcommand there, because Peek needs to relaunch the same interactive session shape on branch/switch.

Managed mode creates a workspace, tracks checkpoints around tool execution, and enables branching, freezing, switching, and merging.

`peek run` keeps ownership of the live terminal. When you branch or switch, use a second terminal to send the control request while the original `peek run` process stays in charge of the provider CLI.

### Branching and workspaces

Branch from any event sequence to explore alternate paths. Run `peek run ...` in one terminal, then issue branch/switch requests from another terminal. The source workspace freezes and the live managed terminal relaunches into the selected workspace:

```sh
# Terminal A: keep the live managed session open
peek run claude

# Terminal B: inspect and control the live managed runtime

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
- **Branch from a later card**: resolves to the latest completed post-tool snapshot at or before the selected sequence.
- **Freeze/switch**: the source workspace freezes on branch. `peek workspace switch` freezes the currently active sibling and hands the live managed terminal back to the target workspace in place.
- **Merge**: merges the branch's current worktree state into the parent workspace. On conflict, Peek stops and reports the target worktree path for manual resolution.
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
