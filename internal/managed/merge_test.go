package managed

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bskyn/peek/internal/workspace"
)

func TestMergeClean(t *testing.T) {
	st, orch, ce, dir := setupBranchTest(t)

	// Capture pre-tool checkpoint
	if err := ce.CapturePreTool(0); err != nil {
		t.Fatal(err)
	}

	// Branch from seq 0
	result, err := orch.Branch(BranchRequest{
		SourceWorkspaceID: "ws-root",
		BranchFromSeq:     0,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Make a change in the branch worktree
	if err := os.WriteFile(filepath.Join(result.WorktreePath, "branch_file.txt"), []byte("from branch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitInDir(t, result.WorktreePath, "branch change")

	// Capture the branch state as a git ref
	ref, err := captureWorktreeAsRef(result.WorktreePath, result.NewWorkspaceID, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateWorkspaceGitRef(result.NewWorkspaceID, ref); err != nil {
		t.Fatal(err)
	}

	// Re-activate source for merge target
	if err := st.UpdateWorkspaceStatus("ws-root", workspace.StatusActive); err != nil {
		t.Fatal(err)
	}

	// Merge
	mergeResult, err := orch.Merge(MergeRequest{
		BranchWorkspaceID: result.NewWorkspaceID,
	})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	if !mergeResult.Clean {
		t.Errorf("expected clean merge, got conflicts: %v", mergeResult.ConflictFiles)
	}

	// Verify branch is now merged
	branch, err := st.GetWorkspace(result.NewWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if branch.Status != workspace.StatusMerged {
		t.Errorf("expected merged status, got %s", branch.Status)
	}

	// Verify file exists in target worktree
	if _, err := os.Stat(filepath.Join(dir, "branch_file.txt")); err != nil {
		t.Errorf("branch_file.txt should be in target after merge: %v", err)
	}
}

func TestMergeConflict(t *testing.T) {
	st, orch, ce, dir := setupBranchTest(t)

	// Capture pre-tool checkpoint
	if err := ce.CapturePreTool(0); err != nil {
		t.Fatal(err)
	}

	// Branch from seq 0
	result, err := orch.Branch(BranchRequest{
		SourceWorkspaceID: "ws-root",
		BranchFromSeq:     0,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Modify same file in both worktrees
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("source change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitInDir(t, dir, "source change")

	if err := os.WriteFile(filepath.Join(result.WorktreePath, "README.md"), []byte("branch change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitInDir(t, result.WorktreePath, "branch change")

	// Capture branch state
	ref, err := captureWorktreeAsRef(result.WorktreePath, result.NewWorkspaceID, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateWorkspaceGitRef(result.NewWorkspaceID, ref); err != nil {
		t.Fatal(err)
	}

	// Re-activate source
	if err := st.UpdateWorkspaceStatus("ws-root", workspace.StatusActive); err != nil {
		t.Fatal(err)
	}

	// Merge should detect conflict
	mergeResult, err := orch.Merge(MergeRequest{
		BranchWorkspaceID: result.NewWorkspaceID,
	})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	if mergeResult.Clean {
		t.Error("expected conflict, got clean merge")
	}
	if len(mergeResult.ConflictFiles) == 0 {
		t.Error("expected conflict files list to be non-empty")
	}

	// Branch should NOT be marked as merged (conflict pending)
	branch, err := st.GetWorkspace(result.NewWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if branch.Status == workspace.StatusMerged {
		t.Error("branch should not be merged when conflicts exist")
	}

	// Abort the merge to clean up
	cmd := exec.Command("git", "merge", "--abort")
	cmd.Dir = dir
	_ = cmd.Run()
}

func TestCoolWorkspace(t *testing.T) {
	st, orch, ce, dir := setupBranchTest(t)

	// Capture checkpoint and branch
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

	// Make a change in the branch and commit it
	if err := os.WriteFile(filepath.Join(result.WorktreePath, "cool_test.txt"), []byte("data\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitInDir(t, result.WorktreePath, "add cool_test.txt")

	// Freeze the child before cooling
	if err := orch.Freeze(result.NewWorkspaceID); err != nil {
		t.Fatal(err)
	}

	// Cool the workspace
	if err := orch.CoolWorkspace(result.NewWorkspaceID); err != nil {
		t.Fatalf("cool workspace: %v", err)
	}

	// Worktree should be removed
	ws, err := st.GetWorkspace(result.NewWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if ws.WorktreePath != "" {
		t.Errorf("expected empty worktree_path after cool, got %q", ws.WorktreePath)
	}
	if _, err := os.Stat(result.WorktreePath); !os.IsNotExist(err) {
		t.Error("worktree directory should be removed after cool")
	}

	// Git ref should be set
	if ws.GitRef == "" {
		t.Error("expected git ref to be set after cool")
	}

	// Should be able to switch back (re-materialize)
	if err := orch.Switch(SwitchRequest{TargetWorkspaceID: result.NewWorkspaceID}); err != nil {
		t.Fatalf("switch after cool: %v", err)
	}

	ws, err = st.GetWorkspace(result.NewWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if ws.Status != workspace.StatusActive {
		t.Errorf("expected active after switch, got %s", ws.Status)
	}
	if ws.WorktreePath == "" {
		t.Error("expected worktree_path to be set after re-materialization")
	}

	// Verify the file we added is present after re-materialization
	if _, err := os.Stat(filepath.Join(ws.WorktreePath, "cool_test.txt")); err != nil {
		t.Errorf("cool_test.txt should exist after re-materialization: %v", err)
	}

	// Cleanup
	_ = RemoveWorktree(ws.WorktreePath, dir)
}

func TestCoolActiveWorkspaceFails(t *testing.T) {
	_, orch, _, _ := setupBranchTest(t)

	err := orch.CoolWorkspace("ws-root")
	if err == nil {
		t.Fatal("expected error cooling active workspace")
	}
}

func TestCoolRootWorkspaceFails(t *testing.T) {
	st, orch, _, _ := setupBranchTest(t)

	// Freeze the root workspace first (cool requires frozen)
	if err := st.UpdateWorkspaceStatus("ws-root", workspace.StatusFrozen); err != nil {
		t.Fatal(err)
	}

	// Cooling a root workspace whose worktree is the primary checkout must be refused
	err := orch.CoolWorkspace("ws-root")
	if err == nil {
		t.Fatal("expected error cooling root workspace with primary checkout")
	}
	if !strings.Contains(err.Error(), "primary checkout") {
		t.Errorf("expected error about primary checkout, got: %v", err)
	}
}

func commitInDir(t *testing.T, dir, msg string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %s: %v", args, dir, out, err)
		}
	}
	run("add", "-A")
	run("commit", "-m", msg)
}
