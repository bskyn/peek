package managed

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRuntimeWaitRestoresTerminalState(t *testing.T) {
	ttyPath := filepath.Join(t.TempDir(), "tty")
	restore := stubManagedTTY(t, ttyPath, "saved-state")

	rt := New(RunRequest{
		Command:    writeManagedExitStub(t, 0),
		ProjectDir: t.TempDir(),
	})

	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("start runtime: %v", err)
	}
	if err := rt.Wait(); err != nil {
		t.Fatalf("wait runtime: %v", err)
	}

	calls := restore()
	if len(calls) != 2 {
		t.Fatalf("expected 2 terminal state calls, got %d (%v)", len(calls), calls)
	}
	if calls[0] != ttyPath+"|-g" {
		t.Fatalf("expected capture call, got %q", calls[0])
	}
	if calls[1] != ttyPath+"|saved-state" {
		t.Fatalf("expected restore call, got %q", calls[1])
	}
}

func TestRuntimeStopGracefullyRestoresTerminalState(t *testing.T) {
	ttyPath := filepath.Join(t.TempDir(), "tty")
	restore := stubManagedTTY(t, ttyPath, "saved-state")

	rt := New(RunRequest{
		Command:    writeManagedTrapStub(t),
		ProjectDir: t.TempDir(),
	})

	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("start runtime: %v", err)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = rt.StopGracefully(stopCtx)

	calls := restore()
	if len(calls) != 2 {
		t.Fatalf("expected 2 terminal state calls, got %d (%v)", len(calls), calls)
	}
	if calls[1] != ttyPath+"|saved-state" {
		t.Fatalf("expected restore call, got %q", calls[1])
	}
}

func TestResetTerminalEmulatorModesWritesControlSequence(t *testing.T) {
	ttyPath := filepath.Join(t.TempDir(), "tty")
	restore := stubManagedTTY(t, ttyPath, "saved-state")

	ResetTerminalEmulatorModes()

	calls := restore()
	if len(calls) != terminalResetPasses*2 {
		t.Fatalf("expected %d terminal reset calls, got %d (%v)", terminalResetPasses*2, len(calls), calls)
	}
	if calls[0] != ttyPath+"|reset" {
		t.Fatalf("expected first terminal reset write, got %q", calls[0])
	}
	if calls[1] != ttyPath+"|drain" {
		t.Fatalf("expected first terminal input drain, got %q", calls[1])
	}
	if calls[len(calls)-2] != ttyPath+"|reset" {
		t.Fatalf("expected final terminal reset write, got %q", calls[len(calls)-2])
	}
	if calls[len(calls)-1] != ttyPath+"|drain" {
		t.Fatalf("expected final terminal input drain, got %q", calls[len(calls)-1])
	}
}

func stubManagedTTY(t *testing.T, ttyPath, savedState string) func() []string {
	t.Helper()

	originalTTYPath := managedTTYPath
	originalRunStty := runStty
	originalWriteTTYControl := writeTTYControl
	originalDrainTTYInput := drainTTYInput
	var calls []string

	managedTTYPath = ttyPath
	runStty = func(path string, args ...string) ([]byte, error) {
		calls = append(calls, path+"|"+strings.Join(args, " "))
		if len(args) == 1 && args[0] == "-g" {
			return []byte(savedState + "\n"), nil
		}
		return nil, nil
	}
	writeTTYControl = func(path string, data []byte) error {
		calls = append(calls, path+"|reset")
		return nil
	}
	drainTTYInput = func(path string) error {
		calls = append(calls, path+"|drain")
		return nil
	}

	t.Cleanup(func() {
		managedTTYPath = originalTTYPath
		runStty = originalRunStty
		writeTTYControl = originalWriteTTYControl
		drainTTYInput = originalDrainTTYInput
	})

	return func() []string {
		return append([]string(nil), calls...)
	}
}

func writeManagedExitStub(t *testing.T, code int) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "exit.sh")
	script := "#!/bin/sh\nexit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write exit stub: %v", err)
	}
	return path
}

func writeManagedTrapStub(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "trap.sh")
	script := `#!/bin/sh
trap 'exit 0' INT TERM
while :; do
  sleep 0.1
done
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write trap stub: %v", err)
	}
	return path
}
