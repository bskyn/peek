package companion

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
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
	pid    int
	target string
}

type serviceStartResult struct {
	summary ServiceSummary
	handle  *serviceHandle
}

type resolvedServiceSpec struct {
	CompanionServiceSpec
	TargetURL string
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
	manager.loadPersistedState()
	manager.emitStatusLocked()
	return manager
}

func (m *Manager) loadPersistedState() {
	if runtime, err := m.st.GetDetachedCompanionRuntime(m.runtimeID); err == nil {
		m.status.ConfigSource = runtime.ConfigSource
		m.status.ActiveWorkspaceID = runtime.ActiveWorkspaceID
		m.status.Phase = ActivationPhase(runtime.Phase)
		m.status.Message = runtime.Message
		m.status.Browser.PathPrefix = runtime.BrowserPathPrefix
		m.status.Browser.TargetURL = runtime.BrowserTargetURL
		m.status.UpdatedAt = runtime.UpdatedAt
	}
	states, err := m.st.ListCompanionServiceStates(m.runtimeID)
	if err != nil {
		return
	}
	m.status.Services = make([]ServiceSummary, 0, len(states))
	for _, state := range states {
		m.status.Services = append(m.status.Services, ServiceSummary{
			Name:      state.ServiceName,
			Role:      state.Role,
			Status:    state.Status,
			TargetURL: state.TargetURL,
			LastError: state.LastError,
		})
	}
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
	if result, reused, err := m.tryReuseDetachedRuntime(workspaceID); err != nil {
		m.fail(workspaceID, err)
		return ActivationResult{}, err
	} else if reused {
		return result, nil
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
	m.persistDetachedRuntimeLocked()
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
	m.persistDetachedRuntimeLocked()
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
	m.persistDetachedRuntimeLocked()
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
	m.persistDetachedRuntimeLocked()
	m.emitStatusLocked()
}

func (m *Manager) tryReuseDetachedRuntime(workspaceID string) (ActivationResult, bool, error) {
	runtime, err := m.st.GetDetachedCompanionRuntime(m.runtimeID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ActivationResult{}, false, nil
		}
		return ActivationResult{}, false, fmt.Errorf("load detached runtime: %w", err)
	}
	if runtime.ActiveWorkspaceID != workspaceID {
		return ActivationResult{}, false, nil
	}
	states, err := m.st.ListCompanionServiceStates(m.runtimeID)
	if err != nil {
		return ActivationResult{}, false, fmt.Errorf("load detached service state: %w", err)
	}
	if len(states) == 0 {
		return ActivationResult{}, false, nil
	}
	summaries := make([]ServiceSummary, 0, len(states))
	for _, state := range states {
		if state.PID <= 0 || !processAlive(state.PID) || state.Status != store.CompanionServiceReady {
			return ActivationResult{}, false, nil
		}
		summaries = append(summaries, ServiceSummary{
			Name:      state.ServiceName,
			Role:      state.Role,
			Status:    state.Status,
			TargetURL: state.TargetURL,
			LastError: state.LastError,
		})
	}
	m.mu.Lock()
	m.status.ActiveWorkspaceID = runtime.ActiveWorkspaceID
	m.status.Phase = ActivationReady
	m.status.Message = "detached runtime reattached"
	m.status.Bootstrap.Status = store.BootstrapSucceeded
	m.status.Bootstrap.Reused = true
	m.status.Services = summaries
	m.status.Browser.PathPrefix = runtime.BrowserPathPrefix
	m.status.Browser.TargetURL = runtime.BrowserTargetURL
	m.status.UpdatedAt = time.Now().UTC()
	m.persistDetachedRuntimeLocked()
	m.emitStatusLocked()
	m.mu.Unlock()
	return ActivationResult{PrimaryTargetURL: runtime.BrowserTargetURL}, true, nil
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
		resolved, err := m.resolveServiceSpec(service)
		if err != nil {
			return nil, "", err
		}
		command := exec.Command(service.Command[0], service.Command[1:]...)
		command.Dir = joinWorkdir(worktreePath, service.Workdir)
		command.Env = mergeEnv(resolved.Env)
		command.Stdout = io.Discard
		command.Stderr = io.Discard
		configureServiceCommand(command)

		handle := &serviceHandle{
			spec:   service,
			cmd:    command,
			done:   make(chan struct{}),
			target: resolved.TargetURL,
		}
		if err := command.Start(); err != nil {
			return nil, "", fmt.Errorf("start service %s: %w", service.Name, err)
		}
		handle.pid = command.Process.Pid

		go func(h *serviceHandle) {
			h.err = h.cmd.Wait()
			close(h.done)
		}(handle)

		startedAt := time.Now().UTC()
		summary := ServiceSummary{
			Name:      service.Name,
			Role:      service.Role,
			Status:    store.CompanionServiceStarting,
			TargetURL: resolved.TargetURL,
		}
		results = append(results, serviceStartResult{summary: summary, handle: handle})
		states = append(states, store.CompanionServiceState{
			RuntimeID:   m.runtimeID,
			WorkspaceID: workspaceID,
			ServiceName: service.Name,
			Role:        service.Role,
			Status:      store.CompanionServiceStarting,
			PID:         handle.pid,
			TargetURL:   resolved.TargetURL,
			StartedAt:   startedAt,
			UpdatedAt:   startedAt,
		})
		if err := m.st.ReplaceCompanionServiceStates(m.runtimeID, states); err != nil {
			return nil, "", fmt.Errorf("persist service state: %w", err)
		}
		m.publishServiceSummaries(results)

		if err := waitForReady(ctx, handle, worktreePath, resolved); err != nil {
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
			primaryURL = resolved.TargetURL
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

func waitForReady(ctx context.Context, handle *serviceHandle, worktreePath string, service resolvedServiceSpec) error {
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
		states, err := m.st.ListCompanionServiceStates(m.runtimeID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		if len(states) == 0 {
			return nil
		}
		results := make([]store.CompanionServiceState, 0, len(states))
		for _, state := range states {
			stopCtx, cancel := context.WithTimeout(ctx, serviceStopTimeout)
			_ = stopPID(stopCtx, state.PID)
			cancel()
			now := time.Now().UTC()
			results = append(results, store.CompanionServiceState{
				RuntimeID:   state.RuntimeID,
				WorkspaceID: state.WorkspaceID,
				ServiceName: state.ServiceName,
				Role:        state.Role,
				Status:      store.CompanionServiceStopped,
				TargetURL:   state.TargetURL,
				StoppedAt:   now,
				UpdatedAt:   now,
			})
		}
		return m.st.ReplaceCompanionServiceStates(m.runtimeID, results)
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
			PID:         0,
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
			PID:         0,
			TargetURL:   result.summary.TargetURL,
			StoppedAt:   now,
			UpdatedAt:   now,
		})
	}
	return m.st.ReplaceCompanionServiceStates(m.runtimeID, states)
}

func stopHandle(ctx context.Context, handle *serviceHandle) error {
	if handle == nil {
		return nil
	}
	if handle.pid > 0 {
		return stopPID(ctx, handle.pid)
	}
	if handle.cmd == nil || handle.cmd.Process == nil {
		return nil
	}
	_ = interruptProcess(handle.cmd.Process.Pid)
	select {
	case <-handle.done:
		return nil
	case <-ctx.Done():
		_ = killProcess(handle.cmd.Process.Pid)
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
	m.persistDetachedRuntimeLocked()
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

func (m *Manager) persistDetachedRuntimeLocked() {
	_ = m.st.UpsertDetachedCompanionRuntime(store.DetachedCompanionRuntime{
		RuntimeID:         m.runtimeID,
		ActiveWorkspaceID: m.status.ActiveWorkspaceID,
		ConfigSource:      m.status.ConfigSource,
		Phase:             string(m.status.Phase),
		Message:           m.status.Message,
		BrowserPathPrefix: m.status.Browser.PathPrefix,
		BrowserTargetURL:  m.status.Browser.TargetURL,
		UpdatedAt:         m.status.UpdatedAt,
	})
}

func (m *Manager) resolveServiceSpec(service CompanionServiceSpec) (resolvedServiceSpec, error) {
	lease, err := m.ensurePortLease(service.Name)
	if err != nil {
		return resolvedServiceSpec{}, fmt.Errorf("ensure port lease for %s: %w", service.Name, err)
	}
	resolved := resolvedServiceSpec{CompanionServiceSpec: service}
	resolved.Env = copyEnvMap(service.Env)
	if resolved.Env == nil {
		resolved.Env = make(map[string]string)
	}
	resolved.Env["PORT"] = strconv.Itoa(lease.Port)
	resolved.Env["PEEK_PORT"] = strconv.Itoa(lease.Port)
	if _, ok := resolved.Env["HOST"]; !ok {
		resolved.Env["HOST"] = lease.Host
	}
	resolved.Ready = service.Ready
	resolved.Ready.URL = rewriteURLPort(service.Ready.URL, lease.Host, lease.Port)
	resolved.TargetURL = rewriteURLPort(defaultServiceTargetURL(service), lease.Host, lease.Port)
	return resolved, nil
}

func (m *Manager) ensurePortLease(serviceName string) (*store.PortLease, error) {
	if lease, err := m.st.GetPortLease(m.runtimeID, serviceName); err == nil {
		if portAppearsAvailable(lease.Host, lease.Port) {
			return lease, nil
		}
		if err := m.st.DeletePortLease(m.runtimeID, serviceName); err != nil {
			return nil, fmt.Errorf("release stale port lease for %s: %w", serviceName, err)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	startPort, err := m.nextAvailablePort()
	if err != nil {
		return nil, err
	}
	for port := startPort; port < 52000; port++ {
		if !portAppearsAvailable("127.0.0.1", port) {
			continue
		}
		now := time.Now().UTC()
		lease := &store.PortLease{
			RuntimeID:   m.runtimeID,
			ServiceName: serviceName,
			Host:        "127.0.0.1",
			Port:        port,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := m.st.UpsertPortLease(*lease); err != nil {
			if isPortLeaseConflict(err) {
				continue
			}
			return nil, err
		}
		return lease, nil
	}
	return nil, fmt.Errorf("no managed companion ports available")
}

func (m *Manager) nextAvailablePort() (int, error) {
	leases, err := m.st.ListPortLeases()
	if err != nil {
		return 0, err
	}
	used := make(map[int]struct{}, len(leases))
	for _, lease := range leases {
		if lease.Host == "127.0.0.1" {
			used[lease.Port] = struct{}{}
		}
	}
	for port := 42000; port < 52000; port++ {
		if _, exists := used[port]; !exists {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no managed companion ports available")
}

func rewriteURLPort(rawURL, host string, port int) string {
	if rawURL == "" {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	if parsed.Host == "" {
		return rawURL
	}
	hostname := parsed.Hostname()
	if hostname == "" || hostname == "localhost" || hostname == "127.0.0.1" {
		parsed.Host = net.JoinHostPort(host, strconv.Itoa(port))
	}
	return parsed.String()
}

func defaultServiceTargetURL(service CompanionServiceSpec) string {
	if service.TargetURL != "" {
		return service.TargetURL
	}
	return service.Ready.URL
}

func copyEnvMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func portAppearsAvailable(host string, port int) bool {
	listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		// Some sandboxes disallow bind() entirely; fall back to the DB lease check.
		return os.Getenv("GO_WANT_HELPER_PROCESS") != "" || strings.Contains(err.Error(), "operation not permitted")
	}
	_ = listener.Close()
	return true
}

func isPortLeaseConflict(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: port_leases.host, port_leases.port")
}

func stopPID(ctx context.Context, pid int) error {
	if pid <= 0 {
		return nil
	}
	_ = interruptProcess(pid)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if !processAlive(pid) {
			return nil
		}
		select {
		case <-ctx.Done():
			_ = killProcess(pid)
			return nil
		case <-ticker.C:
		}
	}
}
