package viewer

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/bskyn/peek/internal/companion"
	"github.com/bskyn/peek/internal/event"
	"github.com/bskyn/peek/internal/store"
	"github.com/bskyn/peek/internal/usage"
	"github.com/google/uuid"
)

const runtimeRequestPollInterval = 200 * time.Millisecond
const runtimeRequestTimeout = 20 * time.Second
const runtimeStaleAfter = 3 * time.Second

type statusResponse struct {
	CurrentRuntimeID string                       `json:"current_runtime_id,omitempty"`
	ActiveSessionID  string                       `json:"active_session_id"`
	Runtime          *companion.StatusSnapshot    `json:"runtime,omitempty"`
	Runtimes         []store.ManagedRuntimeView   `json:"runtimes,omitempty"`
	Workspaces       []store.RuntimeWorkspaceView `json:"workspaces,omitempty"`
}

// NewHandler builds the API and static routes for the viewer runtime.
func NewHandler(st *store.Store, rt *Runtime) (http.Handler, error) {
	staticHandler, err := NewStaticHandler()
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/sessions", handleListSessions(st))
	mux.HandleFunc("GET /api/sessions/{id}", handleGetSession(st))
	mux.HandleFunc("GET /api/sessions/{id}/events", handleGetSessionEvents(st))
	mux.HandleFunc("GET /api/status", handleGetStatus(st, rt))
	mux.Handle("GET /api/stream", NewStreamHandler(rt.Broker()))
	mux.HandleFunc("POST /api/runtimes/{id}/workspaces/{workspace_id}/switch", handleSwitchRuntimeWorkspace(st))
	mux.Handle("/app/", handleAppProxy(st, rt, ""))
	mux.Handle("/r/", handleRuntimeRoute(st, rt, staticHandler))
	mux.Handle("/", staticHandler)
	return mux, nil
}

func handleGetStatus(st *store.Store, rt *Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		runtimeID := r.URL.Query().Get("runtime_id")
		payload, err := buildStatusResponse(st, rt, runtimeID)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, sql.ErrNoRows) {
				status = http.StatusNotFound
			}
			writeAPIError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, payload)
	}
}

func handleAppProxy(st *store.Store, rt *Runtime, runtimeID string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := rt.proxyTarget(runtimeID)
		if target == nil && runtimeID != "" {
			managedRuntime, err := st.GetDetachedCompanionRuntime(runtimeID)
			if err == nil && managedRuntime.BrowserTargetURL != "" {
				parsed, parseErr := url.Parse(managedRuntime.BrowserTargetURL)
				if parseErr == nil {
					target = parsed
				}
			}
		}
		if target == nil {
			writeAPIError(w, http.StatusServiceUnavailable, "primary app is not ready")
			return
		}
		reverseProxy := httputil.NewSingleHostReverseProxy(target)
		reverseProxy.Transport = rt.proxyTransport()
		originalDirector := reverseProxy.Director
		reverseProxy.Director = func(req *http.Request) {
			originalDirector(req)
			req.URL.Path = joinProxyPath(target, proxySuffix(r.URL.Path, runtimeID))
			req.URL.RawPath = req.URL.Path
			req.Host = target.Host
		}
		reverseProxy.ErrorHandler = func(rw http.ResponseWriter, _ *http.Request, err error) {
			writeAPIError(rw, http.StatusBadGateway, err.Error())
		}
		reverseProxy.ServeHTTP(w, r)
	})
}

func handleRuntimeAppProxy(st *store.Store, rt *Runtime) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		runtimeID, ok := runtimeAppRequest(r.URL.Path)
		if !ok {
			http.NotFound(w, r)
			return
		}
		handleAppProxy(st, rt, runtimeID).ServeHTTP(w, r)
	})
}

func handleRuntimeRoute(st *store.Store, rt *Runtime, staticHandler http.Handler) http.Handler {
	appProxy := handleRuntimeAppProxy(st, rt)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := runtimeAppRequest(r.URL.Path); ok {
			appProxy.ServeHTTP(w, r)
			return
		}
		staticHandler.ServeHTTP(w, r)
	})
}

func runtimeAppRequest(requestPath string) (string, bool) {
	pathValue := strings.TrimPrefix(requestPath, "/r/")
	parts := strings.SplitN(pathValue, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] != "app" {
		return "", false
	}
	return parts[0], true
}

func handleListSessions(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		runtimeID := r.URL.Query().Get("runtime_id")
		var (
			sessions []store.SessionSummary
			err      error
		)
		if runtimeID == "" {
			sessions, err = st.ListSessionSummaries()
		} else {
			sessions, err = st.ListSessionSummariesForRuntime(runtimeID)
		}
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
	}
}

func handleSwitchRuntimeWorkspace(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		runtimeID := strings.TrimSpace(r.PathValue("id"))
		workspaceID := strings.TrimSpace(r.PathValue("workspace_id"))
		resp, err := enqueueRuntimeSwitchRequest(st, runtimeID, workspaceID)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, sql.ErrNoRows) {
				status = http.StatusNotFound
			}
			writeAPIError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"session_id":   resp.ResponseSessionID,
			"workspace_id": resp.ResponseWorkspaceID,
		})
	}
}

func handleGetSession(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		detail, err := st.GetSessionDetail(r.PathValue("id"))
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, sql.ErrNoRows) {
				status = http.StatusNotFound
			}
			writeAPIError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, detail)
	}
}

func handleGetSessionEvents(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.PathValue("id")
		if _, err := st.GetSessionSummary(sessionID); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, sql.ErrNoRows) {
				status = http.StatusNotFound
			}
			writeAPIError(w, status, err.Error())
			return
		}

		afterSeq, err := parseOptionalInt64(r.URL.Query().Get("after_seq"), -1)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid after_seq")
			return
		}
		limit, err := parseOptionalInt(r.URL.Query().Get("limit"), 200)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid limit")
			return
		}

		events, err := st.GetEvents(sessionID)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err.Error())
			return
		}
		annotator := usage.NewAnnotator()
		events = annotator.Annotate(events)
		page := paginateEvents(events, afterSeq, limit)
		writeJSON(w, http.StatusOK, page)
	}
}

func paginateEvents(events []event.Event, afterSeq int64, limit int) store.EventPage {
	if limit <= 0 {
		limit = 200
	}

	filtered := make([]event.Event, 0, limit)
	var nextAfterSeq int64
	for _, ev := range events {
		if ev.Seq <= afterSeq {
			continue
		}
		if len(filtered) == limit {
			return store.EventPage{
				Events:       filtered,
				HasMore:      true,
				NextAfterSeq: nextAfterSeq,
			}
		}
		nextAfterSeq = ev.Seq
		filtered = append(filtered, ev)
	}

	return store.EventPage{
		Events:       filtered,
		HasMore:      false,
		NextAfterSeq: nextAfterSeq,
	}
}

func parseOptionalInt(raw string, fallback int) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func parseOptionalInt64(raw string, fallback int64) (int64, error) {
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeAPIError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func joinProxyPath(target *url.URL, suffix string) string {
	base := target.Path
	if base == "" {
		base = "/"
	}
	if suffix == "" {
		return base
	}
	return path.Join(base, suffix)
}

func proxySuffix(requestPath, runtimeID string) string {
	if runtimeID == "" {
		return strings.TrimPrefix(requestPath, "/app")
	}
	prefix := "/r/" + runtimeID + "/app"
	return strings.TrimPrefix(requestPath, prefix)
}

func buildStatusResponse(st *store.Store, rt *Runtime, runtimeID string) (statusResponse, error) {
	selectedRuntimeID := runtimeID
	if selectedRuntimeID == "" {
		selectedRuntimeID = rt.CurrentRuntimeID()
	}

	runtimes, err := runtimeSiblings(st, selectedRuntimeID)
	if err != nil {
		return statusResponse{}, err
	}

	if selectedRuntimeID == "" {
		status := rt.RuntimeStatus()
		return statusResponse{
			CurrentRuntimeID: rt.CurrentRuntimeID(),
			ActiveSessionID:  rt.ActiveSessionID(),
			Runtime:          &status,
			Runtimes:         runtimes,
		}, nil
	}

	workspaces, err := st.ListRuntimeWorkspaceViews(selectedRuntimeID)
	if err != nil {
		return statusResponse{}, err
	}
	activeSessionID := ""
	selectedStatus := rt.RuntimeStatus()
	if rt.CurrentRuntimeID() != selectedRuntimeID {
		selectedStatus, activeSessionID, err = runtimeStatusFromStore(st, selectedRuntimeID)
		if err != nil {
			return statusResponse{}, err
		}
	} else {
		activeSessionID = rt.ActiveSessionID()
	}

	return statusResponse{
		CurrentRuntimeID: selectedRuntimeID,
		ActiveSessionID:  activeSessionID,
		Runtime:          &selectedStatus,
		Runtimes:         runtimes,
		Workspaces:       workspaces,
	}, nil
}

func runtimeSiblings(st *store.Store, runtimeID string) ([]store.ManagedRuntimeView, error) {
	if runtimeID == "" {
		return st.ListManagedRuntimeViews()
	}
	runtime, err := st.GetManagedRuntime(runtimeID)
	if err != nil {
		return nil, err
	}
	projectRuntimes, err := st.ListManagedRuntimesByProjectPath(runtime.ProjectPath)
	if err != nil {
		return nil, err
	}
	result := make([]store.ManagedRuntimeView, 0, len(projectRuntimes))
	for _, candidate := range projectRuntimes {
		view, err := st.GetManagedRuntimeView(candidate.ID)
		if err != nil {
			return nil, err
		}
		result = append(result, view)
	}
	return result, nil
}

func runtimeStatusFromStore(st *store.Store, runtimeID string) (companion.StatusSnapshot, string, error) {
	detached, err := st.GetDetachedCompanionRuntime(runtimeID)
	if err != nil {
		return companion.StatusSnapshot{}, "", err
	}
	services, err := st.ListCompanionServiceStates(runtimeID)
	if err != nil {
		return companion.StatusSnapshot{}, "", err
	}
	runtime, err := st.GetManagedRuntime(runtimeID)
	if err != nil {
		return companion.StatusSnapshot{}, "", err
	}
	bootstrap, err := bootstrapSummaryForWorkspace(st, detached.ActiveWorkspaceID)
	if err != nil {
		return companion.StatusSnapshot{}, "", err
	}

	summaries := make([]companion.ServiceSummary, 0, len(services))
	for _, service := range services {
		summaries = append(summaries, companion.ServiceSummary{
			Name:      service.ServiceName,
			Role:      service.Role,
			Status:    service.Status,
			TargetURL: service.TargetURL,
			LastError: service.LastError,
		})
	}

	return companion.StatusSnapshot{
		Enabled:           detached.ConfigSource != "",
		ConfigSource:      detached.ConfigSource,
		ActiveWorkspaceID: detached.ActiveWorkspaceID,
		Phase:             companion.ActivationPhase(detached.Phase),
		Message:           detached.Message,
		Bootstrap:         bootstrap,
		Services:          summaries,
		Browser: companion.BrowserSummary{
			PathPrefix: detached.BrowserPathPrefix,
			TargetURL:  detached.BrowserTargetURL,
		},
		UpdatedAt: detached.UpdatedAt,
	}, runtime.ActiveSessionID, nil
}

func bootstrapSummaryForWorkspace(st *store.Store, workspaceID string) (companion.BootstrapSummary, error) {
	summary := companion.BootstrapSummary{Status: store.BootstrapPending}
	if workspaceID == "" {
		return summary, nil
	}
	state, err := st.GetWorkspaceBootstrapState(workspaceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return summary, nil
		}
		return companion.BootstrapSummary{}, err
	}
	return companion.BootstrapSummary{
		Status:      state.Status,
		Fingerprint: state.Fingerprint,
		LastError:   state.LastError,
	}, nil
}

func enqueueRuntimeSwitchRequest(st *store.Store, runtimeID, workspaceID string) (*store.ManagedRuntimeRequest, error) {
	runtime, err := st.GetManagedRuntime(runtimeID)
	if err != nil {
		return nil, err
	}
	if runtime.Status != store.ManagedRuntimeRunning || time.Since(runtime.HeartbeatAt) > runtimeStaleAfter {
		return nil, fmt.Errorf("runtime %s is not live", runtimeID)
	}
	rootWorkspaceID, err := st.LineageRootWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	if rootWorkspaceID != runtime.RootWorkspaceID {
		return nil, fmt.Errorf("workspace %s does not belong to runtime %s", workspaceID, runtimeID)
	}
	now := time.Now().UTC()
	req := store.ManagedRuntimeRequest{
		ID:                "rtreq-" + uuid.New().String(),
		RuntimeID:         runtimeID,
		Kind:              "switch",
		TargetWorkspaceID: workspaceID,
		Status:            store.ManagedRuntimeRequestPending,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := st.CreateManagedRuntimeRequest(req); err != nil {
		return nil, err
	}
	return waitForRuntimeRequest(st, req.ID)
}

func waitForRuntimeRequest(st *store.Store, requestID string) (*store.ManagedRuntimeRequest, error) {
	deadline := time.Now().Add(runtimeRequestTimeout)
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
		time.Sleep(runtimeRequestPollInterval)
	}
	return nil, fmt.Errorf("timed out waiting for runtime request %s", requestID)
}
