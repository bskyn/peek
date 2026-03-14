package managed

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bskyn/peek/internal/event"
	"github.com/bskyn/peek/internal/store"
	"github.com/bskyn/peek/internal/workspace"
)

func setupBranchTest(t *testing.T) (*store.Store, *Orchestrator, *CheckpointEngine, string) {
	t.Helper()
	dir := testGitRepo(t)
	st := testStoreForCheckpoint(t)
	worktreeBase := filepath.Join(t.TempDir(), "worktrees")
	now := time.Now().UTC()

	// Create session and workspace
	if err := st.CreateSession(event.Session{
		ID: "s1", Source: "claude", ProjectPath: dir, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateWorkspace(workspace.Workspace{
		ID: "ws-root", Status: workspace.StatusActive, ProjectPath: dir,
		WorktreePath: dir, IsRoot: true,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.LinkWorkspaceSession(workspace.WorkspaceSession{
		WorkspaceID: "ws-root", SessionID: "s1", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveBranchPath(workspace.BranchPathSegment{
		WorkspaceID: "ws-root", Depth: 0, Ordinal: 0,
	}); err != nil {
		t.Fatal(err)
	}

	ce := NewCheckpointEngine(st, "ws-root", "s1", dir)
	orch := NewOrchestrator(st, dir, worktreeBase)

	return st, orch, ce, dir
}

func TestBranchFromToolCall(t *testing.T) {
	st, orch, ce, dir := setupBranchTest(t)

	// Create pre-tool checkpoint at seq 3
	if err := ce.CapturePreTool(3); err != nil {
		t.Fatal(err)
	}

	// Modify file and capture post-tool
	if err := os.WriteFile(filepath.Join(dir, "new_file.go"), []byte("package new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ce.CapturePostTool(3); err != nil {
		t.Fatal(err)
	}

	// Branch from seq 3 — should get pre-result state
	result, err := orch.Branch(BranchRequest{
		SourceWorkspaceID: "ws-root",
		BranchFromSeq:     3,
	})
	if err != nil {
		t.Fatalf("branch: %v", err)
	}

	// Source should be frozen
	src, err := st.GetWorkspace("ws-root")
	if err != nil {
		t.Fatal(err)
	}
	if src.Status != workspace.StatusFrozen {
		t.Errorf("expected source frozen, got %s", src.Status)
	}

	// Child should be active
	child, err := st.GetWorkspace(result.NewWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if child.Status != workspace.StatusActive {
		t.Errorf("expected child active, got %s", child.Status)
	}
	if child.ParentWorkspaceID != "ws-root" {
		t.Errorf("expected parent ws-root, got %q", child.ParentWorkspaceID)
	}
	if child.BranchFromSeq == nil || *child.BranchFromSeq != 3 {
		t.Errorf("expected branch_from_seq=3, got %v", child.BranchFromSeq)
	}

	// Worktree should exist
	if _, err := os.Stat(result.WorktreePath); err != nil {
		t.Errorf("worktree should exist: %v", err)
	}

	// Worktree should NOT have new_file.go (pre-tool snapshot)
	if _, err := os.Stat(filepath.Join(result.WorktreePath, "new_file.go")); !os.IsNotExist(err) {
		t.Errorf("worktree should not have new_file.go (pre-tool snapshot)")
	}

	// Branch path should be correct
	path, err := st.GetBranchPath(result.NewWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(path) != 2 {
		t.Fatalf("expected 2 path segments, got %d", len(path))
	}
	if path[0].WorkspaceID != "ws-root" || path[1].WorkspaceID != result.NewWorkspaceID {
		t.Errorf("unexpected path: %+v", path)
	}
}

func TestBranchFromInactiveWorkspaceFails(t *testing.T) {
	st, orch, ce, _ := setupBranchTest(t)

	if err := ce.CapturePreTool(0); err != nil {
		t.Fatal(err)
	}

	// Freeze the source
	if err := st.UpdateWorkspaceStatus("ws-root", workspace.StatusFrozen); err != nil {
		t.Fatal(err)
	}

	_, err := orch.Branch(BranchRequest{
		SourceWorkspaceID: "ws-root",
		BranchFromSeq:     0,
	})
	if err == nil {
		t.Fatal("expected error branching from frozen workspace")
	}
}

func TestBranchSiblingOrdinals(t *testing.T) {
	st, orch, ce, _ := setupBranchTest(t)

	// Create checkpoint
	if err := ce.CapturePreTool(5); err != nil {
		t.Fatal(err)
	}

	// Branch twice from same seq
	r1, err := orch.Branch(BranchRequest{
		SourceWorkspaceID: "ws-root",
		BranchFromSeq:     5,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Re-activate source for second branch
	if err := st.UpdateWorkspaceStatus("ws-root", workspace.StatusActive); err != nil {
		t.Fatal(err)
	}

	r2, err := orch.Branch(BranchRequest{
		SourceWorkspaceID: "ws-root",
		BranchFromSeq:     5,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify sibling ordinals
	c1, _ := st.GetWorkspace(r1.NewWorkspaceID)
	c2, _ := st.GetWorkspace(r2.NewWorkspaceID)
	if c1.SiblingOrdinal != 0 {
		t.Errorf("first sibling ordinal: expected 0, got %d", c1.SiblingOrdinal)
	}
	if c2.SiblingOrdinal != 1 {
		t.Errorf("second sibling ordinal: expected 1, got %d", c2.SiblingOrdinal)
	}
}

func TestSwitchReactivatesFrozenWorkspace(t *testing.T) {
	st, orch, ce, _ := setupBranchTest(t)

	if err := ce.CapturePreTool(0); err != nil {
		t.Fatal(err)
	}

	// Branch (freezes source)
	result, err := orch.Branch(BranchRequest{
		SourceWorkspaceID: "ws-root",
		BranchFromSeq:     0,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Switch back to frozen source
	if err := orch.Switch(SwitchRequest{TargetWorkspaceID: "ws-root"}); err != nil {
		t.Fatal(err)
	}

	ws, err := st.GetWorkspace("ws-root")
	if err != nil {
		t.Fatal(err)
	}
	if ws.Status != workspace.StatusActive {
		t.Errorf("expected active after switch, got %s", ws.Status)
	}

	child, err := st.GetWorkspace(result.NewWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if child.Status != workspace.StatusFrozen {
		t.Errorf("expected child frozen after switch, got %s", child.Status)
	}
}

func TestSwitchToMergedFails(t *testing.T) {
	st, orch, _, _ := setupBranchTest(t)

	if err := st.UpdateWorkspaceStatus("ws-root", workspace.StatusMerged); err != nil {
		t.Fatal(err)
	}

	err := orch.Switch(SwitchRequest{TargetWorkspaceID: "ws-root"})
	if err == nil {
		t.Fatal("expected error switching to merged workspace")
	}
}

func TestSwitchAlreadyActiveIsNoop(t *testing.T) {
	_, orch, _, _ := setupBranchTest(t)

	// ws-root is already active
	if err := orch.Switch(SwitchRequest{TargetWorkspaceID: "ws-root"}); err != nil {
		t.Fatalf("switch to active should be noop: %v", err)
	}
}

func TestBranchFromLaterCardUsesLatestPostToolCheckpoint(t *testing.T) {
	st, orch, ce, dir := setupBranchTest(t)

	if err := ce.CapturePreTool(3); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "post_tool.txt"), []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ce.CapturePostTool(3); err != nil {
		t.Fatal(err)
	}

	if err := st.InsertEvent(event.Event{
		ID:          "ev-5",
		SessionID:   "s1",
		Timestamp:   time.Now().UTC(),
		Seq:         5,
		Type:        event.EventAssistantMessage,
		PayloadJSON: mustJSONPayload(t, map[string]string{"text": "after the tool ran"}),
	}); err != nil {
		t.Fatal(err)
	}

	result, err := orch.Branch(BranchRequest{
		SourceWorkspaceID: "ws-root",
		BranchFromSeq:     5,
	})
	if err != nil {
		t.Fatalf("branch: %v", err)
	}
	if result.Anchor.SnapshotKind != workspace.SnapshotPostTool {
		t.Fatalf("expected post-tool snapshot, got %s", result.Anchor.SnapshotKind)
	}
	if result.Anchor.SnapshotSeq != 3 {
		t.Fatalf("expected snapshot seq 3, got %d", result.Anchor.SnapshotSeq)
	}
	if _, err := os.Stat(filepath.Join(result.WorktreePath, "post_tool.txt")); err != nil {
		t.Fatalf("expected post_tool.txt in child worktree: %v", err)
	}

	childSession, err := st.GetSession(result.NewSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if childSession.Source != "claude" {
		t.Fatalf("expected child session source claude, got %q", childSession.Source)
	}
	if childSession.ParentSessionID != "s1" {
		t.Fatalf("expected parent session s1, got %q", childSession.ParentSessionID)
	}
	if childSession.ProjectPath != result.WorktreePath {
		t.Fatalf("expected child session project path %q, got %q", result.WorktreePath, childSession.ProjectPath)
	}
}

func mustJSONPayload(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
