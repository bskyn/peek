package companion

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bskyn/peek/internal/store"
)

const serviceStopTimeout = 3 * time.Second

type ActivationPhase string

const (
	ActivationIdle          ActivationPhase = "idle"
	ActivationMaterializing ActivationPhase = "materializing"
	ActivationBootstrapping ActivationPhase = "bootstrapping"
	ActivationStarting      ActivationPhase = "starting"
	ActivationReady         ActivationPhase = "ready"
	ActivationFailed        ActivationPhase = "failed"
)

type BootstrapSummary struct {
	Status      store.BootstrapStatus `json:"status"`
	Fingerprint string                `json:"fingerprint,omitempty"`
	Reused      bool                  `json:"reused,omitempty"`
	LastError   string                `json:"last_error,omitempty"`
}

type ServiceSummary struct {
	Name      string                       `json:"name"`
	Role      string                       `json:"role"`
	Status    store.CompanionServiceStatus `json:"status"`
	TargetURL string                       `json:"target_url,omitempty"`
	LastError string                       `json:"last_error,omitempty"`
}

type BrowserSummary struct {
	PathPrefix string `json:"path_prefix"`
	TargetURL  string `json:"target_url,omitempty"`
}

type StatusSnapshot struct {
	Enabled           bool             `json:"enabled"`
	ConfigSource      string           `json:"config_source,omitempty"`
	ActiveWorkspaceID string           `json:"active_workspace_id,omitempty"`
	Phase             ActivationPhase  `json:"phase"`
	Message           string           `json:"message,omitempty"`
	Bootstrap         BootstrapSummary `json:"bootstrap"`
	Services          []ServiceSummary `json:"services,omitempty"`
	Browser           BrowserSummary   `json:"browser"`
	UpdatedAt         time.Time        `json:"updated_at"`
}

type ActivationResult struct {
	PrimaryTargetURL string
}

type Manager struct {
	st        *store.Store
	repoDir   string
	runtimeID string
	spec      *ProjectRuntimeSpec
	onStatus  func(StatusSnapshot)

	mu      sync.RWMutex
	status  StatusSnapshot
	handles map[string]*serviceHandle
}

type serviceHandle struct {
	spec   CompanionServiceSpec
	cmd    *exec.Cmd
	done   chan struct{}
	err    error
	target string
}

type serviceStartResult struct {
	summary ServiceSummary
	handle  *serviceHandle
}

func NewManager(st *store.Store, repoDir, runtimeID string, spec *ProjectRuntimeSpec, onStatus func(StatusSnapshot)) *Manager {
	if spec == nil {
		return nil
	}
	manager := &Manager{
		st:        st,
		repoDir:   repoDir,
		runtimeID: runtimeID,
		spec:      spec,
		onStatus:  onStatus,
		handles:   make(map[string]*serviceHandle),
		status: StatusSnapshot{
			Enabled:      true,
			ConfigSource: spec.ConfigSource,
			Phase:        ActivationIdle,
			Browser: BrowserSummary{
				PathPrefix: spec.Browser.PathPrefix,
			},
			UpdatedAt: time.Now().UTC(),
		},
	}
	manager.emitStatusLocked()
	return manager
}

func (m *Manager) Snapshot() StatusSnapshot {
	if m == nil {
		return StatusSnapshot{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneSnapshot(m.status)
}

func (m *Manager) Activate(ctx context.Context, workspaceID, worktreePath string) (ActivationResult, error) {
	if m == nil {
		return ActivationResult{}, nil
	}

	m.setPhase(workspaceID, ActivationMaterializing, "materializing workspace assets")
	if err := m.materialize(worktreePath); err != nil {
		m.fail(workspaceID, fmt.Errorf("materialize workspace assets: %w", err))
		return ActivationResult{}, err
	}

	fingerprint, err := FingerprintInputs(worktreePath, m.spec)
	if err != nil {
		m.fail(workspaceID, fmt.Errorf("compute bootstrap fingerprint: %w", err))
		return ActivationResult{}, err
	}

	m.setPhase(workspaceID, ActivationBootstrapping, "checking bootstrap state")
	reused, err := m.bootstrap(ctx, workspaceID, worktreePath, fingerprint)
	if err != nil {
		m.fail(workspaceID, err)
		return ActivationResult{}, err
	}

	m.setPhase(workspaceID, ActivationStarting, "starting companion services")
	if err := m.stopServices(ctx); err != nil {
		m.fail(workspaceID, fmt.Errorf("stop previous companion services: %w", err))
		return ActivationResult{}, err
	}

	serviceSummaries, primaryURL, err := m.startServices(ctx, workspaceID, worktreePath)
	if err != nil {
		m.fail(workspaceID, err)
		return ActivationResult{}, err
	}

	m.mu.Lock()
	m.status.ActiveWorkspaceID = workspaceID
	m.status.Phase = ActivationReady
	m.status.Message = "primary app ready"
	m.status.Bootstrap.Reused = reused
	m.status.Bootstrap.Fingerprint = fingerprint
	m.status.Services = serviceSummaries
	m.status.Browser.TargetURL = primaryURL
	m.status.UpdatedAt = time.Now().UTC()
	m.emitStatusLocked()
	m.mu.Unlock()
	return ActivationResult{PrimaryTargetURL: primaryURL}, nil
}

func (m *Manager) Stop(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if err := m.stopServices(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	m.status.Phase = ActivationIdle
	m.status.Message = ""
	m.status.Services = nil
	m.status.Browser.TargetURL = ""
	m.status.UpdatedAt = time.Now().UTC()
	m.emitStatusLocked()
	m.mu.Unlock()
	return nil
}

func (m *Manager) setPhase(workspaceID string, phase ActivationPhase, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.ActiveWorkspaceID = workspaceID
	m.status.Phase = phase
	m.status.Message = message
	m.status.UpdatedAt = time.Now().UTC()
	m.emitStatusLocked()
}

func (m *Manager) fail(workspaceID string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.ActiveWorkspaceID = workspaceID
	m.status.Phase = ActivationFailed
	m.status.Message = err.Error()
	m.status.Bootstrap.LastError = err.Error()
	for i := range m.status.Services {
		if m.status.Services[i].Status != store.CompanionServiceReady {
			m.status.Services[i].Status = store.CompanionServiceFailed
			m.status.Services[i].LastError = err.Error()
		}
	}
	m.status.UpdatedAt = time.Now().UTC()
	m.emitStatusLocked()
}

func (m *Manager) materialize(worktreePath string) error {
	for _, env := range m.spec.EnvSources {
		sourcePath := filepath.Join(m.repoDir, env.Path)
		targetRel := env.Target
		if targetRel == "" {
			targetRel = env.Path
		}
		targetPath := filepath.Join(worktreePath, targetRel)
		data, err := os.ReadFile(sourcePath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && env.Optional {
				continue
			}
			return fmt.Errorf("read %s: %w", env.Path, err)
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return fmt.Errorf("create %s: %w", filepath.Dir(targetPath), err)
		}
		if err := os.WriteFile(targetPath, data, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", targetRel, err)
		}
	}
	return nil
}

func (m *Manager) bootstrap(ctx context.Context, workspaceID, worktreePath, fingerprint string) (bool, error) {
	now := time.Now().UTC()
	state, err := m.st.GetWorkspaceBootstrapState(workspaceID)
	if err == nil && state.Fingerprint == fingerprint && state.Status == store.BootstrapSucceeded {
		m.mu.Lock()
		m.status.Bootstrap = BootstrapSummary{
			Status:      store.BootstrapSucceeded,
			Fingerprint: fingerprint,
			Reused:      true,
		}
		m.status.UpdatedAt = now
		m.emitStatusLocked()
		m.mu.Unlock()
		return true, nil
	}

	if err := m.st.UpsertWorkspaceBootstrapState(store.WorkspaceBootstrapState{
		WorkspaceID: workspaceID,
		Fingerprint: fingerprint,
		Status:      store.BootstrapRunning,
		StartedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		return false, fmt.Errorf("persist bootstrap state: %w", err)
	}
	m.mu.Lock()
	m.status.Bootstrap = BootstrapSummary{
		Status:      store.BootstrapRunning,
		Fingerprint: fingerprint,
	}
	m.status.UpdatedAt = now
	m.emitStatusLocked()
	m.mu.Unlock()

	for _, cmd := range m.spec.Bootstrap.Commands {
		command := exec.CommandContext(ctx, cmd.Command[0], cmd.Command[1:]...)
		command.Dir = joinWorkdir(worktreePath, cmd.Workdir)
		command.Env = mergeEnv(cmd.Env)
		command.Stdout = io.Discard
		command.Stderr = io.Discard
		if err := command.Run(); err != nil {
			finishedAt := time.Now().UTC()
			_ = m.st.UpsertWorkspaceBootstrapState(store.WorkspaceBootstrapState{
				WorkspaceID: workspaceID,
				Fingerprint: fingerprint,
				Status:      store.BootstrapFailed,
				LastError:   err.Error(),
				StartedAt:   now,
				FinishedAt:  finishedAt,
				UpdatedAt:   finishedAt,
			})
			return false, fmt.Errorf("bootstrap command %q failed: %w", strings.Join(cmd.Command, " "), err)
		}
	}

	finishedAt := time.Now().UTC()
	if err := m.st.UpsertWorkspaceBootstrapState(store.WorkspaceBootstrapState{
		WorkspaceID: workspaceID,
		Fingerprint: fingerprint,
		Status:      store.BootstrapSucceeded,
		StartedAt:   now,
		FinishedAt:  finishedAt,
		UpdatedAt:   finishedAt,
	}); err != nil {
		return false, fmt.Errorf("persist bootstrap success: %w", err)
	}
	m.mu.Lock()
	m.status.Bootstrap = BootstrapSummary{
		Status:      store.BootstrapSucceeded,
		Fingerprint: fingerprint,
	}
	m.status.UpdatedAt = finishedAt
	m.emitStatusLocked()
	m.mu.Unlock()
	return false, nil
}

func (m *Manager) startServices(ctx context.Context, workspaceID, worktreePath string) ([]ServiceSummary, string, error) {
	results := make([]serviceStartResult, 0, len(m.spec.Services))
	states := make([]store.CompanionServiceState, 0, len(m.spec.Services))
	primaryURL := ""

	for _, service := range m.spec.Services {
		command := exec.CommandContext(ctx, service.Command[0], service.Command[1:]...)
		command.Dir = joinWorkdir(worktreePath, service.Workdir)
		command.Env = mergeEnv(service.Env)
		command.Stdout = io.Discard
		command.Stderr = io.Discard

		handle := &serviceHandle{
			spec:   service,
			cmd:    command,
			done:   make(chan struct{}),
			target: serviceTargetURL(service),
		}
		if err := command.Start(); err != nil {
			return nil, "", fmt.Errorf("start service %s: %w", service.Name, err)
		}

		go func(h *serviceHandle) {
			h.err = h.cmd.Wait()
			close(h.done)
		}(handle)

		startedAt := time.Now().UTC()
		summary := ServiceSummary{
			Name:      service.Name,
			Role:      service.Role,
			Status:    store.CompanionServiceStarting,
			TargetURL: serviceTargetURL(service),
		}
		results = append(results, serviceStartResult{summary: summary, handle: handle})
		states = append(states, store.CompanionServiceState{
			RuntimeID:   m.runtimeID,
			WorkspaceID: workspaceID,
			ServiceName: service.Name,
			Role:        service.Role,
			Status:      store.CompanionServiceStarting,
			TargetURL:   serviceTargetURL(service),
			StartedAt:   startedAt,
			UpdatedAt:   startedAt,
		})
		if err := m.st.ReplaceCompanionServiceStates(m.runtimeID, states); err != nil {
			return nil, "", fmt.Errorf("persist service state: %w", err)
		}
		m.publishServiceSummaries(results)

		if err := waitForReady(ctx, handle, worktreePath, service); err != nil {
			summary.Status = store.CompanionServiceFailed
			summary.LastError = err.Error()
			results[len(results)-1].summary = summary
			states[len(states)-1].Status = store.CompanionServiceFailed
			states[len(states)-1].LastError = err.Error()
			states[len(states)-1].UpdatedAt = time.Now().UTC()
			_ = m.st.ReplaceCompanionServiceStates(m.runtimeID, states)
			_ = m.stopHandles(context.Background(), results)
			return nil, "", fmt.Errorf("service %s readiness failed: %w", service.Name, err)
		}

		readyAt := time.Now().UTC()
		summary.Status = store.CompanionServiceReady
		results[len(results)-1].summary = summary
		states[len(states)-1].Status = store.CompanionServiceReady
		states[len(states)-1].ReadyAt = readyAt
		states[len(states)-1].UpdatedAt = readyAt
		if err := m.st.ReplaceCompanionServiceStates(m.runtimeID, states); err != nil {
			return nil, "", fmt.Errorf("persist service readiness: %w", err)
		}
		m.publishServiceSummaries(results)

		if service.Role == ServiceRolePrimary {
			primaryURL = serviceTargetURL(service)
		}
	}

	m.mu.Lock()
	m.handles = make(map[string]*serviceHandle, len(results))
	summaries := make([]ServiceSummary, 0, len(results))
	for _, result := range results {
		m.handles[result.summary.Name] = result.handle
		summaries = append(summaries, result.summary)
	}
	m.mu.Unlock()
	return summaries, primaryURL, nil
}

func waitForReady(ctx context.Context, handle *serviceHandle, worktreePath string, service CompanionServiceSpec) error {
	probe := service.Ready
	timeout := time.Duration(probe.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	interval := time.Duration(probe.IntervalMillis) * time.Millisecond
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}

	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client := &http.Client{Timeout: 1 * time.Second}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-deadlineCtx.Done():
			return fmt.Errorf("timed out waiting for %s", probe.URL)
		case <-handle.done:
			if handle.err == nil {
				return errors.New("process exited before readiness")
			}
			return handle.err
		case <-ticker.C:
			switch probe.Type {
			case ProbeTypeHTTP:
				resp, err := client.Get(probe.URL)
				if err != nil {
					continue
				}
				body, readErr := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 500 {
					if probe.SuccessContains == "" {
						return nil
					}
					if readErr == nil && strings.Contains(string(body), probe.SuccessContains) {
						return nil
					}
				}
			case ProbeTypeFile:
				if _, err := os.Stat(filepath.Join(joinWorkdir(worktreePath, service.Workdir), probe.Path)); err == nil {
					return nil
				}
			}
		}
	}
}

func (m *Manager) stopServices(ctx context.Context) error {
	m.mu.Lock()
	handles := make(map[string]*serviceHandle, len(m.handles))
	for name, handle := range m.handles {
		handles[name] = handle
	}
	m.handles = make(map[string]*serviceHandle)
	m.mu.Unlock()

	if len(handles) == 0 {
		return m.st.ReplaceCompanionServiceStates(m.runtimeID, nil)
	}

	results := make([]store.CompanionServiceState, 0, len(handles))
	names := make([]string, 0, len(handles))
	for name := range handles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		handle := handles[name]
		stopCtx, cancel := context.WithTimeout(ctx, serviceStopTimeout)
		_ = stopHandle(stopCtx, handle)
		cancel()
		now := time.Now().UTC()
		results = append(results, store.CompanionServiceState{
			RuntimeID:   m.runtimeID,
			WorkspaceID: m.status.ActiveWorkspaceID,
			ServiceName: handle.spec.Name,
			Role:        handle.spec.Role,
			Status:      store.CompanionServiceStopped,
			TargetURL:   handle.target,
			StoppedAt:   now,
			UpdatedAt:   now,
		})
	}
	if err := m.st.ReplaceCompanionServiceStates(m.runtimeID, results); err != nil {
		return err
	}
	return nil
}

func (m *Manager) stopHandles(ctx context.Context, results []serviceStartResult) error {
	states := make([]store.CompanionServiceState, 0, len(results))
	for _, result := range results {
		stopCtx, cancel := context.WithTimeout(ctx, serviceStopTimeout)
		_ = stopHandle(stopCtx, result.handle)
		cancel()
		now := time.Now().UTC()
		states = append(states, store.CompanionServiceState{
			RuntimeID:   m.runtimeID,
			WorkspaceID: m.status.ActiveWorkspaceID,
			ServiceName: result.summary.Name,
			Role:        result.summary.Role,
			Status:      store.CompanionServiceStopped,
			TargetURL:   result.summary.TargetURL,
			StoppedAt:   now,
			UpdatedAt:   now,
		})
	}
	return m.st.ReplaceCompanionServiceStates(m.runtimeID, states)
}

func stopHandle(ctx context.Context, handle *serviceHandle) error {
	if handle == nil || handle.cmd == nil || handle.cmd.Process == nil {
		return nil
	}
	_ = handle.cmd.Process.Signal(syscall.SIGINT)
	select {
	case <-handle.done:
		return nil
	case <-ctx.Done():
		_ = handle.cmd.Process.Kill()
		<-handle.done
	}
	return nil
}

func (m *Manager) publishServiceSummaries(results []serviceStartResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	summaries := make([]ServiceSummary, 0, len(results))
	for _, result := range results {
		summaries = append(summaries, result.summary)
	}
	m.status.Services = summaries
	m.status.UpdatedAt = time.Now().UTC()
	m.emitStatusLocked()
}

func (m *Manager) emitStatusLocked() {
	if m.onStatus != nil {
		m.onStatus(cloneSnapshot(m.status))
	}
}

func cloneSnapshot(snapshot StatusSnapshot) StatusSnapshot {
	cloned := snapshot
	if snapshot.Services != nil {
		cloned.Services = append([]ServiceSummary(nil), snapshot.Services...)
	}
	return cloned
}

func joinWorkdir(root, rel string) string {
	if rel == "" {
		return root
	}
	return filepath.Join(root, rel)
}

func mergeEnv(extra map[string]string) []string {
	env := append([]string(nil), os.Environ()...)
	if len(extra) == 0 {
		return env
	}
	keys := make([]string, 0, len(extra))
	for key := range extra {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		env = append(env, key+"="+extra[key])
	}
	return env
}

func serviceTargetURL(service CompanionServiceSpec) string {
	if service.TargetURL != "" {
		return service.TargetURL
	}
	return service.Ready.URL
}
