package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"

	"github.com/bskyn/peek/internal/event"
)

// Store provides persistence for sessions and events.
type Store struct {
	db *sql.DB
}

// AmbiguousSessionIDError reports that a raw source session ID matched multiple stored sessions.
type AmbiguousSessionIDError struct {
	Input   string
	Matches []string
}

func (e *AmbiguousSessionIDError) Error() string {
	return fmt.Sprintf("session %q matches multiple stored sessions", e.Input)
}

// SessionSummary is the read model used by the viewer.
type SessionSummary struct {
	ID              string    `json:"id"`
	Source          string    `json:"source"`
	ProjectPath     string    `json:"project_path"`
	SourceSessionID string    `json:"source_session_id"`
	ParentSessionID string    `json:"parent_session_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	EventCount      int64     `json:"event_count"`
}

// ChildSessionSummary is a thin child-session read model.
type ChildSessionSummary = SessionSummary

// SessionDetail returns the selected session plus its branch metadata.
type SessionDetail struct {
	Session       SessionSummary        `json:"session"`
	RootSession   SessionSummary        `json:"root_session"`
	ChildSessions []ChildSessionSummary `json:"child_sessions"`
}

// EventPage is a paginated event response.
type EventPage struct {
	Events        []event.Event `json:"events"`
	HasMore       bool          `json:"has_more"`
	NextAfterSeq  int64         `json:"next_after_seq,omitempty"`
	NextBeforeSeq int64         `json:"next_before_seq,omitempty"`
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

// CreateSession inserts or updates a session.
func (s *Store) CreateSession(sess event.Session) error {
	_, err := s.db.Exec(
		`INSERT INTO sessions (id, source, project_path, source_session_id, parent_session_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   source = excluded.source,
		   project_path = CASE
		     WHEN excluded.project_path != '' THEN excluded.project_path
		     ELSE sessions.project_path
		   END,
		   source_session_id = CASE
		     WHEN excluded.source_session_id != '' THEN excluded.source_session_id
		     ELSE sessions.source_session_id
		   END,
		   parent_session_id = COALESCE(sessions.parent_session_id, excluded.parent_session_id),
		   updated_at = CASE
		     WHEN sessions.updated_at > excluded.updated_at THEN sessions.updated_at
		     ELSE excluded.updated_at
		   END`,
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

// ListSessions returns all sessions ordered by most recent activity descending.
func (s *Store) ListSessions() ([]event.Session, error) {
	rows, err := s.db.Query(
		`SELECT id, source, project_path, source_session_id, COALESCE(parent_session_id, ''), created_at, updated_at
		 FROM sessions ORDER BY updated_at DESC, created_at DESC`)
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
	_, err := s.AppendEvents([]event.Event{ev})
	return err
}

// InsertEvents inserts multiple events in a transaction.
func (s *Store) InsertEvents(events []event.Event) error {
	_, err := s.AppendEvents(events)
	return err
}

// AppendEvents inserts multiple events and returns the events that were newly persisted.
func (s *Store) AppendEvents(events []event.Event) ([]event.Event, error) {
	if len(events) == 0 {
		return nil, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	stmt, err := tx.Prepare(
		`INSERT OR IGNORE INTO events (id, session_id, ts, seq, type, role, parent_event_id, payload_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	inserted := make([]event.Event, 0, len(events))
	latestBySession := make(map[string]time.Time)

	for _, ev := range events {
		result, err := stmt.Exec(
			ev.ID, ev.SessionID, ev.Timestamp.Format(time.RFC3339Nano), ev.Seq, string(ev.Type),
			ev.Role, nilIfEmpty(ev.ParentEventID), string(ev.PayloadJSON),
		)
		if err != nil {
			return nil, err
		}

		rows, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if rows == 0 {
			continue
		}

		inserted = append(inserted, ev)
		if latest, ok := latestBySession[ev.SessionID]; !ok || ev.Timestamp.After(latest) {
			latestBySession[ev.SessionID] = ev.Timestamp
		}
	}

	for sessionID, ts := range latestBySession {
		if err := touchSessionTx(tx, sessionID, ts); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return inserted, nil
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

// ListSessionSummaries returns all sessions ordered by most recent activity.
func (s *Store) ListSessionSummaries() ([]SessionSummary, error) {
	rows, err := s.db.Query(sessionSummarySelect + `
		GROUP BY s.id, s.source, s.project_path, s.source_session_id, s.parent_session_id, s.created_at, s.updated_at
		ORDER BY s.updated_at DESC, s.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	summaries := make([]SessionSummary, 0)
	for rows.Next() {
		summary, err := scanSessionSummary(rows)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	return summaries, rows.Err()
}

// GetSessionSummary loads one session summary.
func (s *Store) GetSessionSummary(id string) (SessionSummary, error) {
	row := s.db.QueryRow(sessionSummarySelect+`
		AND s.id = ?
		GROUP BY s.id, s.source, s.project_path, s.source_session_id, s.parent_session_id, s.created_at, s.updated_at`, id)
	summary, err := scanSessionSummary(row)
	if err != nil {
		return SessionSummary{}, err
	}
	return summary, nil
}

// ListChildSessionSummaries returns child sessions for a root session.
func (s *Store) ListChildSessionSummaries(parentSessionID string) ([]ChildSessionSummary, error) {
	rows, err := s.db.Query(sessionSummarySelect+`
		AND s.parent_session_id = ?
		GROUP BY s.id, s.source, s.project_path, s.source_session_id, s.parent_session_id, s.created_at, s.updated_at
		ORDER BY s.created_at ASC, s.id ASC`, parentSessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	children := make([]ChildSessionSummary, 0)
	for rows.Next() {
		child, err := scanSessionSummary(rows)
		if err != nil {
			return nil, err
		}
		children = append(children, child)
	}
	return children, rows.Err()
}

// GetSessionDetail returns a session plus its branch context.
func (s *Store) GetSessionDetail(id string) (SessionDetail, error) {
	sessionSummary, err := s.GetSessionSummary(id)
	if err != nil {
		return SessionDetail{}, err
	}

	rootID := sessionSummary.ID
	if sessionSummary.ParentSessionID != "" {
		rootID = sessionSummary.ParentSessionID
	}

	rootSummary := sessionSummary
	if rootID != sessionSummary.ID {
		rootSummary, err = s.GetSessionSummary(rootID)
		if err != nil {
			return SessionDetail{}, err
		}
	}

	children, err := s.ListChildSessionSummaries(rootID)
	if err != nil {
		return SessionDetail{}, err
	}

	return SessionDetail{
		Session:       sessionSummary,
		RootSession:   rootSummary,
		ChildSessions: children,
	}, nil
}

// GetEventPage returns events for a session after the given sequence.
func (s *Store) GetEventPage(sessionID string, afterSeq int64, limit int) (EventPage, error) {
	if limit <= 0 {
		limit = 200
	}

	rows, err := s.db.Query(
		`SELECT id, session_id, ts, seq, type, role, COALESCE(parent_event_id, ''), payload_json
		 FROM events
		 WHERE session_id = ? AND seq > ?
		 ORDER BY seq
		 LIMIT ?`,
		sessionID, afterSeq, limit+1,
	)
	if err != nil {
		return EventPage{}, err
	}
	defer rows.Close()

	events := make([]event.Event, 0, limit)
	var nextAfterSeq int64
	for rows.Next() {
		ev, err := scanEvent(rows)
		if err != nil {
			return EventPage{}, err
		}
		if len(events) == limit {
			return EventPage{
				Events:       events,
				HasMore:      true,
				NextAfterSeq: nextAfterSeq,
			}, nil
		}
		nextAfterSeq = ev.Seq
		events = append(events, ev)
	}
	if err := rows.Err(); err != nil {
		return EventPage{}, err
	}

	return EventPage{
		Events:       events,
		HasMore:      false,
		NextAfterSeq: nextAfterSeq,
	}, nil
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

// ResolveSessionID resolves either an internal session ID or a raw source session ID.
func (s *Store) ResolveSessionID(id string) (string, error) {
	if id == "" {
		return "", sql.ErrNoRows
	}

	ok, err := s.HasSession(id)
	if err != nil {
		return "", err
	}
	if ok {
		return id, nil
	}

	rows, err := s.db.Query(
		`SELECT id
		 FROM sessions
		 WHERE source_session_id = ?
		 ORDER BY updated_at DESC, created_at DESC, id ASC`,
		id,
	)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	matches := make([]string, 0, 1)
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			return "", err
		}
		matches = append(matches, sessionID)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}

	switch len(matches) {
	case 0:
		return "", sql.ErrNoRows
	case 1:
		return matches[0], nil
	default:
		return "", &AmbiguousSessionIDError{Input: id, Matches: matches}
	}
}

// DeleteSession removes a session and its stored events/cursors.
func (s *Store) DeleteSession(id string) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.Exec(`DELETE FROM events WHERE session_id = ?`, id); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`DELETE FROM cursors WHERE session_id = ?`, id); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`UPDATE sessions SET parent_session_id = NULL WHERE parent_session_id = ?`, id); err != nil {
		return false, err
	}

	result, err := tx.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}

	if err := tx.Commit(); err != nil {
		return false, err
	}

	return rows > 0, nil
}

// DeleteAllSessions removes all stored sessions, events, and cursors.
func (s *Store) DeleteAllSessions() (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var count int64
	if err := tx.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil {
		return 0, err
	}

	if _, err := tx.Exec(`DELETE FROM cursors`); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM events`); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM sessions`); err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}

	return count, nil
}

// DeleteSessionsBySource removes all stored sessions, events, and cursors for one source.
func (s *Store) DeleteSessionsBySource(source string) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var count int64
	if err := tx.QueryRow(`SELECT COUNT(*) FROM sessions WHERE source = ?`, source).Scan(&count); err != nil {
		return 0, err
	}

	if _, err := tx.Exec(`DELETE FROM cursors WHERE session_id IN (SELECT id FROM sessions WHERE source = ?)`, source); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM events WHERE session_id IN (SELECT id FROM sessions WHERE source = ?)`, source); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`UPDATE sessions SET parent_session_id = NULL WHERE parent_session_id IN (SELECT id FROM sessions WHERE source = ?)`, source); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE source = ?`, source); err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}

	return count, nil
}

func nilIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

const sessionSummarySelect = `
SELECT
	s.id,
	s.source,
	s.project_path,
	s.source_session_id,
	COALESCE(s.parent_session_id, ''),
	s.created_at,
	s.updated_at,
	COUNT(e.id)
FROM sessions s
LEFT JOIN events e ON e.session_id = s.id
WHERE 1 = 1`

type sessionSummaryScanner interface {
	Scan(dest ...any) error
}

func scanSessionSummary(scanner sessionSummaryScanner) (SessionSummary, error) {
	var summary SessionSummary
	var createdAt string
	var updatedAt string
	err := scanner.Scan(
		&summary.ID,
		&summary.Source,
		&summary.ProjectPath,
		&summary.SourceSessionID,
		&summary.ParentSessionID,
		&createdAt,
		&updatedAt,
		&summary.EventCount,
	)
	if err != nil {
		return SessionSummary{}, err
	}
	summary.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	summary.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return summary, nil
}

type eventScanner interface {
	Scan(dest ...any) error
}

func scanEvent(scanner eventScanner) (event.Event, error) {
	var ev event.Event
	var ts string
	var payload string
	if err := scanner.Scan(&ev.ID, &ev.SessionID, &ts, &ev.Seq, &ev.Type, &ev.Role, &ev.ParentEventID, &payload); err != nil {
		return event.Event{}, err
	}
	ev.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
	ev.PayloadJSON = []byte(payload)
	return ev, nil
}

func touchSessionTx(tx *sql.Tx, sessionID string, ts time.Time) error {
	if sessionID == "" || ts.IsZero() {
		return nil
	}
	_, err := tx.Exec(
		`UPDATE sessions
		 SET updated_at = CASE
		   WHEN updated_at > ? THEN updated_at
		   ELSE ?
		 END
		 WHERE id = ?`,
		ts.Format(time.RFC3339Nano),
		ts.Format(time.RFC3339Nano),
		sessionID,
	)
	return err
}

// HasSession reports whether a session exists.
func (s *Store) HasSession(id string) (bool, error) {
	_, err := s.GetSession(id)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}
