package viewer

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/bskyn/peek/internal/store"
)

const shutdownTimeout = 3 * time.Second

// Runtime owns the HTTP server and live broker for the browser viewer.
type Runtime struct {
	baseURL string
	broker  *Broker
	server  *http.Server
	mu      sync.RWMutex
	active  string
}

// BaseURL returns the listener URL for the runtime.
func (r *Runtime) BaseURL() string {
	if r == nil {
		return ""
	}
	return r.baseURL
}

// InitialURL returns the base URL plus the initial route.
func (r *Runtime) InitialURL(opts ViewerOptions) string {
	if r == nil {
		return ""
	}
	return r.baseURL + opts.InitialPath()
}

// Broker exposes the live fan-out broker.
func (r *Runtime) Broker() *Broker {
	if r == nil {
		return nil
	}
	return r.broker
}

// ActiveSessionID returns the session currently being tailed live.
func (r *Runtime) ActiveSessionID() string {
	if r == nil {
		return ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.active
}

// SetActiveSessionID updates the currently tailed session and broadcasts the change.
func (r *Runtime) SetActiveSessionID(sessionID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.active = sessionID
	r.mu.Unlock()
	r.broker.PublishActiveSession(sessionID)
}

// Start starts the embedded viewer server on a loopback listener.
func Start(ctx context.Context, st *store.Store, opts ViewerOptions, opener BrowserOpener) (*Runtime, error) {
	opts = NormalizeViewerOptions(opts)
	if !opts.Enabled {
		return nil, nil
	}

	broker := NewBroker()
	runtime := &Runtime{
		broker: broker,
	}

	handler, err := NewHandler(st, runtime)
	if err != nil {
		return nil, err
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", opts.Port))
	if err != nil {
		return nil, fmt.Errorf("listen for viewer: %w", err)
	}

	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	runtime.baseURL = "http://" + listener.Addr().String()
	runtime.server = server

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	go func() {
		err := server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Printf("viewer server error: %v\n", err)
		}
	}()

	if opts.OpenBrowser {
		if opener == nil {
			opener = NewBrowserOpener()
		}
		if err := opener.Open(runtime.InitialURL(opts)); err != nil {
			fmt.Printf("viewer browser open failed: %v\n", err)
		}
	}

	return runtime, nil
}
