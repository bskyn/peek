package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/bskyn/peek/internal/event"
	"github.com/bskyn/peek/internal/workspace"
)

// WorkspaceSummary is the read model for workspace listings.
type WorkspaceSummary struct {
	ID                string                    `json:"id"`
	ParentWorkspaceID string                    `json:"parent_workspace_id,omitempty"`
	Status            workspace.WorkspaceStatus `json:"status"`
	ProjectPath       string                    `json:"project_path"`
	WorktreePath      string                    `json:"worktree_path,omitempty"`
	GitRef            string                    `json:"git_ref,omitempty"`
	BranchFromSeq     *int64                    `json:"branch_from_seq,omitempty"`
	SiblingOrdinal    int                       `json:"sibling_ordinal"`
	SessionCount      int64                     `json:"session_count"`
	CheckpointCount   int64                     `json:"checkpoint_count"`
	CreatedAt         time.Time                 `json:"created_at"`
	UpdatedAt         time.Time                 `json:"updated_at"`
}

// BranchOrigin describes where a workspace branched from.
type BranchOrigin struct {
	ParentWorkspaceID string `json:"parent_workspace_id"`
	BranchFromSeq     int64  `json:"branch_from_seq"`
	SiblingOrdinal    int    `json:"sibling_ordinal"`
}

// BranchedWorkspaceCreate captures the atomic database mutations required to
// freeze a source workspace and create its child workspace/session metadata.
type BranchedWorkspaceCreate struct {
	SourceWorkspaceID string
	ChildWorkspace    workspace.Workspace
	ChildSession      event.Session
	ChildLink         workspace.WorkspaceSession
	ChildBranchPath   workspace.BranchPathSegment
}

// CreateWorkspace inserts or updates a workspace.
func (s *Store) CreateWorkspace(w workspace.Workspace) error {
	var branchSeq interface{}
	if w.BranchFromSeq != nil {
		branchSeq = *w.BranchFromSeq
	}

	isRoot := 0
	if w.IsRoot {
		isRoot = 1
	}

	_, err := s.db.Exec(
		`INSERT INTO workspaces (id, parent_workspace_id, status, project_path, worktree_path, git_ref, branch_from_seq, sibling_ordinal, is_root, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   status = excluded.status,
		   worktree_path = CASE
		     WHEN excluded.worktree_path != '' THEN excluded.worktree_path
		     ELSE workspaces.worktree_path
		   END,
		   git_ref = CASE
		     WHEN excluded.git_ref != '' THEN excluded.git_ref
		     ELSE workspaces.git_ref
		   END,
		   updated_at = excluded.updated_at`,
		w.ID, nilIfEmpty(w.ParentWorkspaceID), string(w.Status), w.ProjectPath,
		w.WorktreePath, w.GitRef, branchSeq, w.SiblingOrdinal, isRoot,
		w.CreatedAt.Format(time.RFC3339Nano), w.UpdatedAt.Format(time.RFC3339Nano),
	)
	return err
}

// CreateBranchedWorkspace atomically freezes the source workspace and creates
// the child workspace/session/linkage rows for a new branch.
func (s *Store) CreateBranchedWorkspace(input BranchedWorkspaceCreate) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := freezeActiveWorkspaceTx(tx, input.SourceWorkspaceID); err != nil {
		return err
	}
	if err := insertWorkspaceStrictTx(tx, input.ChildWorkspace); err != nil {
		return err
	}
	if err := insertSessionStrictTx(tx, input.ChildSession); err != nil {
		return err
	}
	if err := insertWorkspaceSessionTx(tx, input.ChildLink); err != nil {
		return err
	}
	if err := insertBranchPathTx(tx, input.ChildBranchPath); err != nil {
		return err
	}

	return tx.Commit()
}

func freezeActiveWorkspaceTx(tx *sql.Tx, id string) error {
	result, err := tx.Exec(
		`UPDATE workspaces
		    SET status = ?, updated_at = ?
		  WHERE id = ? AND status = ?`,
		string(workspace.StatusFrozen),
		time.Now().UTC().Format(time.RFC3339Nano),
		id,
		string(workspace.StatusActive),
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("source workspace %q is not active", id)
	}
	return nil
}

func insertWorkspaceStrictTx(tx *sql.Tx, w workspace.Workspace) error {
	var branchSeq any
	if w.BranchFromSeq != nil {
		branchSeq = *w.BranchFromSeq
	}

	isRoot := 0
	if w.IsRoot {
		isRoot = 1
	}

	_, err := tx.Exec(
		`INSERT INTO workspaces (id, parent_workspace_id, status, project_path, worktree_path, git_ref, branch_from_seq, sibling_ordinal, is_root, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		w.ID, nilIfEmpty(w.ParentWorkspaceID), string(w.Status), w.ProjectPath,
		w.WorktreePath, w.GitRef, branchSeq, w.SiblingOrdinal, isRoot,
		w.CreatedAt.Format(time.RFC3339Nano), w.UpdatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func insertSessionStrictTx(tx *sql.Tx, sess event.Session) error {
	_, err := tx.Exec(
		`INSERT INTO sessions (id, source, project_path, source_session_id, parent_session_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sess.ID, sess.Source, sess.ProjectPath, sess.SourceSessionID, nilIfEmpty(sess.ParentSessionID),
		sess.CreatedAt.Format(time.RFC3339Nano), sess.UpdatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func insertWorkspaceSessionTx(tx *sql.Tx, ws workspace.WorkspaceSession) error {
	_, err := tx.Exec(
		`INSERT INTO workspace_sessions (workspace_id, session_id, created_at)
		 VALUES (?, ?, ?)`,
		ws.WorkspaceID, ws.SessionID, ws.CreatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func insertBranchPathTx(tx *sql.Tx, seg workspace.BranchPathSegment) error {
	_, err := tx.Exec(
		`INSERT INTO branch_path_segments (workspace_id, parent_workspace_id, branch_seq, ordinal, depth)
		 VALUES (?, ?, ?, ?, ?)`,
		seg.WorkspaceID, nilIfEmpty(seg.ParentWorkspaceID), seg.BranchSeq, seg.Ordinal, seg.Depth,
	)
	return err
}

// GetWorkspace retrieves a workspace by ID.
func (s *Store) GetWorkspace(id string) (*workspace.Workspace, error) {
	row := s.db.QueryRow(
		`SELECT id, COALESCE(parent_workspace_id, ''), status, project_path, worktree_path, git_ref, branch_from_seq, sibling_ordinal, is_root, created_at, updated_at
		 FROM workspaces WHERE id = ?`, id)

	return scanWorkspace(row)
}

// UpdateWorkspaceStatus updates the status and updated_at fields.
func (s *Store) UpdateWorkspaceStatus(id string, status workspace.WorkspaceStatus) error {
	result, err := s.db.Exec(
		`UPDATE workspaces SET status = ?, updated_at = ? WHERE id = ?`,
		string(status), time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// UpdateWorkspaceWorktree updates the worktree path (materialization/dematerialization).
func (s *Store) UpdateWorkspaceWorktree(id string, worktreePath string) error {
	_, err := s.db.Exec(
		`UPDATE workspaces SET worktree_path = ?, updated_at = ? WHERE id = ?`,
		worktreePath, time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	return err
}

// UpdateWorkspaceGitRef updates the git ref for a workspace.
func (s *Store) UpdateWorkspaceGitRef(id string, gitRef string) error {
	_, err := s.db.Exec(
		`UPDATE workspaces SET git_ref = ?, updated_at = ? WHERE id = ?`,
		gitRef, time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	return err
}

// ListWorkspaces returns all workspaces ordered by most recent activity.
func (s *Store) ListWorkspaces() ([]WorkspaceSummary, error) {
	rows, err := s.db.Query(workspaceSummarySelect + `
		GROUP BY w.id
		ORDER BY w.updated_at DESC, w.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	summaries := make([]WorkspaceSummary, 0)
	for rows.Next() {
		summary, err := scanWorkspaceSummary(rows)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	return summaries, rows.Err()
}

// ListChildWorkspaces returns child workspaces of a given parent.
func (s *Store) ListChildWorkspaces(parentID string) ([]WorkspaceSummary, error) {
	rows, err := s.db.Query(workspaceSummarySelect+`
		AND w.parent_workspace_id = ?
		GROUP BY w.id
		ORDER BY w.sibling_ordinal ASC, w.created_at ASC`, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	children := make([]WorkspaceSummary, 0)
	for rows.Next() {
		child, err := scanWorkspaceSummary(rows)
		if err != nil {
			return nil, err
		}
		children = append(children, child)
	}
	return children, rows.Err()
}

// GetWorkspaceSummary loads one workspace summary.
func (s *Store) GetWorkspaceSummary(id string) (WorkspaceSummary, error) {
	row := s.db.QueryRow(workspaceSummarySelect+`
		AND w.id = ?
		GROUP BY w.id`, id)
	return scanWorkspaceSummary(row)
}

// NextSiblingOrdinal returns the next sibling ordinal for branches from a parent workspace at a given sequence.
func (s *Store) NextSiblingOrdinal(parentID string, branchFromSeq int64) (int, error) {
	var maxOrdinal sql.NullInt64
	err := s.db.QueryRow(
		`SELECT MAX(sibling_ordinal) FROM workspaces WHERE parent_workspace_id = ? AND branch_from_seq = ?`,
		parentID, branchFromSeq,
	).Scan(&maxOrdinal)
	if err != nil {
		return 0, err
	}
	if !maxOrdinal.Valid {
		return 0, nil
	}
	return int(maxOrdinal.Int64) + 1, nil
}

// LinkWorkspaceSession creates a workspace-session association.
func (s *Store) LinkWorkspaceSession(ws workspace.WorkspaceSession) error {
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO workspace_sessions (workspace_id, session_id, created_at)
		 VALUES (?, ?, ?)`,
		ws.WorkspaceID, ws.SessionID, ws.CreatedAt.Format(time.RFC3339Nano),
	)
	return err
}

// ListWorkspaceSessions returns session IDs linked to a workspace.
func (s *Store) ListWorkspaceSessions(workspaceID string) ([]workspace.WorkspaceSession, error) {
	rows, err := s.db.Query(
		`SELECT workspace_id, session_id, created_at FROM workspace_sessions WHERE workspace_id = ? ORDER BY created_at ASC`,
		workspaceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []workspace.WorkspaceSession
	for rows.Next() {
		var ws workspace.WorkspaceSession
		var createdAt string
		if err := rows.Scan(&ws.WorkspaceID, &ws.SessionID, &createdAt); err != nil {
			return nil, err
		}
		ws.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		result = append(result, ws)
	}
	return result, rows.Err()
}

// CreateCheckpoint inserts a checkpoint reference. Duplicate (workspace_id, seq, kind) is ignored.
func (s *Store) CreateCheckpoint(cp workspace.CheckpointRef) error {
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO checkpoints (id, workspace_id, session_id, seq, kind, git_ref, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		cp.ID, cp.WorkspaceID, cp.SessionID, cp.Seq, string(cp.Kind), cp.GitRef,
		cp.CreatedAt.Format(time.RFC3339Nano),
	)
	return err
}

// GetCheckpoint retrieves a specific checkpoint.
func (s *Store) GetCheckpoint(id string) (*workspace.CheckpointRef, error) {
	row := s.db.QueryRow(
		`SELECT id, workspace_id, session_id, seq, kind, git_ref, created_at
		 FROM checkpoints WHERE id = ?`, id)
	return scanCheckpoint(row)
}

// ListCheckpoints returns all checkpoints for a workspace ordered by sequence.
func (s *Store) ListCheckpoints(workspaceID string) ([]workspace.CheckpointRef, error) {
	rows, err := s.db.Query(
		`SELECT id, workspace_id, session_id, seq, kind, git_ref, created_at
		 FROM checkpoints WHERE workspace_id = ? ORDER BY seq ASC, kind ASC`,
		workspaceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []workspace.CheckpointRef
	for rows.Next() {
		cp, err := scanCheckpoint(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *cp)
	}
	return result, rows.Err()
}

// ResolveCheckpoint finds the latest checkpoint at or before the given sequence for the specified kind.
func (s *Store) ResolveCheckpoint(workspaceID string, seq int64, kind workspace.SnapshotKind) (*workspace.CheckpointRef, error) {
	row := s.db.QueryRow(
		`SELECT id, workspace_id, session_id, seq, kind, git_ref, created_at
		 FROM checkpoints
		 WHERE workspace_id = ? AND seq <= ? AND kind = ?
		 ORDER BY seq DESC
		 LIMIT 1`,
		workspaceID, seq, string(kind),
	)
	return scanCheckpoint(row)
}

// SaveBranchPath inserts or updates a branch path segment.
func (s *Store) SaveBranchPath(seg workspace.BranchPathSegment) error {
	_, err := s.db.Exec(
		`INSERT INTO branch_path_segments (workspace_id, parent_workspace_id, branch_seq, ordinal, depth)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(workspace_id) DO UPDATE SET
		   parent_workspace_id = excluded.parent_workspace_id,
		   branch_seq = excluded.branch_seq,
		   ordinal = excluded.ordinal,
		   depth = excluded.depth`,
		seg.WorkspaceID, nilIfEmpty(seg.ParentWorkspaceID), seg.BranchSeq, seg.Ordinal, seg.Depth,
	)
	return err
}

// GetBranchPath returns the breadcrumb trail from a workspace back to the root.
func (s *Store) GetBranchPath(workspaceID string) ([]workspace.BranchPathSegment, error) {
	// Walk up from the given workspace to root
	var path []workspace.BranchPathSegment
	currentID := workspaceID

	for currentID != "" {
		row := s.db.QueryRow(
			`SELECT workspace_id, COALESCE(parent_workspace_id, ''), branch_seq, ordinal, depth
			 FROM branch_path_segments WHERE workspace_id = ?`, currentID)

		var seg workspace.BranchPathSegment
		err := row.Scan(&seg.WorkspaceID, &seg.ParentWorkspaceID, &seg.BranchSeq, &seg.Ordinal, &seg.Depth)
		if err != nil {
			if err == sql.ErrNoRows {
				break
			}
			return nil, err
		}
		path = append(path, seg)
		currentID = seg.ParentWorkspaceID
	}

	// Reverse so root is first
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}

	return path, nil
}

// DeleteWorkspace removes a workspace and its associated data.
func (s *Store) DeleteWorkspace(id string) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	var childCount int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM workspaces WHERE parent_workspace_id = ?`, id).Scan(&childCount); err != nil {
		return false, err
	}
	if childCount > 0 {
		return false, fmt.Errorf("workspace %q has child workspaces; delete them first", id)
	}

	rows, err := tx.Query(
		`SELECT id, root_workspace_id, active_session_id, status
		   FROM managed_runtimes
		  WHERE active_workspace_id = ?`,
		id,
	)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	type runtimeRef struct {
		id              string
		rootWorkspaceID string
		activeSessionID string
		status          ManagedRuntimeStatus
	}

	var runtimeRefs []runtimeRef
	for rows.Next() {
		var ref runtimeRef
		if err := rows.Scan(&ref.id, &ref.rootWorkspaceID, &ref.activeSessionID, &ref.status); err != nil {
			return false, err
		}
		runtimeRefs = append(runtimeRefs, ref)
	}
	if err := rows.Err(); err != nil {
		return false, err
	}

	for _, ref := range runtimeRefs {
		if ref.rootWorkspaceID == id {
			return false, fmt.Errorf("workspace %q is still the root of managed runtime %q", id, ref.id)
		}
		if ref.status != ManagedRuntimeStopped {
			return false, fmt.Errorf("workspace %q is still referenced by managed runtime %q", id, ref.id)
		}
		if err := parkManagedRuntimeTx(tx, ref.id, ref.rootWorkspaceID, ref.activeSessionID); err != nil {
			return false, err
		}
	}

	if _, err := tx.Exec(`DELETE FROM checkpoints WHERE workspace_id = ?`, id); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`DELETE FROM workspace_sessions WHERE workspace_id = ?`, id); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`DELETE FROM branch_path_segments WHERE workspace_id = ?`, id); err != nil {
		return false, err
	}

	result, err := tx.Exec(`DELETE FROM workspaces WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}

	if err := tx.Commit(); err != nil {
		return false, err
	}
	return affected > 0, nil
}

// -- helpers --

const workspaceSummarySelect = `
SELECT
	w.id,
	COALESCE(w.parent_workspace_id, ''),
	w.status,
	w.project_path,
	w.worktree_path,
	w.git_ref,
	w.branch_from_seq,
	w.sibling_ordinal,
	(SELECT COUNT(*) FROM workspace_sessions ws WHERE ws.workspace_id = w.id),
	(SELECT COUNT(*) FROM checkpoints c WHERE c.workspace_id = w.id),
	w.created_at,
	w.updated_at
FROM workspaces w
WHERE 1 = 1`

func scanWorkspaceSummary(scanner sessionSummaryScanner) (WorkspaceSummary, error) {
	var summary WorkspaceSummary
	var branchSeq sql.NullInt64
	var createdAt, updatedAt string
	err := scanner.Scan(
		&summary.ID,
		&summary.ParentWorkspaceID,
		&summary.Status,
		&summary.ProjectPath,
		&summary.WorktreePath,
		&summary.GitRef,
		&branchSeq,
		&summary.SiblingOrdinal,
		&summary.SessionCount,
		&summary.CheckpointCount,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return WorkspaceSummary{}, err
	}
	if branchSeq.Valid {
		v := branchSeq.Int64
		summary.BranchFromSeq = &v
	}
	summary.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	summary.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return summary, nil
}

type checkpointScanner interface {
	Scan(dest ...any) error
}

func scanCheckpoint(scanner checkpointScanner) (*workspace.CheckpointRef, error) {
	var cp workspace.CheckpointRef
	var createdAt string
	err := scanner.Scan(&cp.ID, &cp.WorkspaceID, &cp.SessionID, &cp.Seq, &cp.Kind, &cp.GitRef, &createdAt)
	if err != nil {
		return nil, err
	}
	cp.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	return &cp, nil
}

func scanWorkspace(scanner checkpointScanner) (*workspace.Workspace, error) {
	var w workspace.Workspace
	var branchSeq sql.NullInt64
	var isRoot int
	var createdAt, updatedAt string
	err := scanner.Scan(
		&w.ID, &w.ParentWorkspaceID, &w.Status, &w.ProjectPath, &w.WorktreePath,
		&w.GitRef, &branchSeq, &w.SiblingOrdinal, &isRoot, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	if branchSeq.Valid {
		v := branchSeq.Int64
		w.BranchFromSeq = &v
	}
	w.IsRoot = isRoot != 0
	w.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	w.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return &w, nil
}

func checkpointID(workspaceID string, seq int64, kind workspace.SnapshotKind) string {
	return fmt.Sprintf("cp-%s-%d-%s", workspaceID, seq, kind)
}

// CheckpointID generates a deterministic checkpoint ID.
func CheckpointID(workspaceID string, seq int64, kind workspace.SnapshotKind) string {
	return checkpointID(workspaceID, seq, kind)
}
