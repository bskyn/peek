package store

import (
	"database/sql"

	"github.com/bskyn/peek/internal/event"
	"github.com/bskyn/peek/internal/workspace"
)

// GetLatestWorkspaceSession returns the most recently linked session for a workspace.
func (s *Store) GetLatestWorkspaceSession(workspaceID string) (*event.Session, error) {
	row := s.db.QueryRow(
		`SELECT session_id
		   FROM workspace_sessions
		  WHERE workspace_id = ?
		  ORDER BY created_at DESC, session_id DESC
		  LIMIT 1`,
		workspaceID,
	)

	var sessionID string
	if err := row.Scan(&sessionID); err != nil {
		return nil, err
	}
	return s.GetSession(sessionID)
}

// GetEventBySeq returns one event at the given session sequence.
func (s *Store) GetEventBySeq(sessionID string, seq int64) (*event.Event, error) {
	row := s.db.QueryRow(
		`SELECT id, session_id, ts, seq, type, role, COALESCE(parent_event_id, ''), payload_json
		   FROM events
		  WHERE session_id = ? AND seq = ?`,
		sessionID,
		seq,
	)
	ev, err := scanEvent(row)
	if err != nil {
		return nil, err
	}
	return &ev, nil
}

// ListLineageWorkspaces returns the root workspace plus all descendants in one lineage.
func (s *Store) ListLineageWorkspaces(rootWorkspaceID string) ([]workspace.Workspace, error) {
	rows, err := s.db.Query(
		`WITH RECURSIVE lineage(id) AS (
			SELECT id FROM workspaces WHERE id = ?
			UNION ALL
			SELECT w.id
			  FROM workspaces w
			  JOIN lineage l ON w.parent_workspace_id = l.id
		)
		SELECT id, COALESCE(parent_workspace_id, ''), status, project_path, worktree_path,
		       git_ref, branch_from_seq, sibling_ordinal, is_root, created_at, updated_at
		  FROM workspaces
		 WHERE id IN (SELECT id FROM lineage)
		 ORDER BY created_at ASC, id ASC`,
		rootWorkspaceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	workspaces := make([]workspace.Workspace, 0)
	for rows.Next() {
		ws, err := scanWorkspace(rows)
		if err != nil {
			return nil, err
		}
		workspaces = append(workspaces, *ws)
	}
	return workspaces, rows.Err()
}

// LineageRootWorkspaceID resolves a workspace lineage back to its root workspace ID.
func (s *Store) LineageRootWorkspaceID(workspaceID string) (string, error) {
	path, err := s.GetBranchPath(workspaceID)
	if err != nil {
		return "", err
	}
	if len(path) == 0 {
		return "", sql.ErrNoRows
	}
	return path[0].WorkspaceID, nil
}
