package managed

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/bskyn/peek/internal/event"
	"github.com/bskyn/peek/internal/store"
	"github.com/bskyn/peek/internal/workspace"
)

func testGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}

	run("init")
	run("config", "user.name", "test")
	run("config", "user.email", "test@test.com")

	// Create initial commit
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "initial")

	return dir
}

func testStoreForCheckpoint(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func setupWorkspaceAndSession(t *testing.T, st *store.Store, wsID, sessID string) {
	t.Helper()
	now := time.Now().UTC()

	if err := st.CreateSession(event.Session{
		ID: sessID, Source: "claude", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateWorkspace(workspace.Workspace{
		ID: wsID, Status: workspace.StatusActive, ProjectPath: "/test",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointPreToolCapture(t *testing.T) {
	dir := testGitRepo(t)
	st := testStoreForCheckpoint(t)
	setupWorkspaceAndSession(t, st, "ws-1", "s1")

	ce := NewCheckpointEngine(st, "ws-1", "s1", dir)

	if err := ce.CapturePreTool(0); err != nil {
		t.Fatalf("capture pre-tool: %v", err)
	}

	// Verify ref exists
	ref := HiddenRef("ws-1", 0, workspace.SnapshotPreTool)
	cmd := exec.Command("git", "rev-parse", ref)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ref not found: %s: %v", out, err)
	}

	// Verify checkpoint persisted
	cp, err := st.GetCheckpoint(store.CheckpointID("ws-1", 0, workspace.SnapshotPreTool))
	if err != nil {
		t.Fatalf("get checkpoint: %v", err)
	}
	if cp.GitRef != ref {
		t.Errorf("expected ref %q, got %q", ref, cp.GitRef)
	}
}

func TestCheckpointPostToolCapture(t *testing.T) {
	dir := testGitRepo(t)
	st := testStoreForCheckpoint(t)
	setupWorkspaceAndSession(t, st, "ws-1", "s1")

	ce := NewCheckpointEngine(st, "ws-1", "s1", dir)

	// Modify file, then capture post-tool
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ce.CapturePostTool(0); err != nil {
		t.Fatalf("capture post-tool: %v", err)
	}

	ref := HiddenRef("ws-1", 0, workspace.SnapshotPostTool)
	cmd := exec.Command("git", "rev-parse", ref)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ref not found: %s: %v", out, err)
	}
}

func TestCheckpointDuplicatePostToolSkipped(t *testing.T) {
	dir := testGitRepo(t)
	st := testStoreForCheckpoint(t)
	setupWorkspaceAndSession(t, st, "ws-1", "s1")

	ce := NewCheckpointEngine(st, "ws-1", "s1", dir)

	if err := ce.CapturePostTool(0); err != nil {
		t.Fatal(err)
	}
	// Second call should be a no-op
	if err := ce.CapturePostTool(0); err != nil {
		t.Fatal(err)
	}

	// Should only have one checkpoint
	cps, err := st.ListCheckpoints("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(cps) != 1 {
		t.Errorf("expected 1 checkpoint, got %d", len(cps))
	}
}

func TestCheckpointMultipleSequences(t *testing.T) {
	dir := testGitRepo(t)
	st := testStoreForCheckpoint(t)
	setupWorkspaceAndSession(t, st, "ws-1", "s1")

	ce := NewCheckpointEngine(st, "ws-1", "s1", dir)

	// Capture pre and post for multiple sequences
	for seq := int64(0); seq < 3; seq++ {
		if err := ce.CapturePreTool(seq); err != nil {
			t.Fatalf("pre-tool seq %d: %v", seq, err)
		}

		// Modify a file
		content := []byte(fmt.Sprintf("version %d\n", seq))
		if err := os.WriteFile(filepath.Join(dir, "data.txt"), content, 0o644); err != nil {
			t.Fatal(err)
		}

		if err := ce.CapturePostTool(seq); err != nil {
			t.Fatalf("post-tool seq %d: %v", seq, err)
		}
	}

	cps, err := st.ListCheckpoints("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(cps) != 6 { // 3 pre + 3 post
		t.Errorf("expected 6 checkpoints, got %d", len(cps))
	}
}

func TestCheckpointResolveAtSequence(t *testing.T) {
	dir := testGitRepo(t)
	st := testStoreForCheckpoint(t)
	setupWorkspaceAndSession(t, st, "ws-1", "s1")

	ce := NewCheckpointEngine(st, "ws-1", "s1", dir)

	// Create checkpoints at seq 2, 5, 8
	for _, seq := range []int64{2, 5, 8} {
		if err := ce.CapturePreTool(seq); err != nil {
			t.Fatal(err)
		}
	}

	// Resolve at seq 6 should return checkpoint at seq 5
	cp, err := st.ResolveCheckpoint("ws-1", 6, workspace.SnapshotPreTool)
	if err != nil {
		t.Fatal(err)
	}
	if cp.Seq != 5 {
		t.Errorf("expected seq 5, got %d", cp.Seq)
	}
}

