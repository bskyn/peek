package store

import (
	"database/sql"
	"strings"
)

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
	id TEXT PRIMARY KEY,
	source TEXT NOT NULL,
	project_path TEXT NOT NULL DEFAULT '',
	source_session_id TEXT NOT NULL DEFAULT '',
	parent_session_id TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sessions_source_session ON sessions(source_session_id);

CREATE TABLE IF NOT EXISTS events (
	id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL REFERENCES sessions(id),
	ts TEXT NOT NULL,
	seq INTEGER NOT NULL,
	type TEXT NOT NULL,
	role TEXT NOT NULL DEFAULT '',
	parent_event_id TEXT,
	payload_json TEXT NOT NULL DEFAULT '{}',
	UNIQUE(session_id, seq)
);

CREATE INDEX IF NOT EXISTS idx_events_session_seq ON events(session_id, seq);

CREATE TABLE IF NOT EXISTS cursors (
	path TEXT PRIMARY KEY,
	byte_offset INTEGER NOT NULL DEFAULT 0,
	session_id TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS workspaces (
	id TEXT PRIMARY KEY,
	parent_workspace_id TEXT REFERENCES workspaces(id),
	status TEXT NOT NULL DEFAULT 'active',
	project_path TEXT NOT NULL DEFAULT '',
	worktree_path TEXT NOT NULL DEFAULT '',
	git_ref TEXT NOT NULL DEFAULT '',
	branch_from_seq INTEGER,
	sibling_ordinal INTEGER NOT NULL DEFAULT 0,
	is_root INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_workspaces_parent ON workspaces(parent_workspace_id);
CREATE INDEX IF NOT EXISTS idx_workspaces_status ON workspaces(status);

CREATE TABLE IF NOT EXISTS workspace_sessions (
	workspace_id TEXT NOT NULL REFERENCES workspaces(id),
	session_id TEXT NOT NULL REFERENCES sessions(id),
	created_at TEXT NOT NULL,
	PRIMARY KEY (workspace_id, session_id)
);

CREATE TABLE IF NOT EXISTS checkpoints (
	id TEXT PRIMARY KEY,
	workspace_id TEXT NOT NULL REFERENCES workspaces(id),
	session_id TEXT NOT NULL REFERENCES sessions(id),
	seq INTEGER NOT NULL,
	kind TEXT NOT NULL,
	git_ref TEXT NOT NULL,
	created_at TEXT NOT NULL,
	UNIQUE(workspace_id, seq, kind)
);

CREATE INDEX IF NOT EXISTS idx_checkpoints_workspace_seq ON checkpoints(workspace_id, seq);

CREATE TABLE IF NOT EXISTS branch_path_segments (
	workspace_id TEXT PRIMARY KEY REFERENCES workspaces(id),
	parent_workspace_id TEXT REFERENCES workspaces(id),
	branch_seq INTEGER NOT NULL DEFAULT 0,
	ordinal INTEGER NOT NULL DEFAULT 0,
	depth INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS managed_runtimes (
	id TEXT PRIMARY KEY,
	root_workspace_id TEXT NOT NULL REFERENCES workspaces(id),
	active_workspace_id TEXT NOT NULL REFERENCES workspaces(id),
	active_session_id TEXT NOT NULL REFERENCES sessions(id),
	source TEXT NOT NULL DEFAULT '',
	launch_args_json TEXT NOT NULL DEFAULT '[]',
	status TEXT NOT NULL DEFAULT 'running',
	heartbeat_at TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_managed_runtimes_root ON managed_runtimes(root_workspace_id);
CREATE INDEX IF NOT EXISTS idx_managed_runtimes_status ON managed_runtimes(status);

CREATE TABLE IF NOT EXISTS managed_runtime_requests (
	id TEXT PRIMARY KEY,
	runtime_id TEXT NOT NULL REFERENCES managed_runtimes(id),
	kind TEXT NOT NULL,
	source_workspace_id TEXT,
	branch_from_seq INTEGER,
	target_workspace_id TEXT,
	status TEXT NOT NULL DEFAULT 'pending',
	response_workspace_id TEXT NOT NULL DEFAULT '',
	response_session_id TEXT NOT NULL DEFAULT '',
	response_worktree_path TEXT NOT NULL DEFAULT '',
	response_git_ref TEXT NOT NULL DEFAULT '',
	error TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_managed_runtime_requests_runtime_status
	ON managed_runtime_requests(runtime_id, status, created_at);
`

// migrations that add columns to existing tables. Each is idempotent —
// SQLite returns "duplicate column name" if the column already exists,
// which we silently ignore.
var alterMigrations = []string{
	`ALTER TABLE workspaces ADD COLUMN is_root INTEGER NOT NULL DEFAULT 0`,
}

func migrate(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	for _, stmt := range alterMigrations {
		if _, err := db.Exec(stmt); err != nil {
			// Ignore "duplicate column name" — means migration already applied
			if !isDuplicateColumn(err) {
				return err
			}
		}
	}
	return nil
}

func isDuplicateColumn(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate column")
}
