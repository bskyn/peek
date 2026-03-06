package store

import "database/sql"

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
`

func migrate(db *sql.DB) error {
	_, err := db.Exec(schema)
	return err
}
