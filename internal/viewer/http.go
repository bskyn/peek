package viewer

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/bskyn/peek/internal/store"
)

type statusResponse struct {
	ActiveSessionID string `json:"active_session_id"`
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
	mux.Handle("/", staticHandler)
	return mux, nil
}

func handleGetStatus(rt *Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, statusResponse{
			ActiveSessionID: rt.ActiveSessionID(),
		})
	}
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

		page, err := st.GetEventPage(sessionID, afterSeq, limit)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, page)
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
