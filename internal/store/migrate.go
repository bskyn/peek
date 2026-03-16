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
	project_path TEXT NOT NULL DEFAULT '',
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

CREATE TABLE IF NOT EXISTS workspace_bootstrap_states (
	workspace_id TEXT PRIMARY KEY REFERENCES workspaces(id),
	fingerprint TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'pending',
	last_error TEXT NOT NULL DEFAULT '',
	started_at TEXT NOT NULL DEFAULT '',
	finished_at TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_workspace_bootstrap_states_status
	ON workspace_bootstrap_states(status, updated_at);

CREATE TABLE IF NOT EXISTS companion_service_states (
	runtime_id TEXT NOT NULL REFERENCES managed_runtimes(id),
	workspace_id TEXT NOT NULL REFERENCES workspaces(id),
	service_name TEXT NOT NULL,
	role TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'stopped',
	pid INTEGER NOT NULL DEFAULT 0,
	target_url TEXT NOT NULL DEFAULT '',
	last_error TEXT NOT NULL DEFAULT '',
	started_at TEXT NOT NULL DEFAULT '',
	ready_at TEXT NOT NULL DEFAULT '',
	stopped_at TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL,
	PRIMARY KEY (runtime_id, service_name)
);

CREATE INDEX IF NOT EXISTS idx_companion_service_states_runtime
	ON companion_service_states(runtime_id, updated_at);

CREATE TABLE IF NOT EXISTS checkout_leases (
	checkout_path TEXT PRIMARY KEY,
	runtime_id TEXT NOT NULL REFERENCES managed_runtimes(id),
	workspace_id TEXT NOT NULL REFERENCES workspaces(id),
	claimed_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_checkout_leases_runtime
	ON checkout_leases(runtime_id, updated_at);

CREATE TABLE IF NOT EXISTS detached_companion_runtimes (
	runtime_id TEXT PRIMARY KEY REFERENCES managed_runtimes(id),
	active_workspace_id TEXT NOT NULL DEFAULT '',
	owner_session_id TEXT NOT NULL DEFAULT '',
	config_source TEXT NOT NULL DEFAULT '',
	phase TEXT NOT NULL DEFAULT 'idle',
	message TEXT NOT NULL DEFAULT '',
	browser_path_prefix TEXT NOT NULL DEFAULT '',
	browser_target_url TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS port_leases (
	runtime_id TEXT NOT NULL REFERENCES managed_runtimes(id),
	service_name TEXT NOT NULL,
	host TEXT NOT NULL DEFAULT '127.0.0.1',
	port INTEGER NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY (runtime_id, service_name),
	UNIQUE(host, port)
);

CREATE INDEX IF NOT EXISTS idx_port_leases_runtime
	ON port_leases(runtime_id, updated_at);
`

// migrations that add columns to existing tables. Each is idempotent —
// SQLite returns "duplicate column name" if the column already exists,
// which we silently ignore.
var alterMigrations = []string{
	`ALTER TABLE sessions ADD COLUMN project_path TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE workspaces ADD COLUMN project_path TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE workspaces ADD COLUMN is_root INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE managed_runtimes ADD COLUMN project_path TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE companion_service_states ADD COLUMN pid INTEGER NOT NULL DEFAULT 0`,
}

var postAlterMigrations = []string{
	`UPDATE managed_runtimes
	    SET project_path = COALESCE(
	    	NULLIF((
	    		SELECT project_path
	    		  FROM workspaces
	    		 WHERE id = managed_runtimes.root_workspace_id
	    	), ''),
	    	NULLIF((
	    		SELECT project_path
	    		  FROM workspaces
	    		 WHERE id = managed_runtimes.active_workspace_id
	    	), ''),
	    	project_path
	    )
	  WHERE project_path = ''`,
	`CREATE INDEX IF NOT EXISTS idx_managed_runtimes_project ON managed_runtimes(project_path, updated_at)`,
}

func migrate(db *sql.DB) error {
	if err := applyAlterMigrations(db, true); err != nil {
		return err
	}
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	if err := applyAlterMigrations(db, false); err != nil {
		return err
	}
	for _, stmt := range postAlterMigrations {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func applyAlterMigrations(db *sql.DB, ignoreMissingTable bool) error {
	for _, stmt := range alterMigrations {
		if _, err := db.Exec(stmt); err != nil {
			// Ignore duplicate columns on repeat runs and missing tables during the
			// pre-schema pass, where older installs may not have every table yet.
			if isDuplicateColumn(err) || (ignoreMissingTable && isMissingTable(err)) {
				continue
			}
			return err
		}
	}
	return nil
}

func isDuplicateColumn(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate column")
}

func isMissingTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table")
}
