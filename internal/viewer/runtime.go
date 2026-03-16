package viewer

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/bskyn/peek/internal/companion"
	"github.com/bskyn/peek/internal/store"
)

const shutdownTimeout = 3 * time.Second

// Runtime owns the HTTP server and live broker for the browser viewer.
type Runtime struct {
	baseURL   string
	broker    *Broker
	server    *http.Server
	mu        sync.RWMutex
	active    string
	status    companion.StatusSnapshot
	target    *url.URL
	transport http.RoundTripper
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

// RuntimeStatus returns the current companion/runtime status for the active workspace.
func (r *Runtime) RuntimeStatus() companion.StatusSnapshot {
	if r == nil {
		return companion.StatusSnapshot{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

// SetRuntimeStatus updates the current workspace runtime status and broadcasts it.
func (r *Runtime) SetRuntimeStatus(status companion.StatusSnapshot) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.status = status
	r.mu.Unlock()
	r.broker.PublishRuntimeStatus(status)
}

// SetProxyTarget updates the live proxy destination for the primary app.
func (r *Runtime) SetProxyTarget(rawURL string) error {
	if r == nil {
		return nil
	}
	var target *url.URL
	if rawURL != "" {
		parsed, err := url.Parse(rawURL)
		if err != nil {
			return err
		}
		target = parsed
	}
	r.mu.Lock()
	r.target = target
	r.mu.Unlock()
	return nil
}

func (r *Runtime) proxyTarget() *url.URL {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.target == nil {
		return nil
	}
	cloned := *r.target
	return &cloned
}

func (r *Runtime) proxyTransport() http.RoundTripper {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.transport
}

// SetProxyTransport overrides the reverse proxy transport. Tests use this to
// validate routing without opening a real listener.
func (r *Runtime) SetProxyTransport(transport http.RoundTripper) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.transport = transport
	r.mu.Unlock()
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
