package managed

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/bskyn/peek/internal/workspace"
)

// DeleteWorkspace removes an inactive leaf workspace and cleans up its
// materialized worktree plus Peek-managed git refs.
func (o *Orchestrator) DeleteWorkspace(workspaceID string) error {
	ws, err := o.st.GetWorkspace(workspaceID)
	if err != nil {
		return fmt.Errorf("get workspace: %w", err)
	}

	if ws.IsRoot {
		return fmt.Errorf("cannot delete root workspace %q", workspaceID)
	}
	if ws.Status == workspace.StatusActive {
		return fmt.Errorf("cannot delete active workspace %q — switch or freeze it first", workspaceID)
	}

	children, err := o.st.ListChildWorkspaces(workspaceID)
	if err != nil {
		return fmt.Errorf("list child workspaces: %w", err)
	}
	if len(children) > 0 {
		return fmt.Errorf("cannot delete workspace %q — delete child workspaces first", workspaceID)
	}

	refs, err := o.workspaceRefs(workspaceID, ws.GitRef)
	if err != nil {
		return fmt.Errorf("list workspace refs: %w", err)
	}

	if ws.WorktreePath != "" && pathExists(ws.WorktreePath) {
		if err := RemoveWorktree(ws.WorktreePath, o.repoDir); err != nil {
			return fmt.Errorf("remove worktree: %w", err)
		}
	}

	deleted, err := o.st.DeleteWorkspace(workspaceID)
	if err != nil {
		return fmt.Errorf("delete workspace metadata: %w", err)
	}
	if !deleted {
		return fmt.Errorf("workspace %q not found", workspaceID)
	}

	for _, ref := range refs {
		if err := deleteGitRef(o.repoDir, ref); err != nil {
			return fmt.Errorf("workspace metadata deleted, but cleanup of git ref %q failed: %w", ref, err)
		}
	}

	return nil
}

func (o *Orchestrator) workspaceRefs(workspaceID, workspaceRef string) ([]string, error) {
	seen := make(map[string]struct{})
	refs := make([]string, 0)
	addRef := func(ref string) {
		if !strings.HasPrefix(ref, "refs/peek/") {
			return
		}
		if _, ok := seen[ref]; ok {
			return
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}

	addRef(workspaceRef)

	checkpoints, err := o.st.ListCheckpoints(workspaceID)
	if err != nil {
		return nil, err
	}
	for _, cp := range checkpoints {
		addRef(cp.GitRef)
	}

	return refs, nil
}

func deleteGitRef(repoDir, ref string) error {
	if ref == "" {
		return nil
	}

	verify := exec.Command("git", "show-ref", "--verify", "--quiet", ref)
	verify.Dir = repoDir
	if err := verify.Run(); err != nil {
		return nil
	}

	cmd := exec.Command("git", "update-ref", "-d", ref)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}
