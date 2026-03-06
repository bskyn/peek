package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bskyn/peek/internal/event"
	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

// Store provides persistence for sessions and events.
type Store struct {
	db *sql.DB
}

// Open opens or creates a SQLite database at the given path.
func Open(dbPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// CreateSession inserts a new session. Duplicate IDs are silently ignored.
func (s *Store) CreateSession(sess event.Session) error {
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO sessions (id, source, project_path, source_session_id, parent_session_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sess.ID, sess.Source, sess.ProjectPath, sess.SourceSessionID, nilIfEmpty(sess.ParentSessionID),
		sess.CreatedAt.Format(time.RFC3339Nano), sess.UpdatedAt.Format(time.RFC3339Nano),
	)
	return err
}

// GetSession retrieves a session by ID.
func (s *Store) GetSession(id string) (*event.Session, error) {
	row := s.db.QueryRow(
		`SELECT id, source, project_path, source_session_id, COALESCE(parent_session_id, ''), created_at, updated_at
		 FROM sessions WHERE id = ?`, id)

	var sess event.Session
	var createdAt, updatedAt string
	err := row.Scan(&sess.ID, &sess.Source, &sess.ProjectPath, &sess.SourceSessionID, &sess.ParentSessionID, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	sess.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	sess.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return &sess, nil
}

// ListSessions returns all sessions ordered by creation time descending.
func (s *Store) ListSessions() ([]event.Session, error) {
	rows, err := s.db.Query(
		`SELECT id, source, project_path, source_session_id, COALESCE(parent_session_id, ''), created_at, updated_at
		 FROM sessions ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []event.Session
	for rows.Next() {
		var sess event.Session
		var createdAt, updatedAt string
		if err := rows.Scan(&sess.ID, &sess.Source, &sess.ProjectPath, &sess.SourceSessionID, &sess.ParentSessionID, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		sess.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		sess.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		sessions = append(sessions, sess)
	}
	return sessions, rows.Err()
}

// InsertEvent inserts a single event. Duplicate (session_id, seq) is ignored.
func (s *Store) InsertEvent(ev event.Event) error {
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO events (id, session_id, ts, seq, type, role, parent_event_id, payload_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		ev.ID, ev.SessionID, ev.Timestamp.Format(time.RFC3339Nano), ev.Seq, string(ev.Type),
		ev.Role, nilIfEmpty(ev.ParentEventID), string(ev.PayloadJSON),
	)
	return err
}

// InsertEvents inserts multiple events in a transaction.
func (s *Store) InsertEvents(events []event.Event) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(
		`INSERT OR IGNORE INTO events (id, session_id, ts, seq, type, role, parent_event_id, payload_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, ev := range events {
		_, err := stmt.Exec(
			ev.ID, ev.SessionID, ev.Timestamp.Format(time.RFC3339Nano), ev.Seq, string(ev.Type),
			ev.Role, nilIfEmpty(ev.ParentEventID), string(ev.PayloadJSON),
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// GetEvents returns all events for a session ordered by sequence number.
func (s *Store) GetEvents(sessionID string) ([]event.Event, error) {
	rows, err := s.db.Query(
		`SELECT id, session_id, ts, seq, type, role, COALESCE(parent_event_id, ''), payload_json
		 FROM events WHERE session_id = ? ORDER BY seq`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []event.Event
	for rows.Next() {
		var ev event.Event
		var ts, payloadStr string
		if err := rows.Scan(&ev.ID, &ev.SessionID, &ts, &ev.Seq, &ev.Type, &ev.Role, &ev.ParentEventID, &payloadStr); err != nil {
			return nil, err
		}
		ev.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
		ev.PayloadJSON = []byte(payloadStr)
		events = append(events, ev)
	}
	return events, rows.Err()
}

// Cursor represents a file tailing position.
type Cursor struct {
	Path       string
	ByteOffset int64
	SessionID  string
}

// GetCursor retrieves a cursor by file path.
func (s *Store) GetCursor(path string) (*Cursor, error) {
	row := s.db.QueryRow(`SELECT path, byte_offset, session_id FROM cursors WHERE path = ?`, path)
	var c Cursor
	err := row.Scan(&c.Path, &c.ByteOffset, &c.SessionID)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// SaveCursor upserts a cursor.
func (s *Store) SaveCursor(c Cursor) error {
	_, err := s.db.Exec(
		`INSERT INTO cursors (path, byte_offset, session_id) VALUES (?, ?, ?)
		 ON CONFLICT(path) DO UPDATE SET byte_offset=excluded.byte_offset, session_id=excluded.session_id`,
		c.Path, c.ByteOffset, c.SessionID,
	)
	return err
}

// MaxSeq returns the highest sequence number for a session, or -1 if no events exist.
func (s *Store) MaxSeq(sessionID string) (int64, error) {
	row := s.db.QueryRow(`SELECT COALESCE(MAX(seq), -1) FROM events WHERE session_id = ?`, sessionID)
	var seq int64
	err := row.Scan(&seq)
	return seq, err
}

func nilIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
