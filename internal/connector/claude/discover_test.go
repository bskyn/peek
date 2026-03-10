package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupTestClaudeDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Create projects structure
	projDir := filepath.Join(dir, "projects", "test-project")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create session files with different mtimes
	sessions := []struct {
		id    string
		delay time.Duration
	}{
		{"sess-old", 0},
		{"sess-mid", 10 * time.Millisecond},
		{"sess-new", 20 * time.Millisecond},
	}

	for _, s := range sessions {
		time.Sleep(s.delay)
		path := filepath.Join(projDir, s.id+".jsonl")
		if err := os.WriteFile(path, []byte(`{"type":"user"}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return dir
}

func TestDiscoverByID(t *testing.T) {
	dir := setupTestClaudeDir(t)

	sf, err := Discover(dir, "sess-mid")
	if err != nil {
		t.Fatalf("discover by ID: %v", err)
	}

	if sf.SessionID != "sess-mid" {
		t.Errorf("expected sess-mid, got %s", sf.SessionID)
	}
	if sf.EncodedProjectKey != "test-project" {
		t.Errorf("expected test-project, got %s", sf.EncodedProjectKey)
	}
}

func TestDiscoverByIDNotFound(t *testing.T) {
	dir := setupTestClaudeDir(t)

	_, err := Discover(dir, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func TestDiscoverLatestByMtime(t *testing.T) {
	dir := setupTestClaudeDir(t)

	sf, err := Discover(dir, "")
	if err != nil {
		t.Fatalf("discover latest: %v", err)
	}

	if sf.SessionID != "sess-new" {
		t.Errorf("expected most recent session sess-new, got %s", sf.SessionID)
	}
}

func TestDiscoverFromHistory(t *testing.T) {
	dir := setupTestClaudeDir(t)

	// Write history.jsonl pointing to sess-old
	entries := []historyEntry{
		{SessionID: "sess-old", Project: "/test", Timestamp: 1000},
		{SessionID: "sess-mid", Project: "/test", Timestamp: 2000},
	}
	var lines []byte
	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, data...)
		lines = append(lines, '\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "history.jsonl"), lines, 0o644); err != nil {
		t.Fatal(err)
	}

	sf, err := Discover(dir, "")
	if err != nil {
		t.Fatalf("discover from history: %v", err)
	}

	// Should pick sess-mid (last entry in history)
	if sf.SessionID != "sess-mid" {
		t.Errorf("expected sess-mid from history, got %s", sf.SessionID)
	}
}

func TestDiscoverEmptyProjectsDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := Discover(dir, "")
	if err == nil {
		t.Fatal("expected error for empty projects dir")
	}
}

func TestDiscoverSkipsAgentFiles(t *testing.T) {
	dir := t.TempDir()
	projDir := filepath.Join(dir, "projects", "proj1")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create only agent files
	if err := os.WriteFile(filepath.Join(projDir, "agent-abc.jsonl"), []byte(`{}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Discover(dir, "")
	if err == nil {
		t.Fatal("expected error when only agent files exist")
	}
}

func TestDiscoverAllOrdersRootsBeforeSubagents(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "projects", "proj1")
	subagentsDir := filepath.Join(projectDir, "subagents")
	if err := os.MkdirAll(subagentsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	rootA := filepath.Join(projectDir, "root-a.jsonl")
	if err := os.WriteFile(rootA, []byte(`{"type":"user"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)

	childA := filepath.Join(subagentsDir, "agent-child-a.jsonl")
	if err := os.WriteFile(childA, []byte(`{"type":"user"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)

	rootB := filepath.Join(projectDir, "root-b.jsonl")
	if err := os.WriteFile(rootB, []byte(`{"type":"user"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := DiscoverAll(dir)
	if err != nil {
		t.Fatalf("DiscoverAll: %v", err)
	}

	if len(files) != 3 {
		t.Fatalf("expected 3 session files, got %d", len(files))
	}

	if files[0].SessionID != "root-a" {
		t.Fatalf("expected first session root-a, got %s", files[0].SessionID)
	}
	if files[1].SessionID != "agent-child-a" {
		t.Fatalf("expected second session agent-child-a, got %s", files[1].SessionID)
	}
	if files[1].ParentSessionID != "root-a" {
		t.Fatalf("expected child parent root-a, got %s", files[1].ParentSessionID)
	}
	if files[2].SessionID != "root-b" {
		t.Fatalf("expected third session root-b, got %s", files[2].SessionID)
	}
}
