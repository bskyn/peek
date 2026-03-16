package managed

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	Command    string
	ProjectDir string
	Args       []string // extra args passed to the native CLI
	Env        []string
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
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
func (r *Runtime) Start(_ context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.started {
		return fmt.Errorf("runtime already started")
	}

	bin, args := r.buildCommand()
	r.cmd = exec.Command(bin, args...)
	r.cmd.Dir = r.req.ProjectDir
	r.cmd.Env = append(append(os.Environ(), r.req.Env...), "PEEK_MANAGED=1")

	r.cmd.Stdin = valueOrDefaultReader(r.req.Stdin, os.Stdin)
	r.cmd.Stdout = valueOrDefaultWriter(r.req.Stdout, os.Stdout)
	r.cmd.Stderr = valueOrDefaultWriter(r.req.Stderr, os.Stderr)

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
func (r *Runtime) StopGracefully(ctx context.Context) error {
	r.mu.Lock()
	cmd := r.cmd
	r.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(os.Interrupt)
	}
	select {
	case <-r.done:
	case <-ctx.Done():
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-r.done
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.exitErr
}

func (r *Runtime) buildCommand() (string, []string) {
	if r.req.Command != "" {
		return r.req.Command, r.req.Args
	}
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

// RunExitError preserves the wrapped provider's real exit code.
type RunExitError struct {
	Source Source
	Code   int
	Err    error
}

func (e *RunExitError) Error() string {
	return fmt.Sprintf("%s exited with code %d: %v", e.Source, e.Code, e.Err)
}

func (e *RunExitError) Unwrap() error {
	return e.Err
}

func (e *RunExitError) ExitCode() int {
	return e.Code
}

// WrapRunExitError converts exec exit errors into an error with a stable exit code.
func WrapRunExitError(source Source, err error) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return err
	}
	return &RunExitError{
		Source: source,
		Code:   exitErr.ExitCode(),
		Err:    err,
	}
}

func valueOrDefaultReader(r io.Reader, fallback *os.File) io.Reader {
	if r != nil {
		return r
	}
	return fallback
}

func valueOrDefaultWriter(w io.Writer, fallback *os.File) io.Writer {
	if w != nil {
		return w
	}
	return fallback
}

var managedTTYPath = "/dev/tty"

var writeTTYControl = func(ttyPath string, data []byte) error {
	tty, err := os.OpenFile(ttyPath, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer tty.Close()
	_, err = tty.Write(data)
	return err
}

// Reset terminal-emulator modes that may survive a SIGINT-triggered CLI exit.
// This runs only when Peek is returning control to the shell, not on
// branch/switch handoffs.
func ResetTerminalEmulatorModes() {
	const sequence = "\x1b[?2004l" +
		"\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1004l\x1b[?1006l" +
		"\x1b[?1l\x1b[>" +
		"\x1b[>4;m" +
		"\x1b[=0;1u" +
		"\x1b[?1049l" +
		"\x1b[>4;m" +
		"\x1b[=0;1u"
	_ = writeTTYControl(managedTTYPath, []byte(sequence))
}
