package store

import "time"

type BootstrapStatus string

const (
	BootstrapPending   BootstrapStatus = "pending"
	BootstrapRunning   BootstrapStatus = "running"
	BootstrapSucceeded BootstrapStatus = "succeeded"
	BootstrapFailed    BootstrapStatus = "failed"
)

type WorkspaceBootstrapState struct {
	WorkspaceID string
	Fingerprint string
	Status      BootstrapStatus
	LastError   string
	StartedAt   time.Time
	FinishedAt  time.Time
	UpdatedAt   time.Time
}

type CompanionServiceStatus string

const (
	CompanionServiceStarting CompanionServiceStatus = "starting"
	CompanionServiceReady    CompanionServiceStatus = "ready"
	CompanionServiceFailed   CompanionServiceStatus = "failed"
	CompanionServiceStopped  CompanionServiceStatus = "stopped"
)

type CompanionServiceState struct {
	RuntimeID   string
	WorkspaceID string
	ServiceName string
	Role        string
	Status      CompanionServiceStatus
	TargetURL   string
	LastError   string
	StartedAt   time.Time
	ReadyAt     time.Time
	StoppedAt   time.Time
	UpdatedAt   time.Time
}

func (s *Store) UpsertWorkspaceBootstrapState(state WorkspaceBootstrapState) error {
	_, err := s.db.Exec(
		`INSERT INTO workspace_bootstrap_states (
			workspace_id, fingerprint, status, last_error, started_at, finished_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(workspace_id) DO UPDATE SET
			fingerprint = excluded.fingerprint,
			status = excluded.status,
			last_error = excluded.last_error,
			started_at = excluded.started_at,
			finished_at = excluded.finished_at,
			updated_at = excluded.updated_at`,
		state.WorkspaceID,
		state.Fingerprint,
		string(state.Status),
		state.LastError,
		formatOptionalTime(state.StartedAt),
		formatOptionalTime(state.FinishedAt),
		state.UpdatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) GetWorkspaceBootstrapState(workspaceID string) (*WorkspaceBootstrapState, error) {
	row := s.db.QueryRow(
		`SELECT workspace_id, fingerprint, status, last_error, started_at, finished_at, updated_at
		   FROM workspace_bootstrap_states
		  WHERE workspace_id = ?`,
		workspaceID,
	)
	return scanWorkspaceBootstrapState(row)
}

func (s *Store) ReplaceCompanionServiceStates(runtimeID string, states []CompanionServiceState) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.Exec(`DELETE FROM companion_service_states WHERE runtime_id = ?`, runtimeID); err != nil {
		return err
	}
	for _, state := range states {
		if _, err := tx.Exec(
			`INSERT INTO companion_service_states (
				runtime_id, workspace_id, service_name, role, status, target_url, last_error,
				started_at, ready_at, stopped_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			state.RuntimeID,
			state.WorkspaceID,
			state.ServiceName,
			state.Role,
			string(state.Status),
			state.TargetURL,
			state.LastError,
			formatOptionalTime(state.StartedAt),
			formatOptionalTime(state.ReadyAt),
			formatOptionalTime(state.StoppedAt),
			state.UpdatedAt.Format(time.RFC3339Nano),
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListCompanionServiceStates(runtimeID string) ([]CompanionServiceState, error) {
	rows, err := s.db.Query(
		`SELECT runtime_id, workspace_id, service_name, role, status, target_url, last_error,
		        started_at, ready_at, stopped_at, updated_at
		   FROM companion_service_states
		  WHERE runtime_id = ?
		  ORDER BY service_name ASC`,
		runtimeID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []CompanionServiceState
	for rows.Next() {
		state, err := scanCompanionServiceState(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *state)
	}
	return result, rows.Err()
}

func scanWorkspaceBootstrapState(scanner interface {
	Scan(dest ...any) error
}) (*WorkspaceBootstrapState, error) {
	var (
		state      WorkspaceBootstrapState
		startedAt  string
		finishedAt string
		updatedAt  string
	)
	if err := scanner.Scan(
		&state.WorkspaceID,
		&state.Fingerprint,
		&state.Status,
		&state.LastError,
		&startedAt,
		&finishedAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}
	state.StartedAt = parseOptionalTime(startedAt)
	state.FinishedAt = parseOptionalTime(finishedAt)
	state.UpdatedAt = mustParseTime(updatedAt)
	return &state, nil
}

func scanCompanionServiceState(scanner interface {
	Scan(dest ...any) error
}) (*CompanionServiceState, error) {
	var (
		state     CompanionServiceState
		startedAt string
		readyAt   string
		stoppedAt string
		updatedAt string
	)
	if err := scanner.Scan(
		&state.RuntimeID,
		&state.WorkspaceID,
		&state.ServiceName,
		&state.Role,
		&state.Status,
		&state.TargetURL,
		&state.LastError,
		&startedAt,
		&readyAt,
		&stoppedAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}
	state.StartedAt = parseOptionalTime(startedAt)
	state.ReadyAt = parseOptionalTime(readyAt)
	state.StoppedAt = parseOptionalTime(stoppedAt)
	state.UpdatedAt = mustParseTime(updatedAt)
	return &state, nil
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339Nano)
}

func parseOptionalTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	return mustParseTime(value)
}

func mustParseTime(value string) time.Time {
	timestamp, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return timestamp
}

func (s *Store) clearCompanionServiceStates(runtimeID string) error {
	_, err := s.db.Exec(`DELETE FROM companion_service_states WHERE runtime_id = ?`, runtimeID)
	return err
}
