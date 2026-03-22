package managed

import (
	"database/sql"
	"fmt"
	"os/exec"
	"strings"

	"github.com/bskyn/peek/internal/store"
	"github.com/bskyn/peek/internal/workspace"
)

// DeleteWorkspace removes an inactive leaf workspace. Root workspaces are
// deleted via the lineage-prune path so runtime metadata is cleaned up too.
func (o *Orchestrator) DeleteWorkspace(workspaceID string) error {
	ws, err := o.st.GetWorkspace(workspaceID)
	if err != nil {
		return fmt.Errorf("get workspace: %w", err)
	}

	if ws.IsRoot {
		_, err := o.PruneWorkspaceLineage(workspaceID)
		return err
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

// PruneWorkspaceLineageResult describes one pruned root lineage.
type PruneWorkspaceLineageResult struct {
	RootWorkspaceID string
	WorkspaceIDs    []string
	RuntimeIDs      []string
}

// PruneWorkspaceLineage removes a stopped root workspace lineage and its
// associated managed-runtime metadata.
func (o *Orchestrator) PruneWorkspaceLineage(rootWorkspaceID string) (*PruneWorkspaceLineageResult, error) {
	root, err := o.st.GetWorkspace(rootWorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("get root workspace: %w", err)
	}
	if !root.IsRoot {
		return nil, fmt.Errorf("workspace %q is not a root workspace", rootWorkspaceID)
	}

	if lease, err := o.st.GetCheckoutLease(root.ProjectPath); err == nil {
		if lease.WorkspaceID == rootWorkspaceID {
			return nil, fmt.Errorf("cannot prune root workspace %q while it owns the current checkout lease", rootWorkspaceID)
		}
	} else if err != sql.ErrNoRows {
		return nil, fmt.Errorf("load checkout lease: %w", err)
	}

	runtime, err := o.st.GetManagedRuntimeByRootWorkspace(rootWorkspaceID)
	if err == nil {
		if runtime.Status != store.ManagedRuntimeStopped {
			return nil, fmt.Errorf("cannot prune root workspace %q while managed runtime %q is %s", rootWorkspaceID, runtime.ID, runtime.Status)
		}
	} else if err != sql.ErrNoRows {
		return nil, fmt.Errorf("load root runtime: %w", err)
	}

	lineage, err := o.st.ListLineageWorkspaces(rootWorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("list lineage workspaces: %w", err)
	}

	// Freeze orphaned active workspaces — the runtime is confirmed stopped,
	// so any remaining active status is stale.
	for i, ws := range lineage {
		if ws.Status == workspace.StatusActive {
			if err := o.st.UpdateWorkspaceStatus(ws.ID, workspace.StatusFrozen); err != nil {
				return nil, fmt.Errorf("freeze orphaned workspace %q: %w", ws.ID, err)
			}
			lineage[i].Status = workspace.StatusFrozen
		}
	}

	refs := make([]string, 0)
	seenRefs := make(map[string]struct{})
	addRef := func(ref string) {
		if !strings.HasPrefix(ref, "refs/peek/") {
			return
		}
		if _, ok := seenRefs[ref]; ok {
			return
		}
		seenRefs[ref] = struct{}{}
		refs = append(refs, ref)
	}

	for _, ws := range lineage {
		addRef(ws.GitRef)
		checkpoints, err := o.st.ListCheckpoints(ws.ID)
		if err != nil {
			return nil, fmt.Errorf("list checkpoints for %s: %w", ws.ID, err)
		}
		for _, cp := range checkpoints {
			addRef(cp.GitRef)
		}
	}

	for i := len(lineage) - 1; i >= 0; i-- {
		ws := lineage[i]
		if ws.WorktreePath == "" || !pathExists(ws.WorktreePath) || ws.IsPrimaryCheckout() {
			continue
		}
		if err := RemoveWorktree(ws.WorktreePath, o.repoDir); err != nil {
			return nil, fmt.Errorf("remove worktree for %s: %w", ws.ID, err)
		}
	}

	deleted, workspaceIDs, runtimeIDs, err := o.st.PruneWorkspaceLineage(rootWorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("delete workspace lineage metadata: %w", err)
	}
	if !deleted {
		return nil, fmt.Errorf("workspace lineage %q not found", rootWorkspaceID)
	}

	for _, ref := range refs {
		if err := deleteGitRef(o.repoDir, ref); err != nil {
			return nil, fmt.Errorf("workspace lineage metadata deleted, but cleanup of git ref %q failed: %w", ref, err)
		}
	}

	return &PruneWorkspaceLineageResult{
		RootWorkspaceID: rootWorkspaceID,
		WorkspaceIDs:    workspaceIDs,
		RuntimeIDs:      runtimeIDs,
	}, nil
}
