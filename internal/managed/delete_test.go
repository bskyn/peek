package managed

import (
	"os"
	"os/exec"
	"testing"
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
