package store

import (
	"database/sql"
	"encoding/json"
	"time"
)

// ManagedRuntimeStatus represents the lifecycle of a live managed runtime.
type ManagedRuntimeStatus string

const (
	ManagedRuntimeRunning ManagedRuntimeStatus = "running"
	ManagedRuntimeStopped ManagedRuntimeStatus = "stopped"
)

// ManagedRuntime tracks the live control-plane state for one workspace lineage.
type ManagedRuntime struct {
	ID                string
	RootWorkspaceID   string
	ActiveWorkspaceID string
	ActiveSessionID   string
	Source            string
	LaunchArgs        []string
	Status            ManagedRuntimeStatus
	HeartbeatAt       time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// ManagedRuntimeRequestStatus is the state of a queued runtime control request.
type ManagedRuntimeRequestStatus string

const (
	ManagedRuntimeRequestPending    ManagedRuntimeRequestStatus = "pending"
	ManagedRuntimeRequestProcessing ManagedRuntimeRequestStatus = "processing"
	ManagedRuntimeRequestCompleted  ManagedRuntimeRequestStatus = "completed"
	ManagedRuntimeRequestFailed     ManagedRuntimeRequestStatus = "failed"
)

// ManagedRuntimeRequest represents one branch/switch request sent to a live runtime.
type ManagedRuntimeRequest struct {
	ID                   string
	RuntimeID            string
	Kind                 string
	SourceWorkspaceID    string
	BranchFromSeq        *int64
	TargetWorkspaceID    string
	Status               ManagedRuntimeRequestStatus
	ResponseWorkspaceID  string
	ResponseSessionID    string
	ResponseWorktreePath string
	ResponseGitRef       string
	Error                string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// ManagedRuntimeResponse is the stored response payload for a completed request.
type ManagedRuntimeResponse struct {
	WorkspaceID  string
	SessionID    string
	WorktreePath string
	GitRef       string
}

// UpsertManagedRuntime inserts or updates a managed runtime record.
func (s *Store) UpsertManagedRuntime(rt ManagedRuntime) error {
	argsJSON, err := json.Marshal(rt.LaunchArgs)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(
		`INSERT INTO managed_runtimes (
			id, root_workspace_id, active_workspace_id, active_session_id,
			source, launch_args_json, status, heartbeat_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			active_workspace_id = excluded.active_workspace_id,
			active_session_id = excluded.active_session_id,
			source = excluded.source,
			launch_args_json = excluded.launch_args_json,
			status = excluded.status,
			heartbeat_at = excluded.heartbeat_at,
			updated_at = excluded.updated_at`,
		rt.ID,
		rt.RootWorkspaceID,
		rt.ActiveWorkspaceID,
		rt.ActiveSessionID,
		rt.Source,
		string(argsJSON),
		string(rt.Status),
		rt.HeartbeatAt.Format(time.RFC3339Nano),
		rt.CreatedAt.Format(time.RFC3339Nano),
		rt.UpdatedAt.Format(time.RFC3339Nano),
	)
	return err
}

// GetManagedRuntime loads one runtime by ID.
func (s *Store) GetManagedRuntime(id string) (*ManagedRuntime, error) {
	row := s.db.QueryRow(
		`SELECT id, root_workspace_id, active_workspace_id, active_session_id,
		        source, launch_args_json, status, heartbeat_at, created_at, updated_at
		   FROM managed_runtimes
		  WHERE id = ?`,
		id,
	)
	return scanManagedRuntime(row)
}

// GetManagedRuntimeByRootWorkspace loads the runtime for a lineage root, if any.
func (s *Store) GetManagedRuntimeByRootWorkspace(rootWorkspaceID string) (*ManagedRuntime, error) {
	row := s.db.QueryRow(
		`SELECT id, root_workspace_id, active_workspace_id, active_session_id,
		        source, launch_args_json, status, heartbeat_at, created_at, updated_at
		   FROM managed_runtimes
		  WHERE root_workspace_id = ?
		  ORDER BY updated_at DESC, created_at DESC
		  LIMIT 1`,
		rootWorkspaceID,
	)
	return scanManagedRuntime(row)
}

// GetManagedRuntimeForWorkspace resolves a workspace lineage to its live runtime record.
func (s *Store) GetManagedRuntimeForWorkspace(workspaceID string) (*ManagedRuntime, error) {
	path, err := s.GetBranchPath(workspaceID)
	if err != nil {
		return nil, err
	}
	if len(path) == 0 {
		return nil, sql.ErrNoRows
	}
	return s.GetManagedRuntimeByRootWorkspace(path[0].WorkspaceID)
}

// TouchManagedRuntime refreshes the heartbeat and active pointers for a live runtime.
func (s *Store) TouchManagedRuntime(id, activeWorkspaceID, activeSessionID string) error {
	_, err := s.db.Exec(
		`UPDATE managed_runtimes
		    SET active_workspace_id = ?,
		        active_session_id = ?,
		        heartbeat_at = ?,
		        updated_at = ?
		  WHERE id = ?`,
		activeWorkspaceID,
		activeSessionID,
		time.Now().UTC().Format(time.RFC3339Nano),
		time.Now().UTC().Format(time.RFC3339Nano),
		id,
	)
	return err
}

// UpdateManagedRuntimeStatus sets the runtime status and active pointers.
func (s *Store) UpdateManagedRuntimeStatus(id string, status ManagedRuntimeStatus, activeWorkspaceID, activeSessionID string) error {
	_, err := s.db.Exec(
		`UPDATE managed_runtimes
		    SET status = ?,
		        active_workspace_id = ?,
		        active_session_id = ?,
		        updated_at = ?
		  WHERE id = ?`,
		string(status),
		activeWorkspaceID,
		activeSessionID,
		time.Now().UTC().Format(time.RFC3339Nano),
		id,
	)
	return err
}

// CreateManagedRuntimeRequest inserts a runtime request. Duplicate IDs are ignored.
func (s *Store) CreateManagedRuntimeRequest(req ManagedRuntimeRequest) error {
	var branchSeq any
	if req.BranchFromSeq != nil {
		branchSeq = *req.BranchFromSeq
	}

	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO managed_runtime_requests (
			id, runtime_id, kind, source_workspace_id, branch_from_seq, target_workspace_id,
			status, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		req.ID,
		req.RuntimeID,
		req.Kind,
		nilIfEmpty(req.SourceWorkspaceID),
		branchSeq,
		nilIfEmpty(req.TargetWorkspaceID),
		string(req.Status),
		req.CreatedAt.Format(time.RFC3339Nano),
		req.UpdatedAt.Format(time.RFC3339Nano),
	)
	return err
}

// ClaimNextManagedRuntimeRequest loads and marks the oldest pending request as processing.
func (s *Store) ClaimNextManagedRuntimeRequest(runtimeID string) (*ManagedRuntimeRequest, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	row := tx.QueryRow(
		`SELECT id, runtime_id, kind, COALESCE(source_workspace_id, ''), branch_from_seq,
		        COALESCE(target_workspace_id, ''), status,
		        response_workspace_id, response_session_id, response_worktree_path, response_git_ref,
		        error, created_at, updated_at
		   FROM managed_runtime_requests
		  WHERE runtime_id = ? AND status = ?
		  ORDER BY created_at ASC
		  LIMIT 1`,
		runtimeID,
		string(ManagedRuntimeRequestPending),
	)
	req, err := scanManagedRuntimeRequest(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	if _, err := tx.Exec(
		`UPDATE managed_runtime_requests
		    SET status = ?, updated_at = ?
		  WHERE id = ?`,
		string(ManagedRuntimeRequestProcessing),
		time.Now().UTC().Format(time.RFC3339Nano),
		req.ID,
	); err != nil {
		return nil, err
	}

	req.Status = ManagedRuntimeRequestProcessing
	req.UpdatedAt = time.Now().UTC()
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return req, nil
}

// GetManagedRuntimeRequest loads one runtime request by ID.
func (s *Store) GetManagedRuntimeRequest(id string) (*ManagedRuntimeRequest, error) {
	row := s.db.QueryRow(
		`SELECT id, runtime_id, kind, COALESCE(source_workspace_id, ''), branch_from_seq,
		        COALESCE(target_workspace_id, ''), status,
		        response_workspace_id, response_session_id, response_worktree_path, response_git_ref,
		        error, created_at, updated_at
		   FROM managed_runtime_requests
		  WHERE id = ?`,
		id,
	)
	return scanManagedRuntimeRequest(row)
}

// CompleteManagedRuntimeRequest stores a successful response payload.
func (s *Store) CompleteManagedRuntimeRequest(id string, resp ManagedRuntimeResponse) error {
	_, err := s.db.Exec(
		`UPDATE managed_runtime_requests
		    SET status = ?,
		        response_workspace_id = ?,
		        response_session_id = ?,
		        response_worktree_path = ?,
		        response_git_ref = ?,
		        error = '',
		        updated_at = ?
		  WHERE id = ?`,
		string(ManagedRuntimeRequestCompleted),
		resp.WorkspaceID,
		resp.SessionID,
		resp.WorktreePath,
		resp.GitRef,
		time.Now().UTC().Format(time.RFC3339Nano),
		id,
	)
	return err
}

// FailManagedRuntimeRequest stores a request failure.
func (s *Store) FailManagedRuntimeRequest(id string, errMsg string) error {
	_, err := s.db.Exec(
		`UPDATE managed_runtime_requests
		    SET status = ?,
		        error = ?,
		        updated_at = ?
		  WHERE id = ?`,
		string(ManagedRuntimeRequestFailed),
		errMsg,
		time.Now().UTC().Format(time.RFC3339Nano),
		id,
	)
	return err
}

func scanManagedRuntime(scanner sessionSummaryScanner) (*ManagedRuntime, error) {
	var rt ManagedRuntime
	var argsJSON string
	var heartbeatAt, createdAt, updatedAt string
	if err := scanner.Scan(
		&rt.ID,
		&rt.RootWorkspaceID,
		&rt.ActiveWorkspaceID,
		&rt.ActiveSessionID,
		&rt.Source,
		&argsJSON,
		&rt.Status,
		&heartbeatAt,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}
	if argsJSON != "" {
		_ = json.Unmarshal([]byte(argsJSON), &rt.LaunchArgs)
	}
	rt.HeartbeatAt, _ = time.Parse(time.RFC3339Nano, heartbeatAt)
	rt.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	rt.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return &rt, nil
}

func scanManagedRuntimeRequest(scanner sessionSummaryScanner) (*ManagedRuntimeRequest, error) {
	var req ManagedRuntimeRequest
	var branchSeq sql.NullInt64
	var createdAt, updatedAt string
	if err := scanner.Scan(
		&req.ID,
		&req.RuntimeID,
		&req.Kind,
		&req.SourceWorkspaceID,
		&branchSeq,
		&req.TargetWorkspaceID,
		&req.Status,
		&req.ResponseWorkspaceID,
		&req.ResponseSessionID,
		&req.ResponseWorktreePath,
		&req.ResponseGitRef,
		&req.Error,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}
	if branchSeq.Valid {
		v := branchSeq.Int64
		req.BranchFromSeq = &v
	}
	req.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	req.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return &req, nil
}
