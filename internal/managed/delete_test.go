package managed

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/bskyn/peek/internal/event"
	"github.com/bskyn/peek/internal/store"
	"github.com/bskyn/peek/internal/workspace"
)

func TestDeleteWorkspaceRemovesLeafWorktreeAndRefs(t *testing.T) {
	st, orch, ce, dir := setupBranchTest(t)

	if err := ce.CapturePreTool(0); err != nil {
		t.Fatal(err)
	}

	result, err := orch.Branch(BranchRequest{
		SourceWorkspaceID: "ws-root",
		BranchFromSeq:     0,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := orch.Freeze(result.NewWorkspaceID); err != nil {
		t.Fatal(err)
	}

	if err := orch.DeleteWorkspace(result.NewWorkspaceID); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}

	if _, err := st.GetWorkspace(result.NewWorkspaceID); err == nil {
		t.Fatal("expected workspace metadata to be deleted")
	}

	if _, err := os.Stat(result.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("expected worktree %s to be removed, got %v", result.WorktreePath, err)
	}

	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", result.GitRef)
	cmd.Dir = dir
	if err := cmd.Run(); err == nil {
		t.Fatalf("expected git ref %s to be removed", result.GitRef)
	}
}

func TestDeleteWorkspaceRejectsActiveWorkspace(t *testing.T) {
	_, orch, ce, _ := setupBranchTest(t)

	if err := ce.CapturePreTool(0); err != nil {
		t.Fatal(err)
	}

	result, err := orch.Branch(BranchRequest{
		SourceWorkspaceID: "ws-root",
		BranchFromSeq:     0,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := orch.DeleteWorkspace(result.NewWorkspaceID); err == nil {
		t.Fatal("expected error deleting active workspace")
	}
}

func TestDeleteWorkspaceRejectsParentWorkspace(t *testing.T) {
	st, orch, ce, _ := setupBranchTest(t)

	if err := ce.CapturePreTool(0); err != nil {
		t.Fatal(err)
	}

	child, err := orch.Branch(BranchRequest{
		SourceWorkspaceID: "ws-root",
		BranchFromSeq:     0,
	})
	if err != nil {
		t.Fatal(err)
	}

	childCheckpoints := NewCheckpointEngine(st, child.NewWorkspaceID, child.NewSessionID, child.WorktreePath)
	if err := childCheckpoints.CapturePreTool(1); err != nil {
		t.Fatal(err)
	}

	grandchild, err := orch.Branch(BranchRequest{
		SourceWorkspaceID: child.NewWorkspaceID,
		BranchFromSeq:     1,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = grandchild

	if err := orch.DeleteWorkspace(child.NewWorkspaceID); err == nil {
		t.Fatal("expected error deleting non-leaf workspace")
	}
}

func TestPruneWorkspaceLineageRemovesStoppedRootLineage(t *testing.T) {
	st, orch, ce, dir := setupBranchTest(t)
	now := time.Now().UTC()

	if err := ce.CapturePreTool(0); err != nil {
		t.Fatal(err)
	}

	child, err := orch.Branch(BranchRequest{
		SourceWorkspaceID: "ws-root",
		BranchFromSeq:     0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := orch.Freeze(child.NewWorkspaceID); err != nil {
		t.Fatal(err)
	}

	if err := st.CreateSession(event.Session{
		ID:          "s-current",
		Source:      "claude",
		ProjectPath: dir,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateWorkspace(workspace.Workspace{
		ID:           "ws-current",
		Status:       workspace.StatusFrozen,
		ProjectPath:  dir,
		WorktreePath: dir,
		IsRoot:       true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.LinkWorkspaceSession(workspace.WorkspaceSession{
		WorkspaceID: "ws-current",
		SessionID:   "s-current",
		CreatedAt:   now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveBranchPath(workspace.BranchPathSegment{
		WorkspaceID: "ws-current",
		Depth:       0,
		Ordinal:     0,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertManagedRuntime(store.ManagedRuntime{
		ID:                "rt-root",
		ProjectPath:       dir,
		RootWorkspaceID:   "ws-root",
		ActiveWorkspaceID: "ws-root",
		ActiveSessionID:   "s1",
		Source:            "claude",
		Status:            store.ManagedRuntimeStopped,
		HeartbeatAt:       now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertManagedRuntime(store.ManagedRuntime{
		ID:                "rt-current",
		ProjectPath:       dir,
		RootWorkspaceID:   "ws-current",
		ActiveWorkspaceID: "ws-current",
		ActiveSessionID:   "s-current",
		Source:            "claude",
		Status:            store.ManagedRuntimeStopped,
		HeartbeatAt:       now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertCheckoutLease(store.CheckoutLease{
		CheckoutPath: dir,
		RuntimeID:    "rt-current",
		WorkspaceID:  "ws-current",
		ClaimedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := orch.PruneWorkspaceLineage("ws-root"); err != nil {
		t.Fatalf("prune workspace lineage: %v", err)
	}

	if _, err := st.GetWorkspace("ws-root"); err == nil {
		t.Fatal("expected root workspace metadata to be deleted")
	}
	if _, err := st.GetWorkspace(child.NewWorkspaceID); err == nil {
		t.Fatal("expected child workspace metadata to be deleted")
	}
	if _, err := st.GetManagedRuntime("rt-root"); err == nil {
		t.Fatal("expected root runtime metadata to be deleted")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("expected primary checkout to remain, got %v", err)
	}
	if _, err := os.Stat(child.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("expected child worktree %s to be removed, got %v", child.WorktreePath, err)
	}

	for _, ref := range []string{child.GitRef, HiddenRef("ws-root", 0, workspace.SnapshotPreTool)} {
		cmd := exec.Command("git", "show-ref", "--verify", "--quiet", ref)
		cmd.Dir = dir
		if err := cmd.Run(); err == nil {
			t.Fatalf("expected git ref %s to be removed", ref)
		}
	}
}

func TestDeleteWorkspacePrunesStoppedRootLineage(t *testing.T) {
	st, orch, ce, dir := setupBranchTest(t)
	now := time.Now().UTC()

	if err := ce.CapturePreTool(0); err != nil {
		t.Fatal(err)
	}

	child, err := orch.Branch(BranchRequest{
		SourceWorkspaceID: "ws-root",
		BranchFromSeq:     0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := orch.Freeze(child.NewWorkspaceID); err != nil {
		t.Fatal(err)
	}

	if err := st.CreateSession(event.Session{
		ID:          "s-current",
		Source:      "claude",
		ProjectPath: dir,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateWorkspace(workspace.Workspace{
		ID:           "ws-current",
		Status:       workspace.StatusFrozen,
		ProjectPath:  dir,
		WorktreePath: dir,
		IsRoot:       true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.LinkWorkspaceSession(workspace.WorkspaceSession{
		WorkspaceID: "ws-current",
		SessionID:   "s-current",
		CreatedAt:   now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveBranchPath(workspace.BranchPathSegment{
		WorkspaceID: "ws-current",
		Depth:       0,
		Ordinal:     0,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertManagedRuntime(store.ManagedRuntime{
		ID:                "rt-root",
		ProjectPath:       dir,
		RootWorkspaceID:   "ws-root",
		ActiveWorkspaceID: "ws-root",
		ActiveSessionID:   "s1",
		Source:            "claude",
		Status:            store.ManagedRuntimeStopped,
		HeartbeatAt:       now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertManagedRuntime(store.ManagedRuntime{
		ID:                "rt-current",
		ProjectPath:       dir,
		RootWorkspaceID:   "ws-current",
		ActiveWorkspaceID: "ws-current",
		ActiveSessionID:   "s-current",
		Source:            "claude",
		Status:            store.ManagedRuntimeStopped,
		HeartbeatAt:       now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertCheckoutLease(store.CheckoutLease{
		CheckoutPath: dir,
		RuntimeID:    "rt-current",
		WorkspaceID:  "ws-current",
		ClaimedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}

	if err := orch.DeleteWorkspace("ws-root"); err != nil {
		t.Fatalf("delete root workspace: %v", err)
	}

	if _, err := st.GetWorkspace("ws-root"); err == nil {
		t.Fatal("expected root lineage to be pruned")
	}
	if _, err := st.GetWorkspace(child.NewWorkspaceID); err == nil {
		t.Fatal("expected child workspace to be pruned with root lineage")
	}
}
