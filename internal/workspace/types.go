package workspace

import "time"

// WorkspaceStatus represents the lifecycle state of a managed workspace.
type WorkspaceStatus string

const (
	StatusActive       WorkspaceStatus = "active"
	StatusFrozen       WorkspaceStatus = "frozen"
	StatusMergePending WorkspaceStatus = "merge_pending"
	StatusConflict     WorkspaceStatus = "conflict"
	StatusMerged       WorkspaceStatus = "merged"
)

// SnapshotKind distinguishes pre-tool and post-tool code snapshots.
type SnapshotKind string

const (
	SnapshotPreTool  SnapshotKind = "pre_tool"
	SnapshotPostTool SnapshotKind = "post_tool"
)

// Workspace represents a managed worktree with branch lineage.
type Workspace struct {
	ID                string          `json:"id"`
	ParentWorkspaceID string          `json:"parent_workspace_id,omitempty"`
	Status            WorkspaceStatus `json:"status"`
	ProjectPath       string          `json:"project_path"`
	WorktreePath      string          `json:"worktree_path,omitempty"`
	GitRef            string          `json:"git_ref,omitempty"`
	BranchFromSeq     *int64          `json:"branch_from_seq,omitempty"`
	SiblingOrdinal    int             `json:"sibling_ordinal"`
	IsRoot            bool            `json:"is_root"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

// IsPrimaryCheckout reports whether this workspace's worktree is the main repo checkout
// (not a git-worktree-managed linked worktree). Primary checkouts must never be removed.
func (w *Workspace) IsPrimaryCheckout() bool {
	return w.IsRoot && w.WorktreePath == w.ProjectPath
}

// WorkspaceSession links a workspace to a session.
type WorkspaceSession struct {
	WorkspaceID string    `json:"workspace_id"`
	SessionID   string    `json:"session_id"`
	CreatedAt   time.Time `json:"created_at"`
}

// CheckpointRef records a code snapshot at a specific event sequence.
type CheckpointRef struct {
	ID          string       `json:"id"`
	WorkspaceID string       `json:"workspace_id"`
	SessionID   string       `json:"session_id"`
	Seq         int64        `json:"seq"`
	Kind        SnapshotKind `json:"kind"`
	GitRef      string       `json:"git_ref"`
	CreatedAt   time.Time    `json:"created_at"`
}

// BranchPathSegment is one hop in the breadcrumb trail from root to a workspace.
type BranchPathSegment struct {
	WorkspaceID       string `json:"workspace_id"`
	ParentWorkspaceID string `json:"parent_workspace_id,omitempty"`
	BranchSeq         int64  `json:"branch_seq"`
	Ordinal           int    `json:"ordinal"`
	Depth             int    `json:"depth"`
}
