package viewer

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bskyn/peek/internal/companion"
	"github.com/bskyn/peek/internal/event"
	"github.com/bskyn/peek/internal/store"
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
	runtime := &Runtime{broker: NewBroker()}
	runtime.SetRuntimeStatus(companion.StatusSnapshot{
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

	if err := runtime.SetProxyTarget("http://root.test"); err != nil {
		t.Fatalf("set first proxy target: %v", err)
	}
	handler := handleAppProxy(runtime)

	firstReq := httptest.NewRequest("GET", "/app/", nil)
	firstRec := httptest.NewRecorder()
	handler.ServeHTTP(firstRec, firstReq)
	if firstRec.Code != http.StatusOK {
		t.Fatalf("unexpected first status: %d", firstRec.Code)
	}
	if body := firstRec.Body.String(); body != "root workspace" {
		t.Fatalf("unexpected first proxy body: %q", body)
	}

	if err := runtime.SetProxyTarget("http://child.test"); err != nil {
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
