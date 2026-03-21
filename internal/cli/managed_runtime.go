package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/bskyn/peek/internal/companion"
	"github.com/bskyn/peek/internal/managed"
	"github.com/bskyn/peek/internal/store"
	"github.com/bskyn/peek/internal/viewer"
	"github.com/bskyn/peek/internal/workspace"
)

const (
	managedRuntimeHeartbeatInterval   = 500 * time.Millisecond
	managedRuntimeRequestPollInterval = 200 * time.Millisecond
	managedRuntimeRequestTimeout      = 20 * time.Second
	managedRuntimeStopTimeout         = 5 * time.Second
	managedRuntimeStaleAfter          = 3 * time.Second

	managedRequestBranch = "branch"
	managedRequestSwitch = "switch"
)

type staleManagedRuntimeError struct {
	WorkspaceID string
}

func (e *staleManagedRuntimeError) Error() string {
	return fmt.Sprintf("workspace %s has no live managed runtime; restart it with `peek run` before branching or switching", e.WorkspaceID)
}

type managedLaunchConfig struct {
	command string
	env     []string
	stdin   io.Reader
	stdout  io.Writer
	stderr  io.Writer
}

type managedSupervisor struct {
	st                *store.Store
	viewer            *viewer.Runtime
	orch              *managed.Orchestrator
	source            managed.Source
	baseArgs          []string
	projectPath       string
	runtimeID         string
	rootWorkspaceID   string
	activeWorkspaceID string
	activeSessionID   string
	companionMgr      *companion.Manager
	launch            managedLaunchConfig
	hasLaunched       bool
}

var writeManagedTTYControl = func(data []byte) error {
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer tty.Close()
	_, err = tty.Write(data)
	return err
}

func newManagedSupervisor(st *store.Store, rt *viewer.Runtime, orch *managed.Orchestrator, source managed.Source, baseArgs []string, runtimeID, rootWorkspaceID, activeWorkspaceID, activeSessionID, projectDir string, companionSpec *companion.ProjectRuntimeSpec, launch managedLaunchConfig) *managedSupervisor {
	supervisor := &managedSupervisor{
		st:                st,
		viewer:            rt,
		orch:              orch,
		source:            source,
		baseArgs:          append([]string(nil), baseArgs...),
		projectPath:       projectDir,
		runtimeID:         runtimeID,
		rootWorkspaceID:   rootWorkspaceID,
		activeWorkspaceID: activeWorkspaceID,
		activeSessionID:   activeSessionID,
		launch:            launch,
	}
	if companionSpec != nil && len(companionSpec.Services) > 0 {
		supervisor.companionMgr = companion.NewManager(
			st,
			projectDir,
			runtimeID,
			companionSpec,
			func(status companion.StatusSnapshot) {
				if rt == nil {
					return
				}
				status.Browser.PathPrefix = companionSpec.Browser.PathPrefix
				if status.Browser.TargetURL != "" {
					_ = rt.SetProxyTarget(runtimeID, status.Browser.TargetURL)
				}
				if status.Browser.TargetURL == "" {
					_ = rt.SetProxyTarget(runtimeID, "")
				}
				rt.SetRuntimeStatus(runtimeID, status)
			},
		)
	}
	return supervisor
}

func (s *managedSupervisor) Run(ctx context.Context) error {
	now := time.Now().UTC()
	if err := s.st.UpsertManagedRuntime(store.ManagedRuntime{
		ID:                s.runtimeID,
		ProjectPath:       s.projectPath,
		RootWorkspaceID:   s.rootWorkspaceID,
		ActiveWorkspaceID: s.activeWorkspaceID,
		ActiveSessionID:   s.activeSessionID,
		Source:            string(s.source),
		LaunchArgs:        append([]string(nil), s.baseArgs...),
		Status:            store.ManagedRuntimeRunning,
		HeartbeatAt:       now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		return fmt.Errorf("register managed runtime: %w", err)
	}
	defer func() {
		_ = s.st.ParkManagedRuntime(s.runtimeID, s.rootWorkspaceID, s.activeSessionID)
	}()

	activeWorkspace, err := s.st.GetWorkspace(s.activeWorkspaceID)
	if err != nil {
		return fmt.Errorf("load active workspace: %w", err)
	}
	if s.companionMgr != nil {
		if _, err := s.companionMgr.Activate(ctx, activeWorkspace.ID, activeWorkspace.WorktreePath); err != nil {
			return fmt.Errorf("activate companion runtime: %w", err)
		}
	}

	spec := managed.BuildInitialResumeSpec(s.source, s.activeWorkspaceID, s.activeSessionID, activeWorkspace.WorktreePath, s.baseArgs)
	for {
		nextSpec, nextActiveWorkspaceID, nextActiveSessionID, err, done := s.runLaunch(ctx, spec)
		if nextActiveWorkspaceID != "" {
			s.activeWorkspaceID = nextActiveWorkspaceID
		}
		if nextActiveSessionID != "" {
			s.activeSessionID = nextActiveSessionID
		}
		if done {
			return err
		}
		spec = nextSpec
	}
}

func (s *managedSupervisor) runLaunch(ctx context.Context, spec managed.ResumeSpec) (managed.ResumeSpec, string, string, error, bool) {
	_ = s.st.TouchManagedRuntime(s.runtimeID, spec.WorkspaceID, spec.SessionID)
	publishRuntimeSessionSummary(s.st, s.viewer, s.runtimeID, spec.SessionID)
	if s.viewer != nil {
		s.viewer.SetCurrentRuntimeID(s.runtimeID)
		s.viewer.SetActiveSessionID(s.runtimeID, spec.SessionID)
	}
	if s.hasLaunched {
		clearManagedTerminal()
		fmt.Fprintf(os.Stdout, "Peek: switched to workspace %s (%s)\n\n", spec.WorkspaceID, spec.SessionID)
	}
	s.hasLaunched = true

	launchCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	run := managed.New(managed.RunRequest{
		Source:     s.source,
		Command:    s.launch.command,
		ProjectDir: spec.WorktreePath,
		Args:       spec.CommandArgs(),
		Env:        s.launch.env,
		Stdin:      s.launch.stdin,
		Stdout:     s.launch.stdout,
		Stderr:     s.launch.stderr,
	})
	run.WorkspaceID = spec.WorkspaceID

	if err := run.Start(launchCtx); err != nil {
		_ = s.st.UpdateWorkspaceStatus(spec.WorkspaceID, workspace.StatusFrozen)
		return spec, spec.WorkspaceID, spec.SessionID, fmt.Errorf("start managed runtime: %w", err), true
	}

	checkpoints := managed.NewCheckpointEngine(s.st, spec.WorkspaceID, spec.SessionID, spec.WorktreePath)
	go tailManagedLaunch(launchCtx, s.st, s.viewer, s.runtimeID, spec, checkpoints)

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- run.Wait()
	}()

	heartbeatTicker := time.NewTicker(managedRuntimeHeartbeatInterval)
	defer heartbeatTicker.Stop()
	requestTicker := time.NewTicker(managedRuntimeRequestPollInterval)
	defer requestTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			stopCtx, stopCancel := context.WithTimeout(context.Background(), managedRuntimeStopTimeout)
			err := run.StopGracefully(stopCtx)
			stopCancel()
			_ = s.st.UpdateWorkspaceStatus(spec.WorkspaceID, workspace.StatusFrozen)
			return spec, spec.WorkspaceID, spec.SessionID, managed.WrapRunExitError(s.source, err), true

		case <-heartbeatTicker.C:
			_ = s.st.TouchManagedRuntime(s.runtimeID, spec.WorkspaceID, spec.SessionID)

		case <-requestTicker.C:
			req, err := s.st.ClaimNextManagedRuntimeRequest(s.runtimeID)
			if err != nil || req == nil {
				continue
			}

			stopCtx, stopCancel := context.WithTimeout(context.Background(), managedRuntimeStopTimeout)
			_ = run.StopGracefully(stopCtx)
			stopCancel()
			cancel()

			nextSpec, resp, applyErr := s.applyRequest(req)
			if applyErr != nil {
				_ = s.st.FailManagedRuntimeRequest(req.ID, applyErr.Error())
				return spec, spec.WorkspaceID, spec.SessionID, nil, false
			}

			_ = s.st.TouchManagedRuntime(s.runtimeID, nextSpec.WorkspaceID, nextSpec.SessionID)
			_ = s.st.CompleteManagedRuntimeRequest(req.ID, resp)
			return nextSpec, nextSpec.WorkspaceID, nextSpec.SessionID, nil, false

		case err := <-waitCh:
			cancel()
			_ = s.st.UpdateWorkspaceStatus(spec.WorkspaceID, workspace.StatusFrozen)
			return spec, spec.WorkspaceID, spec.SessionID, managed.WrapRunExitError(s.source, err), true
		}
	}
}

func (s *managedSupervisor) applyRequest(req *store.ManagedRuntimeRequest) (managed.ResumeSpec, store.ManagedRuntimeResponse, error) {
	switch req.Kind {
	case managedRequestBranch:
		if req.SourceWorkspaceID != s.activeWorkspaceID {
			return managed.ResumeSpec{}, store.ManagedRuntimeResponse{}, fmt.Errorf("workspace %s is not active in the live runtime", req.SourceWorkspaceID)
		}
		if req.BranchFromSeq == nil {
			return managed.ResumeSpec{}, store.ManagedRuntimeResponse{}, fmt.Errorf("branch request is missing a sequence number")
		}

		result, err := s.orch.Branch(managed.BranchRequest{
			SourceWorkspaceID: req.SourceWorkspaceID,
			BranchFromSeq:     *req.BranchFromSeq,
		})
		if err != nil {
			return managed.ResumeSpec{}, store.ManagedRuntimeResponse{}, err
		}

		spec, err := managed.BuildBranchResumeSpec(
			s.st,
			s.source,
			s.baseArgs,
			result.NewWorkspaceID,
			result.NewSessionID,
			result.WorktreePath,
			result.Anchor,
		)
		if err != nil {
			return managed.ResumeSpec{}, store.ManagedRuntimeResponse{}, err
		}
		if s.companionMgr != nil {
			if _, err := s.companionMgr.Activate(context.Background(), result.NewWorkspaceID, result.WorktreePath); err != nil {
				currentWorkspace, currentErr := s.st.GetWorkspace(s.activeWorkspaceID)
				if currentErr == nil {
					_, _ = s.companionMgr.Activate(context.Background(), currentWorkspace.ID, currentWorkspace.WorktreePath)
				}
				return managed.ResumeSpec{}, store.ManagedRuntimeResponse{}, err
			}
		}

		return spec, store.ManagedRuntimeResponse{
			WorkspaceID:  result.NewWorkspaceID,
			SessionID:    result.NewSessionID,
			WorktreePath: result.WorktreePath,
			GitRef:       result.GitRef,
		}, nil

	case managedRequestSwitch:
		if req.TargetWorkspaceID == "" {
			return managed.ResumeSpec{}, store.ManagedRuntimeResponse{}, fmt.Errorf("switch request is missing a target workspace")
		}
		if err := s.orch.Switch(managed.SwitchRequest{TargetWorkspaceID: req.TargetWorkspaceID}); err != nil {
			return managed.ResumeSpec{}, store.ManagedRuntimeResponse{}, err
		}

		targetWorkspace, err := s.st.GetWorkspace(req.TargetWorkspaceID)
		if err != nil {
			return managed.ResumeSpec{}, store.ManagedRuntimeResponse{}, err
		}
		targetSession, err := s.st.GetLatestWorkspaceSession(req.TargetWorkspaceID)
		if err != nil {
			return managed.ResumeSpec{}, store.ManagedRuntimeResponse{}, err
		}

		spec, err := managed.BuildSwitchResumeSpec(
			s.st,
			s.source,
			s.baseArgs,
			targetWorkspace.ID,
			targetWorkspace.WorktreePath,
			targetSession,
		)
		if err != nil {
			return managed.ResumeSpec{}, store.ManagedRuntimeResponse{}, err
		}
		if s.companionMgr != nil {
			if _, err := s.companionMgr.Activate(context.Background(), targetWorkspace.ID, targetWorkspace.WorktreePath); err != nil {
				currentWorkspace, currentErr := s.st.GetWorkspace(s.activeWorkspaceID)
				if currentErr == nil {
					_, _ = s.companionMgr.Activate(context.Background(), currentWorkspace.ID, currentWorkspace.WorktreePath)
				}
				return managed.ResumeSpec{}, store.ManagedRuntimeResponse{}, err
			}
		}

		return spec, store.ManagedRuntimeResponse{
			WorkspaceID:  targetWorkspace.ID,
			SessionID:    targetSession.ID,
			WorktreePath: targetWorkspace.WorktreePath,
			GitRef:       targetWorkspace.GitRef,
		}, nil
	}

	return managed.ResumeSpec{}, store.ManagedRuntimeResponse{}, fmt.Errorf("unsupported runtime request kind %q", req.Kind)
}

func enqueueManagedBranchRequest(st *store.Store, workspaceID string, seq int64) (*store.ManagedRuntimeRequest, error) {
	runtime, err := liveManagedRuntimeForWorkspace(st, workspaceID)
	if err != nil {
		return nil, err
	}

	reqSeq := seq
	now := time.Now().UTC()
	req := store.ManagedRuntimeRequest{
		ID:                "rtreq-" + uuid.New().String(),
		RuntimeID:         runtime.ID,
		Kind:              managedRequestBranch,
		SourceWorkspaceID: workspaceID,
		BranchFromSeq:     &reqSeq,
		Status:            store.ManagedRuntimeRequestPending,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := st.CreateManagedRuntimeRequest(req); err != nil {
		return nil, err
	}
	return waitForManagedRuntimeRequest(st, req.ID)
}

func enqueueManagedSwitchRequest(st *store.Store, workspaceID string) (*store.ManagedRuntimeRequest, error) {
	runtime, err := liveManagedRuntimeForWorkspace(st, workspaceID)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	req := store.ManagedRuntimeRequest{
		ID:                "rtreq-" + uuid.New().String(),
		RuntimeID:         runtime.ID,
		Kind:              managedRequestSwitch,
		TargetWorkspaceID: workspaceID,
		Status:            store.ManagedRuntimeRequestPending,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := st.CreateManagedRuntimeRequest(req); err != nil {
		return nil, err
	}
	return waitForManagedRuntimeRequest(st, req.ID)
}

func waitForManagedRuntimeRequest(st *store.Store, requestID string) (*store.ManagedRuntimeRequest, error) {
	deadline := time.Now().Add(managedRuntimeRequestTimeout)
	for time.Now().Before(deadline) {
		req, err := st.GetManagedRuntimeRequest(requestID)
		if err != nil {
			return nil, err
		}
		switch req.Status {
		case store.ManagedRuntimeRequestCompleted:
			return req, nil
		case store.ManagedRuntimeRequestFailed:
			return nil, fmt.Errorf("%s", req.Error)
		}
		time.Sleep(managedRuntimeRequestPollInterval)
	}
	return nil, fmt.Errorf("timed out waiting for the managed runtime to apply request %s", requestID)
}

func liveManagedRuntimeForWorkspace(st *store.Store, workspaceID string) (*store.ManagedRuntime, error) {
	rt, err := st.GetManagedRuntimeForWorkspace(workspaceID)
	if err != nil {
		return nil, &staleManagedRuntimeError{WorkspaceID: workspaceID}
	}
	if rt.Status != store.ManagedRuntimeRunning || time.Since(rt.HeartbeatAt) > managedRuntimeStaleAfter {
		return nil, &staleManagedRuntimeError{WorkspaceID: workspaceID}
	}
	return rt, nil
}

func managedWorktreeBase() string {
	if dbPath == "" {
		return filepath.Join(os.TempDir(), "peek-worktrees")
	}
	return filepath.Join(filepath.Dir(dbPath), "worktrees")
}

func clearManagedTerminal() {
	const sequence = "\x1b[?1049l\x1b[?25h\x1b[2J\x1b[3J\x1b[H"
	_ = writeManagedTTYControl([]byte(sequence))
}
