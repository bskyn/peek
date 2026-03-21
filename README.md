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

Managed sessions workflow: keep Claude or Codex in one terminal, branch or switch workspaces from another terminal, and inspect everything in the local viewer.

Before the first managed run, create a repo-local manifest. Peek resolves the repo root automatically and writes `peek.runtime.json` there.

```sh
# Preview the generated manifest
peek manifest create --stdout

# Write ./peek.runtime.json
peek manifest create

# Monorepo: choose the app explicitly
peek manifest create --service apps/core
```

`peek manifest create` supports:

- single-package JS/TS repos with a root `package.json` and `dev` script
- monorepos declared through root workspaces or `pnpm-workspace.yaml`
- `pnpm`, `npm`, `yarn`, and `bun`

If the scaffold is close but not perfect, edit `peek.runtime.json` and rerun. Managed runs require this file.

Start a managed session:

```sh
peek run claude
peek run codex

# Pass reusable provider flags only
peek run claude -- --model sonnet
peek run codex -- --model o4-mini

# Reattach a stopped runtime
peek run claude --runtime-id rt-abc123
```

Use a second terminal to inspect and control the live runtime:

```sh

peek ws list
peek ws branch ws-root 5
peek ws switch ws-abc123
peek ws freeze ws-abc123
peek ws merge ws-abc123
peek ws cool ws-abc123
peek ws status ws-abc123
peek ws delete ws-abc123
peek ws prune
```

Notes:

- `peek run` keeps ownership of the live provider terminal.
- The first runtime reuses the current checkout; a second runtime in the same repo gets an isolated root worktree.
- Branching captures code state around tool execution so you can iterate from earlier points in the chat.
- Companion app services are owned by the managed runtime and stay aligned with the active workspace.

### Session management

Use these commands to inspect, reload, or delete stored sessions:

```sh
peek sessions list

# Delete one session
peek sessions delete aa961bad-c727-4479-ac42-8d1db8bdf261

# Delete everything
peek sessions delete --all

# Reload Claude and Codex sessions from disk
peek sessions load --all
```

### Viewer mode

Peek starts the local browser viewer by default for both managed runs and passive monitoring.

```sh
# Passive monitoring
peek claude
peek codex

# Monitor a specific session
peek claude 75c5194d-ea16-4b91-99cf-3d321d111a51
peek codex 019cc0a5-6911-7123-b2ff-a4848ccd6e79

# Replay from the beginning
peek claude --replay
peek codex --replay

# Disable the viewer or choose a port
peek claude --no-web
peek codex --open-browser=false --web-port 4317
```

Managed-mode viewer behavior:

- each managed run gets a runtime-scoped route
- the session list is scoped to that runtime lineage
- the app surface follows the active managed workspace

### Development

```sh
# Install frontend dependencies once
make install

# Build CLI + embedded web app
make build

# Run tests
make test

# Lint
make lint

# Run from source
make run ARGS="run claude"
make run ARGS="claude"
```

## License

MIT
