package cli

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/bskyn/peek/internal/event"
	"github.com/bskyn/peek/internal/managed"
	"github.com/bskyn/peek/internal/store"
	"github.com/bskyn/peek/internal/workspace"
)

type managedStartState struct {
	runtimeID         string
	rootWorkspaceID   string
	activeWorkspaceID string
	activeSessionID   string
	activeWorktree    string
	reusedRuntime     bool
	isolatedRoot      bool
}

type managedStartLock struct {
	file *os.File
}

func prepareManagedStart(st *store.Store, orch *managed.Orchestrator, source managed.Source, projectPath, requestedRuntimeID string, launchArgs []string) (*managedStartState, error) {
	lock, err := acquireManagedStartLock(projectPath)
	if err != nil {
		return nil, err
	}
	defer lock.Close()

	now := time.Now().UTC()
	if requestedRuntimeID != "" {
		return prepareExplicitManagedStart(st, orch, source, projectPath, requestedRuntimeID, launchArgs, now)
	}
	lease, leaseErr := st.GetCheckoutLease(projectPath)
	if leaseErr != nil && leaseErr != sql.ErrNoRows {
		return nil, fmt.Errorf("load checkout lease: %w", leaseErr)
	}

	if leaseErr == nil {
		runtime, err := st.GetManagedRuntime(lease.RuntimeID)
		if err != nil && err != sql.ErrNoRows {
			return nil, fmt.Errorf("load leased runtime: %w", err)
		}
		if err == nil {
			if runtime.Source == string(source) && !managedRuntimeIsLive(runtime) {
				return reattachManagedStart(st, orch, runtime, launchArgs, now)
			}
			return createIsolatedManagedStart(st, source, projectPath, launchArgs, now)
		}
	}

	return createPrimaryManagedStart(st, source, projectPath, launchArgs, now)
}

func acquireManagedStartLock(projectPath string) (*managedStartLock, error) {
	sum := sha256.Sum256([]byte(projectPath))
	lockDir := filepath.Join(managedWorktreeBase(), "locks")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return nil, fmt.Errorf("create managed-start lock dir: %w", err)
	}
	lockPath := filepath.Join(lockDir, hex.EncodeToString(sum[:])+".lock")
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open managed-start lock %s: %w", lockPath, err)
	}
	if err := lockFile(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock managed-start %s: %w", lockPath, err)
	}
	return &managedStartLock{file: file}, nil
}

func (l *managedStartLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	if err := unlockFile(l.file); err != nil {
		_ = l.file.Close()
		return err
	}
	return l.file.Close()
}

func prepareExplicitManagedStart(st *store.Store, orch *managed.Orchestrator, source managed.Source, projectPath, requestedRuntimeID string, launchArgs []string, now time.Time) (*managedStartState, error) {
	runtime, err := st.GetManagedRuntime(requestedRuntimeID)
	if err != nil {
		return nil, fmt.Errorf("load requested runtime: %w", err)
	}
	if runtime.Source != string(source) {
		return nil, fmt.Errorf("runtime %s is %s, not %s", requestedRuntimeID, runtime.Source, source)
	}
	if runtime.ProjectPath != projectPath {
		return nil, fmt.Errorf("runtime %s belongs to %s, not %s", requestedRuntimeID, runtime.ProjectPath, projectPath)
	}
	if managedRuntimeIsLive(runtime) {
		return nil, fmt.Errorf("runtime %s is already live", requestedRuntimeID)
	}
	return reattachManagedStart(st, orch, runtime, launchArgs, now)
}

func managedRuntimeIsLive(runtime *store.ManagedRuntime) bool {
	if runtime == nil {
		return false
	}
	return runtime.Status == store.ManagedRuntimeRunning && time.Since(runtime.HeartbeatAt) <= managedRuntimeStaleAfter
}

func createPrimaryManagedStart(st *store.Store, source managed.Source, projectPath string, launchArgs []string, now time.Time) (*managedStartState, error) {
	wsID := newWorkspaceID()
	sessID := newManagedSessionID(source)
	runtimeID := newRuntimeID()

	if err := st.CreateWorkspace(workspace.Workspace{
		ID:           wsID,
		Status:       workspace.StatusActive,
		ProjectPath:  projectPath,
		WorktreePath: projectPath,
		IsRoot:       true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		return nil, fmt.Errorf("create root workspace: %w", err)
	}
	if err := createManagedSessionLink(st, sessID, string(source), projectPath, wsID, now); err != nil {
		return nil, err
	}
	if err := st.SaveBranchPath(workspace.BranchPathSegment{WorkspaceID: wsID, Depth: 0, Ordinal: 0}); err != nil {
		return nil, fmt.Errorf("save branch path: %w", err)
	}
	if err := st.UpsertManagedRuntime(store.ManagedRuntime{
		ID:                runtimeID,
		ProjectPath:       projectPath,
		RootWorkspaceID:   wsID,
		ActiveWorkspaceID: wsID,
		ActiveSessionID:   sessID,
		Source:            string(source),
		LaunchArgs:        append([]string(nil), launchArgs...),
		Status:            store.ManagedRuntimeRunning,
		HeartbeatAt:       now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		return nil, fmt.Errorf("create runtime: %w", err)
	}
	if err := st.UpsertCheckoutLease(store.CheckoutLease{
		CheckoutPath: projectPath,
		RuntimeID:    runtimeID,
		WorkspaceID:  wsID,
		ClaimedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		return nil, fmt.Errorf("claim checkout lease: %w", err)
	}
	return &managedStartState{
		runtimeID:         runtimeID,
		rootWorkspaceID:   wsID,
		activeWorkspaceID: wsID,
		activeSessionID:   sessID,
		activeWorktree:    projectPath,
	}, nil
}

func reattachManagedStart(st *store.Store, orch *managed.Orchestrator, runtime *store.ManagedRuntime, launchArgs []string, now time.Time) (*managedStartState, error) {
	activeWorkspace, err := st.GetWorkspace(runtime.ActiveWorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("load reattach workspace: %w", err)
	}
	if err := orch.Switch(managed.SwitchRequest{TargetWorkspaceID: activeWorkspace.ID}); err != nil {
		return nil, fmt.Errorf("materialize reattach workspace: %w", err)
	}
	activeWorkspace, err = st.GetWorkspace(activeWorkspace.ID)
	if err != nil {
		return nil, fmt.Errorf("reload reattach workspace: %w", err)
	}

	sessID := newManagedSessionID(managed.Source(runtime.Source))
	if err := createManagedSessionLink(st, sessID, runtime.Source, activeWorkspace.WorktreePath, activeWorkspace.ID, now); err != nil {
		return nil, err
	}
	if err := st.UpsertManagedRuntime(store.ManagedRuntime{
		ID:                runtime.ID,
		ProjectPath:       runtime.ProjectPath,
		RootWorkspaceID:   runtime.RootWorkspaceID,
		ActiveWorkspaceID: activeWorkspace.ID,
		ActiveSessionID:   sessID,
		Source:            runtime.Source,
		LaunchArgs:        append([]string(nil), launchArgs...),
		Status:            store.ManagedRuntimeRunning,
		HeartbeatAt:       now,
		CreatedAt:         runtime.CreatedAt,
		UpdatedAt:         now,
	}); err != nil {
		return nil, fmt.Errorf("update reattach runtime: %w", err)
	}
	return &managedStartState{
		runtimeID:         runtime.ID,
		rootWorkspaceID:   runtime.RootWorkspaceID,
		activeWorkspaceID: activeWorkspace.ID,
		activeSessionID:   sessID,
		activeWorktree:    activeWorkspace.WorktreePath,
		reusedRuntime:     true,
	}, nil
}

func createIsolatedManagedStart(st *store.Store, source managed.Source, projectPath string, launchArgs []string, now time.Time) (*managedStartState, error) {
	runtimeID := newRuntimeID()
	wsID := newWorkspaceID()
	sessID := newManagedSessionID(source)
	worktreePath := filepath.Join(managedWorktreeBase(), runtimeID, "root")
	rootRef, err := managed.CaptureWorktreeAsRef(projectPath, wsID, projectPath, "root")
	if err != nil {
		return nil, fmt.Errorf("snapshot current checkout for isolated runtime: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return nil, fmt.Errorf("create managed root dir: %w", err)
	}
	if err := managed.MaterializeRef(rootRef, worktreePath, projectPath); err != nil {
		return nil, fmt.Errorf("materialize isolated root worktree: %w", err)
	}
	if err := st.CreateWorkspace(workspace.Workspace{
		ID:           wsID,
		Status:       workspace.StatusActive,
		ProjectPath:  projectPath,
		WorktreePath: worktreePath,
		GitRef:       rootRef,
		IsRoot:       true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		return nil, fmt.Errorf("create isolated root workspace: %w", err)
	}
	if err := createManagedSessionLink(st, sessID, string(source), worktreePath, wsID, now); err != nil {
		return nil, err
	}
	if err := st.SaveBranchPath(workspace.BranchPathSegment{WorkspaceID: wsID, Depth: 0, Ordinal: 0}); err != nil {
		return nil, fmt.Errorf("save isolated branch path: %w", err)
	}
	if err := st.UpsertManagedRuntime(store.ManagedRuntime{
		ID:                runtimeID,
		ProjectPath:       projectPath,
		RootWorkspaceID:   wsID,
		ActiveWorkspaceID: wsID,
		ActiveSessionID:   sessID,
		Source:            string(source),
		LaunchArgs:        append([]string(nil), launchArgs...),
		Status:            store.ManagedRuntimeRunning,
		HeartbeatAt:       now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		return nil, fmt.Errorf("create isolated runtime: %w", err)
	}
	return &managedStartState{
		runtimeID:         runtimeID,
		rootWorkspaceID:   wsID,
		activeWorkspaceID: wsID,
		activeSessionID:   sessID,
		activeWorktree:    worktreePath,
		isolatedRoot:      true,
	}, nil
}

func createManagedSessionLink(st *store.Store, sessionID, source, projectPath, workspaceID string, now time.Time) error {
	if err := st.CreateSession(event.Session{
		ID:          sessionID,
		Source:      source,
		ProjectPath: projectPath,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	if err := st.LinkWorkspaceSession(workspace.WorkspaceSession{
		WorkspaceID: workspaceID,
		SessionID:   sessionID,
		CreatedAt:   now,
	}); err != nil {
		return fmt.Errorf("link workspace session: %w", err)
	}
	return nil
}

func newWorkspaceID() string {
	return fmt.Sprintf("ws-%s", uuid.New().String()[:8])
}

func newManagedSessionID(source managed.Source) string {
	return fmt.Sprintf("%s-managed-%s", source, uuid.New().String()[:8])
}

func newRuntimeID() string {
	return fmt.Sprintf("rt-%s", uuid.New().String()[:8])
}
