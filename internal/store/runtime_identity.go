package store

import (
	"database/sql"
	"time"
)

type CheckoutLease struct {
	CheckoutPath string
	RuntimeID    string
	WorkspaceID  string
	ClaimedAt    time.Time
	UpdatedAt    time.Time
}

type DetachedCompanionRuntime struct {
	RuntimeID         string
	ActiveWorkspaceID string
	OwnerSessionID    string
	ConfigSource      string
	Phase             string
	Message           string
	BrowserPathPrefix string
	BrowserTargetURL  string
	UpdatedAt         time.Time
}

type PortLease struct {
	RuntimeID   string
	ServiceName string
	Host        string
	Port        int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type ManagedRuntimeView struct {
	Runtime   ManagedRuntime
	Checkout  *CheckoutLease
	Companion *DetachedCompanionRuntime
}

type RuntimeWorkspaceView struct {
	Workspace      WorkspaceSummary `json:"workspace"`
	IsActive       bool             `json:"is_active"`
	LatestSession  *SessionSummary  `json:"latest_session,omitempty"`
	RuntimeAppPath string           `json:"runtime_app_path,omitempty"`
}

func (s *Store) UpsertCheckoutLease(lease CheckoutLease) error {
	_, err := s.db.Exec(
		`INSERT INTO checkout_leases (checkout_path, runtime_id, workspace_id, claimed_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(checkout_path) DO UPDATE SET
		   runtime_id = excluded.runtime_id,
		   workspace_id = excluded.workspace_id,
		   updated_at = excluded.updated_at`,
		lease.CheckoutPath,
		lease.RuntimeID,
		lease.WorkspaceID,
		lease.ClaimedAt.Format(time.RFC3339Nano),
		lease.UpdatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) GetCheckoutLease(checkoutPath string) (*CheckoutLease, error) {
	row := s.db.QueryRow(
		`SELECT checkout_path, runtime_id, workspace_id, claimed_at, updated_at
		   FROM checkout_leases
		  WHERE checkout_path = ?`,
		checkoutPath,
	)
	var lease CheckoutLease
	var claimedAt string
	var updatedAt string
	if err := row.Scan(&lease.CheckoutPath, &lease.RuntimeID, &lease.WorkspaceID, &claimedAt, &updatedAt); err != nil {
		return nil, err
	}
	lease.ClaimedAt = mustParseTime(claimedAt)
	lease.UpdatedAt = mustParseTime(updatedAt)
	return &lease, nil
}

func (s *Store) UpsertDetachedCompanionRuntime(runtime DetachedCompanionRuntime) error {
	_, err := s.db.Exec(
		`INSERT INTO detached_companion_runtimes (
			runtime_id, active_workspace_id, owner_session_id, config_source, phase, message,
			browser_path_prefix, browser_target_url, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(runtime_id) DO UPDATE SET
			active_workspace_id = excluded.active_workspace_id,
			owner_session_id = excluded.owner_session_id,
			config_source = excluded.config_source,
			phase = excluded.phase,
			message = excluded.message,
			browser_path_prefix = excluded.browser_path_prefix,
			browser_target_url = excluded.browser_target_url,
			updated_at = excluded.updated_at`,
		runtime.RuntimeID,
		runtime.ActiveWorkspaceID,
		runtime.OwnerSessionID,
		runtime.ConfigSource,
		runtime.Phase,
		runtime.Message,
		runtime.BrowserPathPrefix,
		runtime.BrowserTargetURL,
		runtime.UpdatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) GetDetachedCompanionRuntime(runtimeID string) (*DetachedCompanionRuntime, error) {
	row := s.db.QueryRow(
		`SELECT runtime_id, active_workspace_id, owner_session_id, config_source, phase, message,
		        browser_path_prefix, browser_target_url, updated_at
		   FROM detached_companion_runtimes
		  WHERE runtime_id = ?`,
		runtimeID,
	)
	var runtime DetachedCompanionRuntime
	var updatedAt string
	if err := row.Scan(
		&runtime.RuntimeID,
		&runtime.ActiveWorkspaceID,
		&runtime.OwnerSessionID,
		&runtime.ConfigSource,
		&runtime.Phase,
		&runtime.Message,
		&runtime.BrowserPathPrefix,
		&runtime.BrowserTargetURL,
		&updatedAt,
	); err != nil {
		return nil, err
	}
	runtime.UpdatedAt = mustParseTime(updatedAt)
	return &runtime, nil
}

func (s *Store) UpsertPortLease(lease PortLease) error {
	_, err := s.db.Exec(
		`INSERT INTO port_leases (runtime_id, service_name, host, port, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(runtime_id, service_name) DO UPDATE SET
		   host = excluded.host,
		   port = excluded.port,
		   updated_at = excluded.updated_at`,
		lease.RuntimeID,
		lease.ServiceName,
		lease.Host,
		lease.Port,
		lease.CreatedAt.Format(time.RFC3339Nano),
		lease.UpdatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) GetPortLease(runtimeID, serviceName string) (*PortLease, error) {
	row := s.db.QueryRow(
		`SELECT runtime_id, service_name, host, port, created_at, updated_at
		   FROM port_leases
		  WHERE runtime_id = ? AND service_name = ?`,
		runtimeID,
		serviceName,
	)
	var lease PortLease
	var createdAt string
	var updatedAt string
	if err := row.Scan(&lease.RuntimeID, &lease.ServiceName, &lease.Host, &lease.Port, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	lease.CreatedAt = mustParseTime(createdAt)
	lease.UpdatedAt = mustParseTime(updatedAt)
	return &lease, nil
}

func (s *Store) ListPortLeases() ([]PortLease, error) {
	rows, err := s.db.Query(
		`SELECT runtime_id, service_name, host, port, created_at, updated_at
		   FROM port_leases
		  ORDER BY host ASC, port ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]PortLease, 0)
	for rows.Next() {
		var lease PortLease
		var createdAt string
		var updatedAt string
		if err := rows.Scan(&lease.RuntimeID, &lease.ServiceName, &lease.Host, &lease.Port, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		lease.CreatedAt = mustParseTime(createdAt)
		lease.UpdatedAt = mustParseTime(updatedAt)
		result = append(result, lease)
	}
	return result, rows.Err()
}

func (s *Store) ListManagedRuntimeViews() ([]ManagedRuntimeView, error) {
	runtimes, err := s.ListManagedRuntimes()
	if err != nil {
		return nil, err
	}

	result := make([]ManagedRuntimeView, 0, len(runtimes))
	for _, runtime := range runtimes {
		view := ManagedRuntimeView{Runtime: runtime}
		if runtime.ProjectPath != "" {
			if lease, err := s.GetCheckoutLease(runtime.ProjectPath); err == nil && lease.RuntimeID == runtime.ID {
				view.Checkout = lease
			}
		}
		if companion, err := s.GetDetachedCompanionRuntime(runtime.ID); err == nil {
			view.Companion = companion
		}
		result = append(result, view)
	}
	return result, nil
}

func (s *Store) GetManagedRuntimeView(runtimeID string) (ManagedRuntimeView, error) {
	runtime, err := s.GetManagedRuntime(runtimeID)
	if err != nil {
		return ManagedRuntimeView{}, err
	}
	view := ManagedRuntimeView{Runtime: *runtime}
	if runtime.ProjectPath != "" {
		if lease, err := s.GetCheckoutLease(runtime.ProjectPath); err == nil && lease.RuntimeID == runtime.ID {
			view.Checkout = lease
		}
	}
	if companion, err := s.GetDetachedCompanionRuntime(runtime.ID); err == nil {
		view.Companion = companion
	}
	return view, nil
}

func (s *Store) ListSessionSummariesForRuntime(runtimeID string) ([]SessionSummary, error) {
	runtime, err := s.GetManagedRuntime(runtimeID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(sessionSummarySelect+`
		AND s.id IN (
			SELECT DISTINCT ws.session_id
			  FROM workspace_sessions ws
			  JOIN (
				WITH RECURSIVE lineage(id) AS (
					SELECT id FROM workspaces WHERE id = ?
					UNION ALL
					SELECT w.id
					  FROM workspaces w
					  JOIN lineage l ON w.parent_workspace_id = l.id
				)
				SELECT id FROM lineage
			  ) runtime_lineage ON runtime_lineage.id = ws.workspace_id
		)
		GROUP BY s.id, s.source, s.project_path, s.source_session_id, s.parent_session_id, s.created_at, s.updated_at
		ORDER BY s.updated_at DESC, s.created_at DESC`, runtime.RootWorkspaceID)
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

func (s *Store) ListRuntimeWorkspaceViews(runtimeID string) ([]RuntimeWorkspaceView, error) {
	runtime, err := s.GetManagedRuntime(runtimeID)
	if err != nil {
		return nil, err
	}
	workspaces, err := s.ListLineageWorkspaces(runtime.RootWorkspaceID)
	if err != nil {
		return nil, err
	}
	result := make([]RuntimeWorkspaceView, 0, len(workspaces))
	for _, ws := range workspaces {
		summary, err := s.GetWorkspaceSummary(ws.ID)
		if err != nil {
			return nil, err
		}
		view := RuntimeWorkspaceView{
			Workspace:      summary,
			IsActive:       ws.ID == runtime.ActiveWorkspaceID,
			RuntimeAppPath: "/r/" + runtime.ID + "/app/",
		}
		session, err := s.GetLatestWorkspaceSession(ws.ID)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		if err == nil {
			sessionSummary, err := s.GetSessionSummary(session.ID)
			if err != nil {
				return nil, err
			}
			view.LatestSession = &sessionSummary
		}
		result = append(result, view)
	}
	return result, nil
}

func (s *Store) ListManagedRuntimes() ([]ManagedRuntime, error) {
	rows, err := s.db.Query(
		`SELECT id, project_path, root_workspace_id, active_workspace_id, active_session_id,
		        source, launch_args_json, status, heartbeat_at, created_at, updated_at
		   FROM managed_runtimes
		  ORDER BY updated_at DESC, created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]ManagedRuntime, 0)
	for rows.Next() {
		rt, err := scanManagedRuntime(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *rt)
	}
	return result, rows.Err()
}
