package managed

import (
	"database/sql"
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
	Anchor         BranchAnchorResolution
}

// Orchestrator manages branch creation, workspace freezing, and switch activation.
type Orchestrator struct {
	st           *store.Store
	repoDir      string
	worktreeBase string // parent directory for worktrees (e.g., ~/.peek/worktrees)
}

// NewOrchestrator creates a branch/switch orchestrator.
func NewOrchestrator(st *store.Store, repoDir, worktreeBase string) *Orchestrator {
	return &Orchestrator{
		st:           st,
		repoDir:      repoDir,
		worktreeBase: defaultWorktreeBase(worktreeBase),
	}
}

// Branch creates a new workspace from a specific event sequence in the source workspace.
// It freezes the source, resolves the pre-tool checkpoint, and materializes the child.
func (o *Orchestrator) Branch(req BranchRequest) (*BranchResult, error) {
	src, err := o.st.GetWorkspace(req.SourceWorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("get source workspace: %w", err)
	}
	if src.Status != workspace.StatusActive {
		return nil, fmt.Errorf("cannot branch from %s workspace %q", src.Status, src.ID)
	}

	sourceSession, err := o.st.GetLatestWorkspaceSession(req.SourceWorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("get source session: %w", err)
	}

	anchor, err := o.resolveBranchAnchor(req.SourceWorkspaceID, sourceSession.ID, req.BranchFromSeq)
	if err != nil {
		return nil, fmt.Errorf("resolve branch anchor at seq %d: %w", req.BranchFromSeq, err)
	}

	ordinal, err := o.st.NextSiblingOrdinal(req.SourceWorkspaceID, req.BranchFromSeq)
	if err != nil {
		return nil, fmt.Errorf("next sibling ordinal: %w", err)
	}

	now := time.Now().UTC()
	newWSID := fmt.Sprintf("ws-%s", uuid.New().String()[:8])
	newSessID := fmt.Sprintf("%s-managed-%s", sourceSession.Source, uuid.New().String()[:8])

	worktreePath := filepath.Join(o.worktreeBase, newWSID)
	if err := os.MkdirAll(o.worktreeBase, 0o755); err != nil {
		return nil, fmt.Errorf("create worktree base: %w", err)
	}
	if err := MaterializeRef(anchor.GitRef, worktreePath, o.repoDir); err != nil {
		return nil, fmt.Errorf("materialize worktree: %w", err)
	}

	cleanup := func() {
		_ = RemoveWorktree(worktreePath, o.repoDir)
	}

	branchSeq := req.BranchFromSeq
	childWS := workspace.Workspace{
		ID:                newWSID,
		ParentWorkspaceID: req.SourceWorkspaceID,
		Status:            workspace.StatusActive,
		ProjectPath:       src.ProjectPath,
		WorktreePath:      worktreePath,
		GitRef:            anchor.GitRef,
		BranchFromSeq:     &branchSeq,
		SiblingOrdinal:    ordinal,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	sess := event.Session{
		ID:              newSessID,
		Source:          sourceSession.Source,
		ProjectPath:     worktreePath,
		ParentSessionID: sourceSession.ID,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	parentPath, err := o.st.GetBranchPath(req.SourceWorkspaceID)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("get parent branch path: %w", err)
	}
	depth := 0
	if len(parentPath) > 0 {
		depth = parentPath[len(parentPath)-1].Depth + 1
	}
	if err := o.st.CreateBranchedWorkspace(store.BranchedWorkspaceCreate{
		SourceWorkspaceID: req.SourceWorkspaceID,
		ChildWorkspace:    childWS,
		ChildSession:      sess,
		ChildLink: workspace.WorkspaceSession{
			WorkspaceID: newWSID,
			SessionID:   newSessID,
			CreatedAt:   now,
		},
		ChildBranchPath: workspace.BranchPathSegment{
			WorkspaceID:       newWSID,
			ParentWorkspaceID: req.SourceWorkspaceID,
			BranchSeq:         req.BranchFromSeq,
			Ordinal:           ordinal,
			Depth:             depth,
		},
	}); err != nil {
		cleanup()
		return nil, fmt.Errorf("persist child workspace: %w", err)
	}

	return &BranchResult{
		NewWorkspaceID: newWSID,
		NewSessionID:   newSessID,
		WorktreePath:   worktreePath,
		GitRef:         anchor.GitRef,
		Anchor:         anchor,
	}, nil
}

// Switch activates a workspace, re-materializing it if needed.
func (o *Orchestrator) Switch(req SwitchRequest) error {
	ws, err := o.st.GetWorkspace(req.TargetWorkspaceID)
	if err != nil {
		return fmt.Errorf("get target workspace: %w", err)
	}

	if ws.Status == workspace.StatusMerged {
		return fmt.Errorf("cannot switch to merged workspace %q", ws.ID)
	}

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

	rootID, err := o.st.LineageRootWorkspaceID(ws.ID)
	if err != nil {
		return fmt.Errorf("resolve lineage root: %w", err)
	}
	lineage, err := o.st.ListLineageWorkspaces(rootID)
	if err != nil {
		return fmt.Errorf("list lineage workspaces: %w", err)
	}
	for _, candidate := range lineage {
		if candidate.ID == ws.ID {
			continue
		}
		if candidate.Status == workspace.StatusActive {
			if err := o.st.UpdateWorkspaceStatus(candidate.ID, workspace.StatusFrozen); err != nil {
				return fmt.Errorf("freeze active workspace %q: %w", candidate.ID, err)
			}
		}
	}

	if ws.Status == workspace.StatusActive {
		return nil
	}
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

func defaultWorktreeBase(worktreeBase string) string {
	if worktreeBase != "" {
		return worktreeBase
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".peek", "worktrees")
}

func (o *Orchestrator) resolveBranchAnchor(workspaceID, sessionID string, seq int64) (BranchAnchorResolution, error) {
	resolution := BranchAnchorResolution{
		SessionID:   sessionID,
		CutoffSeq:   seq,
		WorkspaceID: workspaceID,
	}

	ev, err := o.st.GetEventBySeq(sessionID, seq)
	if err == nil && ev.Type == event.EventToolCall {
		cp, cpErr := o.st.ResolveCheckpoint(workspaceID, seq, workspace.SnapshotPreTool)
		if cpErr != nil {
			return BranchAnchorResolution{}, cpErr
		}
		resolution.SnapshotSeq = cp.Seq
		resolution.SnapshotKind = cp.Kind
		resolution.GitRef = cp.GitRef
		return resolution, nil
	}
	if err != nil && err != sql.ErrNoRows {
		return BranchAnchorResolution{}, err
	}

	preCp, preErr := o.st.ResolveCheckpoint(workspaceID, seq, workspace.SnapshotPreTool)
	if preErr == nil && preCp.Seq == seq {
		resolution.SnapshotSeq = preCp.Seq
		resolution.SnapshotKind = preCp.Kind
		resolution.GitRef = preCp.GitRef
		return resolution, nil
	}
	if preErr != nil && preErr != sql.ErrNoRows {
		return BranchAnchorResolution{}, preErr
	}

	cp, err := o.st.ResolveCheckpoint(workspaceID, seq, workspace.SnapshotPostTool)
	if err == nil {
		resolution.SnapshotSeq = cp.Seq
		resolution.SnapshotKind = cp.Kind
		resolution.GitRef = cp.GitRef
		return resolution, nil
	}
	if err != sql.ErrNoRows {
		return BranchAnchorResolution{}, err
	}

	cp, err = o.st.ResolveCheckpoint(workspaceID, seq, workspace.SnapshotPreTool)
	if err != nil {
		return BranchAnchorResolution{}, err
	}
	resolution.SnapshotSeq = cp.Seq
	resolution.SnapshotKind = cp.Kind
	resolution.GitRef = cp.GitRef
	return resolution, nil
}
