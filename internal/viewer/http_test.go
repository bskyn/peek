package viewer

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bskyn/peek/internal/companion"
	"github.com/bskyn/peek/internal/event"
	"github.com/bskyn/peek/internal/store"
	"github.com/bskyn/peek/internal/workspace"
)

func TestHandleGetSessionEventsAnnotatesHistoricalUsage(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/viewer.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Date(2026, 3, 10, 7, 30, 0, 0, time.UTC)
	if err := st.CreateSession(event.Session{
		ID:        "codex-test",
		Source:    "codex",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	if err := st.InsertEvents([]event.Event{
		{
			ID:        "e1",
			SessionID: "codex-test",
			Timestamp: now,
			Seq:       0,
			Type:      event.EventSystem,
			PayloadJSON: json.RawMessage(`{
				"model": "gpt-5.4"
			}`),
		},
		{
			ID:        "e2",
			SessionID: "codex-test",
			Timestamp: now.Add(time.Second),
			Seq:       1,
			Type:      event.EventProgress,
			PayloadJSON: json.RawMessage(`{
				"subtype": "token_count",
				"info": {
					"total_token_usage": {
						"input_tokens": 1000,
						"output_tokens": 200,
						"total_tokens": 1200
					}
				}
			}`),
		},
	}); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/sessions/codex-test/events?limit=500", nil)
	req.SetPathValue("id", "codex-test")
	rec := httptest.NewRecorder()

	handleGetSessionEvents(st).ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("unexpected status: %d", rec.Code)
	}

	var page struct {
		Events []struct {
			PayloadJSON map[string]any `json:"payload_json"`
		} `json:"events"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(page.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(page.Events))
	}

	usage, ok := page.Events[1].PayloadJSON["usage"].(map[string]any)
	if !ok {
		t.Fatalf("expected usage payload: %+v", page.Events[1].PayloadJSON)
	}
	if usage["pricing_model"] != "gpt-5.4" {
		t.Fatalf("unexpected pricing model: %+v", usage)
	}
	if totalCost, ok := usage["total_cost_usd"].(float64); !ok || totalCost <= 0 {
		t.Fatalf("expected positive total cost: %+v", usage)
	}
}

func TestHandleAppProxyRepointsStableURL(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/viewer-proxy.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	runtime := &Runtime{broker: NewBroker(), targets: make(map[string]*url.URL)}
	runtime.SetCurrentRuntimeID("rt-root")
	runtime.SetRuntimeStatus("rt-root", companion.StatusSnapshot{
		Enabled: true,
		Browser: companion.BrowserSummary{PathPrefix: "/app/"},
	})
	runtime.SetProxyTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := "root workspace"
		if req.URL.Host == "child.test" {
			body = "child workspace"
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewBufferString(body)),
			Request:    req,
		}, nil
	}))

	if err := runtime.SetProxyTarget("rt-root", "http://root.test"); err != nil {
		t.Fatalf("set first proxy target: %v", err)
	}
	handler := handleAppProxy(st, runtime, "rt-root")

	firstReq := httptest.NewRequest("GET", "/app/", nil)
	firstRec := httptest.NewRecorder()
	handler.ServeHTTP(firstRec, firstReq)
	if firstRec.Code != http.StatusOK {
		t.Fatalf("unexpected first status: %d", firstRec.Code)
	}
	if body := firstRec.Body.String(); body != "root workspace" {
		t.Fatalf("unexpected first proxy body: %q", body)
	}

	if err := runtime.SetProxyTarget("rt-root", "http://child.test"); err != nil {
		t.Fatalf("set second proxy target: %v", err)
	}
	secondReq := httptest.NewRequest("GET", "/app/", nil)
	secondRec := httptest.NewRecorder()
	handler.ServeHTTP(secondRec, secondReq)
	if secondRec.Code != http.StatusOK {
		t.Fatalf("unexpected second status: %d", secondRec.Code)
	}
	if body := secondRec.Body.String(); body != "child workspace" {
		t.Fatalf("unexpected second proxy body: %q", body)
	}
}

func TestNewHandlerServesRuntimeSessionRouteFromSPA(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/viewer-spa.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	handler, err := NewHandler(st, &Runtime{broker: NewBroker(), targets: make(map[string]*url.URL)})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/r/rt-root/sessions/s-root", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("expected html response, got %q", got)
	}
}

func TestRuntimeStatusFromStoreUsesBootstrapState(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/viewer-status.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now().UTC()
	if err := st.CreateSession(event.Session{
		ID:        "s-root",
		Source:    "claude",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := st.CreateWorkspace(workspace.Workspace{
		ID:        "ws-root",
		Status:    workspace.StatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := st.UpsertManagedRuntime(store.ManagedRuntime{
		ID:                "rt-root",
		ProjectPath:       "/repo",
		RootWorkspaceID:   "ws-root",
		ActiveWorkspaceID: "ws-root",
		ActiveSessionID:   "s-root",
		Source:            "claude",
		Status:            store.ManagedRuntimeRunning,
		HeartbeatAt:       now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatalf("upsert managed runtime: %v", err)
	}
	if err := st.UpsertDetachedCompanionRuntime(store.DetachedCompanionRuntime{
		RuntimeID:         "rt-root",
		ActiveWorkspaceID: "ws-root",
		ConfigSource:      "peek.runtime.json",
		Phase:             string(companion.ActivationFailed),
		Message:           "bootstrap failed",
		BrowserPathPrefix: "/app/",
		UpdatedAt:         now,
	}); err != nil {
		t.Fatalf("upsert detached runtime: %v", err)
	}
	if err := st.UpsertWorkspaceBootstrapState(store.WorkspaceBootstrapState{
		WorkspaceID: "ws-root",
		Fingerprint: "fp-1",
		Status:      store.BootstrapFailed,
		LastError:   "install failed",
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("upsert bootstrap state: %v", err)
	}

	status, activeSessionID, err := runtimeStatusFromStore(st, "rt-root")
	if err != nil {
		t.Fatalf("runtime status from store: %v", err)
	}
	if activeSessionID != "s-root" {
		t.Fatalf("expected active session s-root, got %s", activeSessionID)
	}
	if status.Bootstrap.Status != store.BootstrapFailed {
		t.Fatalf("expected failed bootstrap status, got %s", status.Bootstrap.Status)
	}
	if status.Bootstrap.Fingerprint != "fp-1" {
		t.Fatalf("expected fingerprint fp-1, got %s", status.Bootstrap.Fingerprint)
	}
	if status.Bootstrap.LastError != "install failed" {
		t.Fatalf("expected bootstrap error to round-trip, got %q", status.Bootstrap.LastError)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
