package codex

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDiscoverLatest(t *testing.T) {
	dir := t.TempDir()
	sessDir := filepath.Join(dir, "sessions", "2026", "03", "05")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}

	meta := `{"timestamp":"2026-03-05T10:00:00Z","type":"session_meta","payload":{"id":"aaa-bbb","cwd":"/projects/foo"}}` + "\n"

	// Older file
	old := filepath.Join(sessDir, "rollout-2026-03-05T10-00-00-aaa-bbb-ccc-ddd-eee.jsonl")
	if err := os.WriteFile(old, []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)

	// Newer file
	newMeta := `{"timestamp":"2026-03-05T11:00:00Z","type":"session_meta","payload":{"id":"fff-ggg","cwd":"/projects/bar"}}` + "\n"
	newer := filepath.Join(sessDir, "rollout-2026-03-05T11-00-00-fff-ggg-hhh-iii-jjj.jsonl")
	if err := os.WriteFile(newer, []byte(newMeta), 0o644); err != nil {
		t.Fatal(err)
	}

	sf, err := Discover(dir, "")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if sf.Path != newer {
		t.Errorf("expected newest file %s, got %s", newer, sf.Path)
	}
	if sf.ProjectPath != "/projects/bar" {
		t.Errorf("expected project /projects/bar, got %s", sf.ProjectPath)
	}
}

func TestDiscoverByID(t *testing.T) {
	dir := t.TempDir()
	sessDir := filepath.Join(dir, "sessions", "2026", "03", "05")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}

	meta := `{"timestamp":"2026-03-05T10:00:00Z","type":"session_meta","payload":{"id":"target-uuid","cwd":"/projects/baz"}}` + "\n"
	target := filepath.Join(sessDir, "rollout-2026-03-05T10-00-00-target-uuid.jsonl")
	if err := os.WriteFile(target, []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}

	// Another file
	other := filepath.Join(sessDir, "rollout-2026-03-05T09-00-00-other-uuid.jsonl")
	if err := os.WriteFile(other, []byte(`{"type":"session_meta","payload":{"cwd":"/other"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sf, err := Discover(dir, "target-uuid")
	if err != nil {
		t.Fatalf("discover by id: %v", err)
	}
	if sf.SessionID != "target-uuid" {
		t.Errorf("expected session id target-uuid, got %s", sf.SessionID)
	}
	if sf.ProjectPath != "/projects/baz" {
		t.Errorf("expected /projects/baz, got %s", sf.ProjectPath)
	}
}

func TestDiscoverByIDNotFound(t *testing.T) {
	dir := t.TempDir()
	sessDir := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := Discover(dir, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing session")
	}
}

func TestDiscoverEmptyDir(t *testing.T) {
	dir := t.TempDir()
	sessDir := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := Discover(dir, "")
	if err == nil {
		t.Fatal("expected error for empty sessions dir")
	}
}

func TestExtractUUID(t *testing.T) {
	tests := []struct {
		filename string
		want     string
	}{
		{"rollout-2026-03-05T16-56-31-019cc0a5-6911-7123-b2ff-a4848ccd6e79.jsonl", "019cc0a5-6911-7123-b2ff-a4848ccd6e79"},
		{"rollout-2026-03-03T15-10-13-019cb5f7-5e99-7d51-96a5-3fbe6a781692.jsonl", "019cb5f7-5e99-7d51-96a5-3fbe6a781692"},
	}

	for _, tt := range tests {
		got := ExtractUUID(tt.filename)
		if got != tt.want {
			t.Errorf("ExtractUUID(%s) = %s, want %s", tt.filename, got, tt.want)
		}
	}
}

func TestToSession(t *testing.T) {
	sf := &SessionFile{
		SessionID:   "abc-def",
		ProjectPath: "/my/project",
	}
	sess := sf.ToSession("codex-abc-def")
	if sess.Source != "codex" {
		t.Errorf("expected source codex, got %s", sess.Source)
	}
	if sess.ID != "codex-abc-def" {
		t.Errorf("expected id codex-abc-def, got %s", sess.ID)
	}
}

func TestDiscoverAllOrdersByMtime(t *testing.T) {
	dir := t.TempDir()
	sessDir := filepath.Join(dir, "sessions", "2026", "03", "05")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}

	oldPath := filepath.Join(sessDir, "rollout-2026-03-05T10-00-00-old-uuid.jsonl")
	oldMeta := `{"timestamp":"2026-03-05T10:00:00Z","type":"session_meta","payload":{"id":"old-uuid","cwd":"/projects/old"}}` + "\n"
	if err := os.WriteFile(oldPath, []byte(oldMeta), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)

	newPath := filepath.Join(sessDir, "rollout-2026-03-05T11-00-00-new-uuid.jsonl")
	newMeta := `{"timestamp":"2026-03-05T11:00:00Z","type":"session_meta","payload":{"id":"new-uuid","cwd":"/projects/new"}}` + "\n"
	if err := os.WriteFile(newPath, []byte(newMeta), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := DiscoverAll(dir)
	if err != nil {
		t.Fatalf("DiscoverAll: %v", err)
	}

	if len(files) != 2 {
		t.Fatalf("expected 2 session files, got %d", len(files))
	}
	if files[0].SessionID != "old-uuid" {
		t.Fatalf("expected first session old-uuid, got %s", files[0].SessionID)
	}
	if files[1].SessionID != "new-uuid" {
		t.Fatalf("expected second session new-uuid, got %s", files[1].SessionID)
	}
}
