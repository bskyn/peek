package managed

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/bskyn/peek/internal/store"
	"github.com/bskyn/peek/internal/workspace"
)

// CheckpointEngine captures deterministic pre-tool and post-tool workspace
// snapshots as hidden git refs. It ensures that branching from any event
// sequence resolves to the correct code state.
type CheckpointEngine struct {
	st          *store.Store
	workspaceID string
	sessionID   string
	projectDir  string
	mu          sync.Mutex
	lastPostSeq int64 // tracks the last post-tool checkpoint to avoid duplicates
}

// NewCheckpointEngine creates a checkpoint engine for a managed workspace.
func NewCheckpointEngine(st *store.Store, workspaceID, sessionID, projectDir string) *CheckpointEngine {
	return &CheckpointEngine{
		st:          st,
		workspaceID: workspaceID,
		sessionID:   sessionID,
		projectDir:  projectDir,
		lastPostSeq: -1,
	}
}

// CapturePreTool creates a snapshot of the workspace before a tool modifies code.
func (ce *CheckpointEngine) CapturePreTool(seq int64) error {
	return ce.capture(seq, workspace.SnapshotPreTool)
}

// CapturePostTool creates a snapshot of the workspace after a tool modifies code.
func (ce *CheckpointEngine) CapturePostTool(seq int64) error {
	ce.mu.Lock()
	if seq == ce.lastPostSeq {
		ce.mu.Unlock()
		return nil // duplicate, skip
	}
	ce.mu.Unlock()

	if err := ce.capture(seq, workspace.SnapshotPostTool); err != nil {
		return err
	}

	ce.mu.Lock()
	ce.lastPostSeq = seq
	ce.mu.Unlock()
	return nil
}

func (ce *CheckpointEngine) capture(seq int64, kind workspace.SnapshotKind) error {
	ref := hiddenRef(ce.workspaceID, seq, kind)

	// Create a synthetic commit capturing the current worktree state
	// using a temporary index so we never touch the user's real index.
	treeHash, err := writeTreeFromWorktree(ce.projectDir)
	if err != nil {
		return fmt.Errorf("write tree: %w", err)
	}

	commitHash, err := ce.commitTree(treeHash, seq, kind)
	if err != nil {
		return fmt.Errorf("commit tree: %w", err)
	}

	// Store as hidden ref
	if err := ce.updateRef(ref, commitHash); err != nil {
		return fmt.Errorf("update ref: %w", err)
	}

	// Persist to database
	cp := workspace.CheckpointRef{
		ID:          store.CheckpointID(ce.workspaceID, seq, kind),
		WorkspaceID: ce.workspaceID,
		SessionID:   ce.sessionID,
		Seq:         seq,
		Kind:        kind,
		GitRef:      ref,
		CreatedAt:   time.Now().UTC(),
	}
	return ce.st.CreateCheckpoint(cp)
}

// writeTreeFromWorktree creates a git tree object from the worktree state
// using a temporary index file so the user's real index is never modified.
func writeTreeFromWorktree(worktreeDir string) (string, error) {
	// Create a unique temp path for the index. We remove it immediately so git
	// creates a fresh index file (an empty file is invalid as a git index).
	tmpIndex, err := os.CreateTemp("", "peek-index-*")
	if err != nil {
		return "", fmt.Errorf("create temp index: %w", err)
	}
	tmpIndexPath := tmpIndex.Name()
	tmpIndex.Close()
	os.Remove(tmpIndexPath)
	defer os.Remove(tmpIndexPath)

	env := append(os.Environ(), "GIT_INDEX_FILE="+tmpIndexPath)

	// Stage all files into the temporary index
	cmd := exec.Command("git", "add", "-A")
	cmd.Dir = worktreeDir
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git add: %s: %w", out, err)
	}

	// Write the tree from the temporary index
	cmd = exec.Command("git", "write-tree")
	cmd.Dir = worktreeDir
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git write-tree: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// commitTree creates a synthetic commit from a tree hash.
func (ce *CheckpointEngine) commitTree(treeHash string, seq int64, kind workspace.SnapshotKind) (string, error) {
	msg := fmt.Sprintf("peek: %s checkpoint at seq %d for %s", kind, seq, ce.workspaceID)

	// Try to get HEAD as parent
	headCmd := exec.Command("git", "rev-parse", "HEAD")
	headCmd.Dir = ce.projectDir
	headOut, headErr := headCmd.Output()

	var args []string
	if headErr == nil {
		parent := strings.TrimSpace(string(headOut))
		args = []string{"commit-tree", treeHash, "-p", parent, "-m", msg}
	} else {
		args = []string{"commit-tree", treeHash, "-m", msg}
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = ce.projectDir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git commit-tree: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// updateRef stores a commit under a hidden ref.
func (ce *CheckpointEngine) updateRef(ref, commitHash string) error {
	cmd := exec.Command("git", "update-ref", ref, commitHash)
	cmd.Dir = ce.projectDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git update-ref %s: %s: %w", ref, out, err)
	}
	return nil
}

// hiddenRef returns the hidden ref path for a checkpoint.
func hiddenRef(workspaceID string, seq int64, kind workspace.SnapshotKind) string {
	return fmt.Sprintf("refs/peek/%s/%d/%s", workspaceID, seq, kind)
}

// HiddenRef is the exported form for use by other packages.
func HiddenRef(workspaceID string, seq int64, kind workspace.SnapshotKind) string {
	return hiddenRef(workspaceID, seq, kind)
}

// MaterializeRef checks out a hidden ref into the given worktree path.
func MaterializeRef(gitRef, worktreePath, repoDir string) error {
	cmd := exec.Command("git", "worktree", "add", "--detach", worktreePath, gitRef)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree add: %s: %w", out, err)
	}
	return nil
}

// RemoveWorktree removes a git worktree.
func RemoveWorktree(worktreePath, repoDir string) error {
	cmd := exec.Command("git", "worktree", "remove", "--force", worktreePath)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree remove: %s: %w", out, err)
	}
	return nil
}
