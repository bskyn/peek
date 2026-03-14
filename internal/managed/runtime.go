package managed

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"

	"github.com/bskyn/peek/internal/workspace"
)

// Source identifies which CLI to launch.
type Source string

const (
	SourceClaude Source = "claude"
	SourceCodex  Source = "codex"
)

// RunRequest describes a managed session launch.
type RunRequest struct {
	Source     Source
	ProjectDir string
	Args       []string // extra args passed to the native CLI
}

// Runtime supervises a native CLI subprocess for a managed workspace.
// The subprocess runs interactively with the user's terminal attached.
type Runtime struct {
	req     RunRequest
	cmd     *exec.Cmd
	done    chan struct{}
	mu      sync.Mutex
	started bool
	exited  bool
	exitErr error

	// WorkspaceID is set after workspace creation.
	WorkspaceID string
	Status      workspace.WorkspaceStatus
}

// New creates a managed runtime for the given request.
func New(req RunRequest) *Runtime {
	return &Runtime{
		req:    req,
		done:   make(chan struct{}),
		Status: workspace.StatusActive,
	}
}

// Start launches the native CLI subprocess with the user's terminal attached.
func (r *Runtime) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.started {
		return fmt.Errorf("runtime already started")
	}

	bin, args := r.buildCommand()
	r.cmd = exec.CommandContext(ctx, bin, args...)
	r.cmd.Dir = r.req.ProjectDir
	r.cmd.Env = append(os.Environ(), "PEEK_MANAGED=1")

	// Connect directly to the user's terminal for interactive use
	r.cmd.Stdin = os.Stdin
	r.cmd.Stdout = os.Stdout
	r.cmd.Stderr = os.Stderr

	if err := r.cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", bin, err)
	}
	r.started = true

	// Wait for process exit in background
	go func() {
		err := r.cmd.Wait()
		r.mu.Lock()
		r.exited = true
		r.exitErr = err
		r.mu.Unlock()
		close(r.done)
	}()

	return nil
}

// Wait blocks until the subprocess exits and returns its error.
func (r *Runtime) Wait() error {
	<-r.done
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.exitErr
}

// Done returns a channel that closes when the subprocess exits.
func (r *Runtime) Done() <-chan struct{} {
	return r.done
}

// Stop sends an interrupt to the subprocess and waits for exit.
func (r *Runtime) Stop() {
	r.mu.Lock()
	cmd := r.cmd
	r.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(os.Interrupt)
	}
	<-r.done
}

func (r *Runtime) buildCommand() (string, []string) {
	switch r.req.Source {
	case SourceClaude:
		// Launch Claude interactively — no --print flag
		return "claude", r.req.Args
	case SourceCodex:
		return "codex", r.req.Args
	default:
		return string(r.req.Source), r.req.Args
	}
}
