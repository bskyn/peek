package managed

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/bskyn/peek/internal/event"
	"github.com/bskyn/peek/internal/store"
	"github.com/bskyn/peek/internal/workspace"
)

// BranchRequest describes a branch from a managed workspace.
type BranchRequest struct {
	SourceWorkspaceID string
	BranchFromSeq     int64
}

// SwitchRequest describes switching to a different workspace.
type SwitchRequest struct {
	TargetWorkspaceID string
}

// BranchResult is returned after a successful branch.
type BranchResult struct {
	NewWorkspaceID string
	NewSessionID   string
	WorktreePath   string
	GitRef         string
}

// Orchestrator manages branch creation, workspace freezing, and switch activation.
type Orchestrator struct {
	st           *store.Store
	repoDir      string
	worktreeBase string // parent directory for worktrees (e.g., ~/.peek/worktrees)
}

// NewOrchestrator creates a branch/switch orchestrator.
func NewOrchestrator(st *store.Store, repoDir string) *Orchestrator {
	home, _ := os.UserHomeDir()
	return &Orchestrator{
		st:           st,
		repoDir:      repoDir,
		worktreeBase: filepath.Join(home, ".peek", "worktrees"),
	}
}

// Branch creates a new workspace from a specific event sequence in the source workspace.
// It freezes the source, resolves the pre-tool checkpoint, and materializes the child.
func (o *Orchestrator) Branch(req BranchRequest) (*BranchResult, error) {
	// Validate source workspace
	src, err := o.st.GetWorkspace(req.SourceWorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("get source workspace: %w", err)
	}
	if src.Status != workspace.StatusActive {
		return nil, fmt.Errorf("cannot branch from %s workspace %q", src.Status, src.ID)
	}

	// Resolve pre-tool checkpoint at the branch sequence
	cp, err := o.st.ResolveCheckpoint(req.SourceWorkspaceID, req.BranchFromSeq, workspace.SnapshotPreTool)
	if err != nil {
		return nil, fmt.Errorf("resolve checkpoint at seq %d: %w", req.BranchFromSeq, err)
	}

	// Get next sibling ordinal
	ordinal, err := o.st.NextSiblingOrdinal(req.SourceWorkspaceID, req.BranchFromSeq)
	if err != nil {
		return nil, fmt.Errorf("next sibling ordinal: %w", err)
	}

	now := time.Now().UTC()
	newWSID := fmt.Sprintf("ws-%s", uuid.New().String()[:8])
	newSessID := fmt.Sprintf("%s-managed-%s", src.ProjectPath, uuid.New().String()[:8])

	// Determine worktree path
	worktreePath := filepath.Join(o.worktreeBase, newWSID)

	// Freeze source workspace
	if err := o.st.UpdateWorkspaceStatus(req.SourceWorkspaceID, workspace.StatusFrozen); err != nil {
		return nil, fmt.Errorf("freeze source: %w", err)
	}

	// Create child workspace
	branchSeq := req.BranchFromSeq
	childWS := workspace.Workspace{
		ID:                newWSID,
		ParentWorkspaceID: req.SourceWorkspaceID,
		Status:            workspace.StatusActive,
		ProjectPath:       src.ProjectPath,
		WorktreePath:      worktreePath,
		GitRef:            cp.GitRef,
		BranchFromSeq:     &branchSeq,
		SiblingOrdinal:    ordinal,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := o.st.CreateWorkspace(childWS); err != nil {
		return nil, fmt.Errorf("create child workspace: %w", err)
	}

	// Create session for the new workspace
	sess := event.Session{
		ID:          newSessID,
		Source:      "managed",
		ProjectPath: src.ProjectPath,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := o.st.CreateSession(sess); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	// Link workspace to session
	if err := o.st.LinkWorkspaceSession(workspace.WorkspaceSession{
		WorkspaceID: newWSID,
		SessionID:   newSessID,
		CreatedAt:   now,
	}); err != nil {
		return nil, fmt.Errorf("link workspace session: %w", err)
	}

	// Save branch path segment
	parentPath, err := o.st.GetBranchPath(req.SourceWorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("get parent branch path: %w", err)
	}
	depth := 0
	if len(parentPath) > 0 {
		depth = parentPath[len(parentPath)-1].Depth + 1
	}
	if err := o.st.SaveBranchPath(workspace.BranchPathSegment{
		WorkspaceID:       newWSID,
		ParentWorkspaceID: req.SourceWorkspaceID,
		BranchSeq:         req.BranchFromSeq,
		Ordinal:           ordinal,
		Depth:             depth,
	}); err != nil {
		return nil, fmt.Errorf("save branch path: %w", err)
	}

	// Materialize worktree from the checkpoint ref
	if err := os.MkdirAll(o.worktreeBase, 0o755); err != nil {
		return nil, fmt.Errorf("create worktree base: %w", err)
	}
	if err := MaterializeRef(cp.GitRef, worktreePath, o.repoDir); err != nil {
		return nil, fmt.Errorf("materialize worktree: %w", err)
	}

	return &BranchResult{
		NewWorkspaceID: newWSID,
		NewSessionID:   newSessID,
		WorktreePath:   worktreePath,
		GitRef:         cp.GitRef,
	}, nil
}

// Switch activates a workspace, re-materializing it if needed.
func (o *Orchestrator) Switch(req SwitchRequest) error {
	ws, err := o.st.GetWorkspace(req.TargetWorkspaceID)
	if err != nil {
		return fmt.Errorf("get target workspace: %w", err)
	}

	// If already active, nothing to do
	if ws.Status == workspace.StatusActive {
		return nil
	}

	if ws.Status == workspace.StatusMerged {
		return fmt.Errorf("cannot switch to merged workspace %q", ws.ID)
	}

	// Re-materialize if worktree path is empty or doesn't exist
	if ws.WorktreePath == "" || !pathExists(ws.WorktreePath) {
		if ws.GitRef == "" {
			return fmt.Errorf("workspace %q has no git ref to materialize", ws.ID)
		}
		worktreePath := filepath.Join(o.worktreeBase, ws.ID)
		if err := os.MkdirAll(o.worktreeBase, 0o755); err != nil {
			return fmt.Errorf("create worktree base: %w", err)
		}
		if err := MaterializeRef(ws.GitRef, worktreePath, o.repoDir); err != nil {
			return fmt.Errorf("materialize worktree: %w", err)
		}
		if err := o.st.UpdateWorkspaceWorktree(ws.ID, worktreePath); err != nil {
			return fmt.Errorf("update worktree path: %w", err)
		}
	}

	// Activate the workspace
	return o.st.UpdateWorkspaceStatus(req.TargetWorkspaceID, workspace.StatusActive)
}

// Freeze sets a workspace to frozen status.
func (o *Orchestrator) Freeze(workspaceID string) error {
	return o.st.UpdateWorkspaceStatus(workspaceID, workspace.StatusFrozen)
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
