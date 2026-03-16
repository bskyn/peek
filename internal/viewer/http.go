package viewer

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/bskyn/peek/internal/companion"
	"github.com/bskyn/peek/internal/event"
	"github.com/bskyn/peek/internal/store"
	"github.com/bskyn/peek/internal/usage"
)

type statusResponse struct {
	ActiveSessionID string                    `json:"active_session_id"`
	Runtime         *companion.StatusSnapshot `json:"runtime,omitempty"`
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
	mux.HandleFunc("GET /api/status", handleGetStatus(rt))
	mux.Handle("GET /api/stream", NewStreamHandler(rt.Broker()))
	mux.Handle("/app/", handleAppProxy(rt))
	mux.Handle("/", staticHandler)
	return mux, nil
}

func handleGetStatus(rt *Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		status := rt.RuntimeStatus()
		writeJSON(w, http.StatusOK, statusResponse{
			ActiveSessionID: rt.ActiveSessionID(),
			Runtime:         &status,
		})
	}
}

func handleAppProxy(rt *Runtime) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := rt.proxyTarget()
		if target == nil {
			writeAPIError(w, http.StatusServiceUnavailable, "primary app is not ready")
			return
		}
		reverseProxy := httputil.NewSingleHostReverseProxy(target)
		reverseProxy.Transport = rt.proxyTransport()
		originalDirector := reverseProxy.Director
		reverseProxy.Director = func(req *http.Request) {
			originalDirector(req)
			req.URL.Path = joinProxyPath(target, strings.TrimPrefix(r.URL.Path, "/app"))
			req.URL.RawPath = req.URL.Path
			req.Host = target.Host
		}
		reverseProxy.ErrorHandler = func(rw http.ResponseWriter, _ *http.Request, err error) {
			writeAPIError(rw, http.StatusBadGateway, err.Error())
		}
		reverseProxy.ServeHTTP(w, r)
	})
}

func handleListSessions(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessions, err := st.ListSessionSummaries()
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
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
