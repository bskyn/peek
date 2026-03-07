package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bskyn/peek/internal/connector/codex"
)

func TestWatchForNewCodexSessionWaitsForSessionMeta(t *testing.T) {
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "sessions")
	todayDir := filepath.Join(sessionsDir, time.Now().Format("2006"), time.Now().Format("01"), time.Now().Format("02"))
	if err := os.MkdirAll(todayDir, 0o755); err != nil {
		t.Fatal(err)
	}

	currentPath := filepath.Join(todayDir, "rollout-2026-03-07T00-00-00-current-session.jsonl")
	currentMeta := `{"timestamp":"2026-03-07T00:00:00Z","type":"session_meta","payload":{"id":"current-session","cwd":"/projects/peek"}}` + "\n"
	if err := os.WriteFile(currentPath, []byte(currentMeta), 0o644); err != nil {
		t.Fatal(err)
	}

	prevPollInterval := codexWatchPollInterval
	codexWatchPollInterval = 50 * time.Millisecond
	defer func() {
		codexWatchPollInterval = prevPollInterval
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	newSessionCh := make(chan *codex.SessionFile, 1)
	go watchForNewCodexSession(ctx, dir, &codex.SessionFile{
		Path:        currentPath,
		SessionID:   "current-session",
		ProjectPath: "/projects/peek",
	}, newSessionCh)

	time.Sleep(100 * time.Millisecond)

	newPath := filepath.Join(todayDir, "rollout-2026-03-07T00-00-01-new-session.jsonl")
	if err := os.WriteFile(newPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)

	newMeta := `{"timestamp":"2026-03-07T00:00:01Z","type":"session_meta","payload":{"id":"new-session","cwd":"/projects/peek"}}` + "\n"
	if err := os.WriteFile(newPath, []byte(newMeta), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case sf := <-newSessionCh:
		if sf == nil {
			t.Fatal("expected discovered session, got nil")
		}
		if sf.Path != newPath {
			t.Fatalf("expected path %q, got %q", newPath, sf.Path)
		}
		if sf.SessionID != "new-session" {
			t.Fatalf("expected session id %q, got %q", "new-session", sf.SessionID)
		}
		if sf.ProjectPath != "/projects/peek" {
			t.Fatalf("expected project path %q, got %q", "/projects/peek", sf.ProjectPath)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for new Codex session")
	}
}
