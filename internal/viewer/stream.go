package viewer

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const streamHeartbeat = 15 * time.Second

// StreamFilter scopes SSE delivery.
type StreamFilter struct {
	SessionID string
}

// NewStreamHandler returns the SSE endpoint used by the web app.
func NewStreamHandler(broker *Broker) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		filter := StreamFilter{
			SessionID: r.URL.Query().Get("session_id"),
		}

		var (
			ch          <-chan LiveEnvelope
			unsubscribe func()
		)
		if filter.SessionID == "" {
			ch, unsubscribe = broker.SubscribeAll()
		} else {
			ch, unsubscribe = broker.SubscribeSession(filter.SessionID)
		}
		defer unsubscribe()

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		fmt.Fprint(w, ": connected\n\n")
		flusher.Flush()

		heartbeat := time.NewTicker(streamHeartbeat)
		defer heartbeat.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-heartbeat.C:
				fmt.Fprint(w, ": heartbeat\n\n")
				flusher.Flush()
			case envelope, ok := <-ch:
				if !ok {
					return
				}
				payload, err := json.Marshal(envelope)
				if err != nil {
					continue
				}
				if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	})
}
