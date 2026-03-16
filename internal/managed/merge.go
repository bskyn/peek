package managed

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/bskyn/peek/internal/workspace"
)

// MergeRequest describes merging a branch workspace back into its source.
type MergeRequest struct {
	BranchWorkspaceID string
	TargetWorkspaceID string // if empty, merges into parent
}

// MergeResult describes the outcome of a merge.
type MergeResult struct {
	Clean          bool
	ConflictFiles  []string
	TargetWorktree string // path to resolve conflicts in
}

// Merge merges the branch workspace code into the target workspace.
// On conflict, it stops and reports the target worktree for manual resolution.
func (o *Orchestrator) Merge(req MergeRequest) (*MergeResult, error) {
	branch, err := o.st.GetWorkspace(req.BranchWorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("get branch workspace: %w", err)
	}

	targetID := req.TargetWorkspaceID
	if targetID == "" {
		targetID = branch.ParentWorkspaceID
	}
	if targetID == "" {
		return nil, fmt.Errorf("branch %q has no parent to merge into", req.BranchWorkspaceID)
	}

	target, err := o.st.GetWorkspace(targetID)
	if err != nil {
		return nil, fmt.Errorf("get target workspace: %w", err)
	}

	if target.GitRef == "" && target.WorktreePath == "" {
		return nil, fmt.Errorf("target workspace %q has no git ref or worktree", targetID)
	}

	targetWorktree := target.WorktreePath
	if targetWorktree == "" || !pathExists(targetWorktree) {
		return nil, fmt.Errorf("target workspace %q worktree not materialized", targetID)
	}

	mergeRef := branch.GitRef
	if branch.WorktreePath != "" && pathExists(branch.WorktreePath) {
		mergeRef, err = captureWorktreeAsRef(branch.WorktreePath, req.BranchWorkspaceID, o.repoDir)
		if err != nil {
			return nil, fmt.Errorf("capture live merge source: %w", err)
		}
		if err := o.st.UpdateWorkspaceGitRef(req.BranchWorkspaceID, mergeRef); err != nil {
			return nil, fmt.Errorf("update live merge ref: %w", err)
		}
	}
	if mergeRef == "" {
		return nil, fmt.Errorf("branch workspace %q has no git ref", req.BranchWorkspaceID)
	}

	mergeResult, err := gitMerge(targetWorktree, mergeRef)
	if err != nil {
		return nil, fmt.Errorf("git merge: %w", err)
	}

	if mergeResult.Clean {
		if err := o.st.UpdateWorkspaceStatus(req.BranchWorkspaceID, workspace.StatusMerged); err != nil {
			return nil, fmt.Errorf("mark branch merged: %w", err)
		}
	} else if err := o.st.UpdateWorkspaceStatus(req.BranchWorkspaceID, workspace.StatusConflict); err != nil {
		return nil, fmt.Errorf("mark branch conflicted: %w", err)
	}

	mergeResult.TargetWorktree = targetWorktree
	return mergeResult, nil
}

// CoolWorkspace dematerializes a workspace down to ref-only storage.
func (o *Orchestrator) CoolWorkspace(workspaceID string) error {
	ws, err := o.st.GetWorkspace(workspaceID)
	if err != nil {
		return fmt.Errorf("get workspace: %w", err)
	}

	if ws.Status == workspace.StatusActive {
		return fmt.Errorf("cannot cool active workspace %q — freeze it first", workspaceID)
	}

	if ws.WorktreePath == "" || !pathExists(ws.WorktreePath) {
		return nil // already cold
	}

	// Never remove the primary checkout — it's the user's actual repo directory.
	if ws.IsPrimaryCheckout() {
		return fmt.Errorf("cannot cool root workspace %q — its worktree is the primary checkout", workspaceID)
	}

	// Always capture the current worktree state before removing it.
	// The existing git_ref may point to the original checkpoint, not the current state.
	ref, err := captureWorktreeAsRef(ws.WorktreePath, workspaceID, o.repoDir)
	if err != nil {
		return fmt.Errorf("capture worktree state: %w", err)
	}
	if err := o.st.UpdateWorkspaceGitRef(workspaceID, ref); err != nil {
		return fmt.Errorf("update git ref: %w", err)
	}

	// Remove the linked worktree. Do NOT fall back to os.RemoveAll —
	// if git worktree remove fails, the worktree stays on disk for manual cleanup.
	if err := RemoveWorktree(ws.WorktreePath, o.repoDir); err != nil {
		return fmt.Errorf("remove worktree: %w", err)
	}

	// Clear worktree path
	return o.st.UpdateWorkspaceWorktree(workspaceID, "")
}

// gitMerge attempts a merge of the given ref into the worktree.
func gitMerge(worktreeDir, ref string) (*MergeResult, error) {
	cmd := exec.Command("git", "merge", "--no-ff", "--no-edit", ref)
	cmd.Dir = worktreeDir
	out, err := cmd.CombinedOutput()

	if err == nil {
		return &MergeResult{Clean: true}, nil
	}

	// Check for merge conflicts
	output := string(out)
	if strings.Contains(output, "CONFLICT") || strings.Contains(output, "Automatic merge failed") {
		conflicts := parseConflictFiles(worktreeDir)
		return &MergeResult{
			Clean:         false,
			ConflictFiles: conflicts,
		}, nil
	}

	return nil, fmt.Errorf("merge failed: %s", output)
}

// parseConflictFiles finds unmerged files in the worktree.
func parseConflictFiles(worktreeDir string) []string {
	cmd := exec.Command("git", "diff", "--name-only", "--diff-filter=U")
	cmd.Dir = worktreeDir
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}

// CaptureWorktreeAsRef creates a hidden ref from the current worktree state.
// Uses a temporary GIT_INDEX_FILE so the user's real index is never modified.
func CaptureWorktreeAsRef(worktreeDir, workspaceID, repoDir, refSuffix string) (string, error) {
	// Write tree using temporary index (never touches the real index)
	treeHash, err := writeTreeFromWorktree(worktreeDir)
	if err != nil {
		return "", fmt.Errorf("write tree: %w", err)
	}

	// Get HEAD as parent
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = worktreeDir
	headOut, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("rev-parse HEAD: %w", err)
	}
	parent := strings.TrimSpace(string(headOut))

	// Commit tree
	msg := fmt.Sprintf("peek: cool snapshot for %s", workspaceID)
	cmd = exec.Command("git", "commit-tree", treeHash, "-p", parent, "-m", msg)
	cmd.Dir = worktreeDir
	commitOut, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("commit-tree: %w", err)
	}
	commitHash := strings.TrimSpace(string(commitOut))

	// Store as hidden ref
	if refSuffix == "" {
		refSuffix = "live"
	}
	ref := fmt.Sprintf("refs/peek/%s/%s", workspaceID, refSuffix)
	cmd = exec.Command("git", "update-ref", ref, commitHash)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("update-ref: %s: %w", out, err)
	}

	return ref, nil
}

func captureWorktreeAsRef(worktreeDir, workspaceID, repoDir string) (string, error) {
	return CaptureWorktreeAsRef(worktreeDir, workspaceID, repoDir, "cool")
}
