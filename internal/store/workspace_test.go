package store

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/bskyn/peek/internal/event"
	"github.com/bskyn/peek/internal/workspace"
)

func TestCreateAndGetWorkspace(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	w := workspace.Workspace{
		ID:          "ws-1",
		Status:      workspace.StatusActive,
		ProjectPath: "/test/project",
		GitRef:      "refs/peek/ws-1",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := s.CreateWorkspace(w); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.GetWorkspace("ws-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.ID != w.ID || got.Status != w.Status || got.ProjectPath != w.ProjectPath || got.GitRef != w.GitRef {
		t.Errorf("workspace mismatch: got %+v", got)
	}
	if got.BranchFromSeq != nil {
		t.Errorf("expected nil BranchFromSeq, got %v", *got.BranchFromSeq)
	}
}

func TestCreateWorkspaceWithBranch(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	// Create parent
	parent := workspace.Workspace{
		ID:          "ws-parent",
		Status:      workspace.StatusActive,
		ProjectPath: "/test/project",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.CreateWorkspace(parent); err != nil {
		t.Fatalf("create parent: %v", err)
	}

	// Create child branched from seq 5
	branchSeq := int64(5)
	child := workspace.Workspace{
		ID:                "ws-child",
		ParentWorkspaceID: "ws-parent",
		Status:            workspace.StatusActive,
		ProjectPath:       "/test/project",
		BranchFromSeq:     &branchSeq,
		SiblingOrdinal:    0,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := s.CreateWorkspace(child); err != nil {
		t.Fatalf("create child: %v", err)
	}

	got, err := s.GetWorkspace("ws-child")
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if got.ParentWorkspaceID != "ws-parent" {
		t.Errorf("expected parent ws-parent, got %q", got.ParentWorkspaceID)
	}
	if got.BranchFromSeq == nil || *got.BranchFromSeq != 5 {
		t.Errorf("expected BranchFromSeq=5, got %v", got.BranchFromSeq)
	}
}

func TestWorkspaceUpsertPreservesFields(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	w := workspace.Workspace{
		ID:           "ws-1",
		Status:       workspace.StatusActive,
		ProjectPath:  "/test/project",
		WorktreePath: "/tmp/worktree",
		GitRef:       "refs/peek/ws-1",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.CreateWorkspace(w); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Upsert with empty worktree_path and git_ref — should preserve originals
	w2 := workspace.Workspace{
		ID:          "ws-1",
		Status:      workspace.StatusFrozen,
		ProjectPath: "/test/project",
		CreatedAt:   now,
		UpdatedAt:   now.Add(time.Second),
	}
	if err := s.CreateWorkspace(w2); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := s.GetWorkspace("ws-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != workspace.StatusFrozen {
		t.Errorf("expected frozen status, got %s", got.Status)
	}
	if got.WorktreePath != "/tmp/worktree" {
		t.Errorf("expected preserved worktree_path, got %q", got.WorktreePath)
	}
	if got.GitRef != "refs/peek/ws-1" {
		t.Errorf("expected preserved git_ref, got %q", got.GitRef)
	}
}

func TestUpdateWorkspaceStatus(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	w := workspace.Workspace{
		ID:          "ws-1",
		Status:      workspace.StatusActive,
		ProjectPath: "/test",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.CreateWorkspace(w); err != nil {
		t.Fatal(err)
	}

	if err := s.UpdateWorkspaceStatus("ws-1", workspace.StatusFrozen); err != nil {
		t.Fatalf("update status: %v", err)
	}

	got, err := s.GetWorkspace("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != workspace.StatusFrozen {
		t.Errorf("expected frozen, got %s", got.Status)
	}

	// Non-existent workspace
	if err := s.UpdateWorkspaceStatus("missing", workspace.StatusFrozen); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected sql.ErrNoRows for missing workspace, got %v", err)
	}
}

func TestListWorkspaces(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	for i := 0; i < 3; i++ {
		w := workspace.Workspace{
			ID:          fmt.Sprintf("ws-%d", i),
			Status:      workspace.StatusActive,
			ProjectPath: "/test",
			CreatedAt:   now.Add(time.Duration(i) * time.Second),
			UpdatedAt:   now.Add(time.Duration(i) * time.Second),
		}
		if err := s.CreateWorkspace(w); err != nil {
			t.Fatal(err)
		}
	}

	summaries, err := s.ListWorkspaces()
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 3 {
		t.Fatalf("expected 3 workspaces, got %d", len(summaries))
	}
	// Newest first
	if summaries[0].ID != "ws-2" {
		t.Errorf("expected newest first, got %s", summaries[0].ID)
	}
}

func TestListChildWorkspaces(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	parent := workspace.Workspace{
		ID: "ws-root", Status: workspace.StatusActive, ProjectPath: "/test",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CreateWorkspace(parent); err != nil {
		t.Fatal(err)
	}

	branchSeq := int64(3)
	for i := 0; i < 3; i++ {
		child := workspace.Workspace{
			ID:                fmt.Sprintf("ws-child-%d", i),
			ParentWorkspaceID: "ws-root",
			Status:            workspace.StatusActive,
			ProjectPath:       "/test",
			BranchFromSeq:     &branchSeq,
			SiblingOrdinal:    i,
			CreatedAt:         now.Add(time.Duration(i) * time.Second),
			UpdatedAt:         now.Add(time.Duration(i) * time.Second),
		}
		if err := s.CreateWorkspace(child); err != nil {
			t.Fatal(err)
		}
	}

	children, err := s.ListChildWorkspaces("ws-root")
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 3 {
		t.Fatalf("expected 3 children, got %d", len(children))
	}
	for i, child := range children {
		if child.SiblingOrdinal != i {
			t.Errorf("child %d: expected ordinal %d, got %d", i, i, child.SiblingOrdinal)
		}
	}
}

func TestNextSiblingOrdinal(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	parent := workspace.Workspace{
		ID: "ws-root", Status: workspace.StatusActive, ProjectPath: "/test",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CreateWorkspace(parent); err != nil {
		t.Fatal(err)
	}

	// No children yet
	ord, err := s.NextSiblingOrdinal("ws-root", 5)
	if err != nil {
		t.Fatal(err)
	}
	if ord != 0 {
		t.Errorf("expected 0, got %d", ord)
	}

	// Add two children at seq 5
	branchSeq := int64(5)
	for i := 0; i < 2; i++ {
		child := workspace.Workspace{
			ID:                fmt.Sprintf("ws-child-%d", i),
			ParentWorkspaceID: "ws-root",
			Status:            workspace.StatusActive,
			ProjectPath:       "/test",
			BranchFromSeq:     &branchSeq,
			SiblingOrdinal:    i,
			CreatedAt:         now,
			UpdatedAt:         now,
		}
		if err := s.CreateWorkspace(child); err != nil {
			t.Fatal(err)
		}
	}

	ord, err = s.NextSiblingOrdinal("ws-root", 5)
	if err != nil {
		t.Fatal(err)
	}
	if ord != 2 {
		t.Errorf("expected 2, got %d", ord)
	}

	// Different branch seq should be independent
	ord, err = s.NextSiblingOrdinal("ws-root", 10)
	if err != nil {
		t.Fatal(err)
	}
	if ord != 0 {
		t.Errorf("expected 0 for different seq, got %d", ord)
	}
}

func TestWorkspaceSessionLink(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	// Need a session and workspace first
	if err := s.CreateSession(testSession("s1", now)); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(testSession("s2", now)); err != nil {
		t.Fatal(err)
	}
	w := workspace.Workspace{
		ID: "ws-1", Status: workspace.StatusActive, ProjectPath: "/test",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CreateWorkspace(w); err != nil {
		t.Fatal(err)
	}

	// Link two sessions
	for _, sid := range []string{"s1", "s2"} {
		ws := workspace.WorkspaceSession{
			WorkspaceID: "ws-1",
			SessionID:   sid,
			CreatedAt:   now,
		}
		if err := s.LinkWorkspaceSession(ws); err != nil {
			t.Fatalf("link %s: %v", sid, err)
		}
	}

	// Idempotent
	if err := s.LinkWorkspaceSession(workspace.WorkspaceSession{
		WorkspaceID: "ws-1", SessionID: "s1", CreatedAt: now,
	}); err != nil {
		t.Fatalf("duplicate link should not error: %v", err)
	}

	links, err := s.ListWorkspaceSessions("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 2 {
		t.Fatalf("expected 2 links, got %d", len(links))
	}
}

func TestCreateBranchedWorkspaceRollsBackOnLateFailure(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	if err := s.CreateWorkspace(workspace.Workspace{
		ID:          "ws-root",
		Status:      workspace.StatusActive,
		ProjectPath: "/repo",
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(testSession("s-existing", now)); err != nil {
		t.Fatal(err)
	}

	branchSeq := int64(7)
	err := s.CreateBranchedWorkspace(BranchedWorkspaceCreate{
		SourceWorkspaceID: "ws-root",
		ChildWorkspace: workspace.Workspace{
			ID:                "ws-child",
			ParentWorkspaceID: "ws-root",
			Status:            workspace.StatusActive,
			ProjectPath:       "/repo",
			WorktreePath:      "/tmp/ws-child",
			GitRef:            "refs/peek/ws-child",
			BranchFromSeq:     &branchSeq,
			SiblingOrdinal:    0,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
		ChildSession: event.Session{
			ID:          "s-existing",
			Source:      "claude",
			ProjectPath: "/tmp/ws-child",
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		ChildLink: workspace.WorkspaceSession{
			WorkspaceID: "ws-child",
			SessionID:   "s-existing",
			CreatedAt:   now,
		},
		ChildBranchPath: workspace.BranchPathSegment{
			WorkspaceID:       "ws-child",
			ParentWorkspaceID: "ws-root",
			BranchSeq:         branchSeq,
			Ordinal:           0,
			Depth:             1,
		},
	})
	if err == nil {
		t.Fatal("expected error")
	}

	root, err := s.GetWorkspace("ws-root")
	if err != nil {
		t.Fatal(err)
	}
	if root.Status != workspace.StatusActive {
		t.Fatalf("expected source to stay active after rollback, got %s", root.Status)
	}

	if _, err := s.GetWorkspace("ws-child"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected child workspace rollback, got %v", err)
	}

	links, err := s.ListWorkspaceSessions("ws-child")
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 0 {
		t.Fatalf("expected no child workspace links after rollback, got %d", len(links))
	}

	path, err := s.GetBranchPath("ws-child")
	if err != nil {
		t.Fatal(err)
	}
	if len(path) != 0 {
		t.Fatalf("expected no child branch path after rollback, got %d segments", len(path))
	}
}

func TestCheckpointCRUD(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	if err := s.CreateSession(testSession("s1", now)); err != nil {
		t.Fatal(err)
	}
	w := workspace.Workspace{
		ID: "ws-1", Status: workspace.StatusActive, ProjectPath: "/test",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CreateWorkspace(w); err != nil {
		t.Fatal(err)
	}

	// Create checkpoints
	for seq := int64(0); seq < 5; seq++ {
		for _, kind := range []workspace.SnapshotKind{workspace.SnapshotPreTool, workspace.SnapshotPostTool} {
			cp := workspace.CheckpointRef{
				ID:          CheckpointID("ws-1", seq, kind),
				WorkspaceID: "ws-1",
				SessionID:   "s1",
				Seq:         seq,
				Kind:        kind,
				GitRef:      fmt.Sprintf("refs/peek/ws-1/%d/%s", seq, kind),
				CreatedAt:   now.Add(time.Duration(seq) * time.Second),
			}
			if err := s.CreateCheckpoint(cp); err != nil {
				t.Fatalf("create checkpoint seq=%d kind=%s: %v", seq, kind, err)
			}
		}
	}

	// Idempotent
	cp := workspace.CheckpointRef{
		ID:          CheckpointID("ws-1", 0, workspace.SnapshotPreTool),
		WorkspaceID: "ws-1",
		SessionID:   "s1",
		Seq:         0,
		Kind:        workspace.SnapshotPreTool,
		GitRef:      "refs/peek/ws-1/0/pre_tool",
		CreatedAt:   now,
	}
	if err := s.CreateCheckpoint(cp); err != nil {
		t.Fatalf("duplicate checkpoint should not error: %v", err)
	}

	// List
	checkpoints, err := s.ListCheckpoints("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpoints) != 10 {
		t.Fatalf("expected 10 checkpoints, got %d", len(checkpoints))
	}

	// Get
	got, err := s.GetCheckpoint(CheckpointID("ws-1", 2, workspace.SnapshotPreTool))
	if err != nil {
		t.Fatal(err)
	}
	if got.Seq != 2 || got.Kind != workspace.SnapshotPreTool {
		t.Errorf("unexpected checkpoint: %+v", got)
	}
}

func TestResolveCheckpoint(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	if err := s.CreateSession(testSession("s1", now)); err != nil {
		t.Fatal(err)
	}
	w := workspace.Workspace{
		ID: "ws-1", Status: workspace.StatusActive, ProjectPath: "/test",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CreateWorkspace(w); err != nil {
		t.Fatal(err)
	}

	// Create checkpoints at seq 2, 5, 8
	for _, seq := range []int64{2, 5, 8} {
		cp := workspace.CheckpointRef{
			ID:          CheckpointID("ws-1", seq, workspace.SnapshotPreTool),
			WorkspaceID: "ws-1",
			SessionID:   "s1",
			Seq:         seq,
			Kind:        workspace.SnapshotPreTool,
			GitRef:      fmt.Sprintf("refs/peek/ws-1/%d/pre_tool", seq),
			CreatedAt:   now,
		}
		if err := s.CreateCheckpoint(cp); err != nil {
			t.Fatal(err)
		}
	}

	// Resolve at seq 6 should return checkpoint at seq 5
	got, err := s.ResolveCheckpoint("ws-1", 6, workspace.SnapshotPreTool)
	if err != nil {
		t.Fatal(err)
	}
	if got.Seq != 5 {
		t.Errorf("expected seq 5, got %d", got.Seq)
	}

	// Resolve at seq 1 should fail (no checkpoint at or before 1)
	_, err = s.ResolveCheckpoint("ws-1", 1, workspace.SnapshotPreTool)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected sql.ErrNoRows, got %v", err)
	}

	// Resolve at exact seq should return that checkpoint
	got, err = s.ResolveCheckpoint("ws-1", 8, workspace.SnapshotPreTool)
	if err != nil {
		t.Fatal(err)
	}
	if got.Seq != 8 {
		t.Errorf("expected seq 8, got %d", got.Seq)
	}
}

func TestBranchPathBreadcrumb(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	// Create a lineage: root -> child1 -> grandchild
	for _, w := range []workspace.Workspace{
		{ID: "ws-root", Status: workspace.StatusActive, ProjectPath: "/test", CreatedAt: now, UpdatedAt: now},
		{ID: "ws-child1", ParentWorkspaceID: "ws-root", Status: workspace.StatusActive, ProjectPath: "/test", CreatedAt: now, UpdatedAt: now},
		{ID: "ws-grandchild", ParentWorkspaceID: "ws-child1", Status: workspace.StatusActive, ProjectPath: "/test", CreatedAt: now, UpdatedAt: now},
	} {
		if err := s.CreateWorkspace(w); err != nil {
			t.Fatal(err)
		}
	}

	// Save branch path segments
	segments := []workspace.BranchPathSegment{
		{WorkspaceID: "ws-root", Depth: 0, Ordinal: 0},
		{WorkspaceID: "ws-child1", ParentWorkspaceID: "ws-root", BranchSeq: 3, Depth: 1, Ordinal: 0},
		{WorkspaceID: "ws-grandchild", ParentWorkspaceID: "ws-child1", BranchSeq: 7, Depth: 2, Ordinal: 0},
	}
	for _, seg := range segments {
		if err := s.SaveBranchPath(seg); err != nil {
			t.Fatal(err)
		}
	}

	// Breadcrumb from grandchild should return root -> child1 -> grandchild
	path, err := s.GetBranchPath("ws-grandchild")
	if err != nil {
		t.Fatal(err)
	}
	if len(path) != 3 {
		t.Fatalf("expected 3 segments, got %d", len(path))
	}
	if path[0].WorkspaceID != "ws-root" {
		t.Errorf("first segment should be root, got %s", path[0].WorkspaceID)
	}
	if path[1].WorkspaceID != "ws-child1" || path[1].BranchSeq != 3 {
		t.Errorf("second segment mismatch: %+v", path[1])
	}
	if path[2].WorkspaceID != "ws-grandchild" || path[2].BranchSeq != 7 {
		t.Errorf("third segment mismatch: %+v", path[2])
	}

	// Breadcrumb from root should return just root
	path, err = s.GetBranchPath("ws-root")
	if err != nil {
		t.Fatal(err)
	}
	if len(path) != 1 {
		t.Fatalf("expected 1 segment for root, got %d", len(path))
	}
}

func TestBranchPathSiblingStability(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	root := workspace.Workspace{
		ID: "ws-root", Status: workspace.StatusActive, ProjectPath: "/test",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CreateWorkspace(root); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveBranchPath(workspace.BranchPathSegment{
		WorkspaceID: "ws-root", Depth: 0, Ordinal: 0,
	}); err != nil {
		t.Fatal(err)
	}

	// Branch twice from same sequence
	branchSeq := int64(5)
	for i := 0; i < 2; i++ {
		id := fmt.Sprintf("ws-sibling-%d", i)
		w := workspace.Workspace{
			ID:                id,
			ParentWorkspaceID: "ws-root",
			Status:            workspace.StatusActive,
			ProjectPath:       "/test",
			BranchFromSeq:     &branchSeq,
			SiblingOrdinal:    i,
			CreatedAt:         now.Add(time.Duration(i) * time.Second),
			UpdatedAt:         now.Add(time.Duration(i) * time.Second),
		}
		if err := s.CreateWorkspace(w); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveBranchPath(workspace.BranchPathSegment{
			WorkspaceID:       id,
			ParentWorkspaceID: "ws-root",
			BranchSeq:         5,
			Ordinal:           i,
			Depth:             1,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Verify sibling ordinals remain stable
	children, err := s.ListChildWorkspaces("ws-root")
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 2 {
		t.Fatalf("expected 2 siblings, got %d", len(children))
	}
	if children[0].SiblingOrdinal != 0 || children[1].SiblingOrdinal != 1 {
		t.Errorf("sibling ordinals not stable: %d, %d", children[0].SiblingOrdinal, children[1].SiblingOrdinal)
	}
}

func TestDeleteWorkspaceLeaf(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	if err := s.CreateSession(testSession("s1", now)); err != nil {
		t.Fatal(err)
	}

	parent := workspace.Workspace{
		ID: "ws-parent", Status: workspace.StatusActive, ProjectPath: "/test",
		CreatedAt: now, UpdatedAt: now,
	}
	child := workspace.Workspace{
		ID: "ws-child", ParentWorkspaceID: "ws-parent", Status: workspace.StatusActive,
		ProjectPath: "/test", CreatedAt: now, UpdatedAt: now,
	}
	for _, w := range []workspace.Workspace{parent, child} {
		if err := s.CreateWorkspace(w); err != nil {
			t.Fatal(err)
		}
	}

	// Add session link and checkpoint to child
	if err := s.LinkWorkspaceSession(workspace.WorkspaceSession{
		WorkspaceID: "ws-child", SessionID: "s1", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateCheckpoint(workspace.CheckpointRef{
		ID: "cp-1", WorkspaceID: "ws-child", SessionID: "s1",
		Seq: 0, Kind: workspace.SnapshotPreTool, GitRef: "ref1", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveBranchPath(workspace.BranchPathSegment{
		WorkspaceID: "ws-parent", Depth: 0,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveBranchPath(workspace.BranchPathSegment{
		WorkspaceID:       "ws-child",
		ParentWorkspaceID: "ws-parent",
		Depth:             1,
	}); err != nil {
		t.Fatal(err)
	}

	deleted, err := s.DeleteWorkspace("ws-child")
	if err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("expected workspace to be deleted")
	}

	// Workspace gone
	if _, err := s.GetWorkspace("ws-child"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected workspace to be gone, got %v", err)
	}

	// Checkpoint gone
	if _, err := s.GetCheckpoint("cp-1"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected checkpoint to be gone, got %v", err)
	}

	// Parent remains intact
	gotParent, err := s.GetWorkspace("ws-parent")
	if err != nil {
		t.Fatal(err)
	}
	if gotParent.ID != "ws-parent" {
		t.Errorf("expected parent workspace to remain, got %q", gotParent.ID)
	}
}

func TestDeleteWorkspaceRejectsParentWithChildren(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	parent := workspace.Workspace{
		ID: "ws-parent", Status: workspace.StatusFrozen, ProjectPath: "/test",
		CreatedAt: now, UpdatedAt: now,
	}
	child := workspace.Workspace{
		ID: "ws-child", ParentWorkspaceID: "ws-parent", Status: workspace.StatusFrozen,
		ProjectPath: "/test", CreatedAt: now, UpdatedAt: now,
	}
	for _, w := range []workspace.Workspace{parent, child} {
		if err := s.CreateWorkspace(w); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SaveBranchPath(workspace.BranchPathSegment{
		WorkspaceID: "ws-parent", Depth: 0,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveBranchPath(workspace.BranchPathSegment{
		WorkspaceID:       "ws-child",
		ParentWorkspaceID: "ws-parent",
		Depth:             1,
	}); err != nil {
		t.Fatal(err)
	}

	deleted, err := s.DeleteWorkspace("ws-parent")
	if err == nil {
		t.Fatal("expected error deleting parent workspace")
	}
	if deleted {
		t.Fatal("expected delete result to be false")
	}

	if _, err := s.GetWorkspace("ws-parent"); err != nil {
		t.Fatalf("expected parent workspace to remain: %v", err)
	}
	if _, err := s.GetWorkspace("ws-child"); err != nil {
		t.Fatalf("expected child workspace to remain: %v", err)
	}
}

func TestDeleteWorkspaceRehomesStoppedManagedRuntimeReference(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	if err := s.CreateSession(testSession("s-root", now)); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(testSession("s-child", now)); err != nil {
		t.Fatal(err)
	}

	root := workspace.Workspace{
		ID: "ws-root", Status: workspace.StatusFrozen, ProjectPath: "/test",
		CreatedAt: now, UpdatedAt: now,
	}
	branchSeq := int64(2)
	child := workspace.Workspace{
		ID: "ws-child", ParentWorkspaceID: "ws-root", Status: workspace.StatusFrozen,
		ProjectPath: "/test", BranchFromSeq: &branchSeq, CreatedAt: now, UpdatedAt: now,
	}
	for _, w := range []workspace.Workspace{root, child} {
		if err := s.CreateWorkspace(w); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.LinkWorkspaceSession(workspace.WorkspaceSession{
		WorkspaceID: "ws-root", SessionID: "s-root", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.LinkWorkspaceSession(workspace.WorkspaceSession{
		WorkspaceID: "ws-child", SessionID: "s-child", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.UpsertManagedRuntime(ManagedRuntime{
		ID:                "rt-1",
		RootWorkspaceID:   "ws-root",
		ActiveWorkspaceID: "ws-child",
		ActiveSessionID:   "s-child",
		Source:            "claude",
		Status:            ManagedRuntimeStopped,
		HeartbeatAt:       now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatal(err)
	}

	deleted, err := s.DeleteWorkspace("ws-child")
	if err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("expected workspace to be deleted")
	}

	if _, err := s.GetWorkspace("ws-child"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected workspace to be gone, got %v", err)
	}

	rt, err := s.GetManagedRuntime("rt-1")
	if err != nil {
		t.Fatal(err)
	}
	if rt.Status != ManagedRuntimeStopped {
		t.Fatalf("expected stopped runtime, got %s", rt.Status)
	}
	if rt.ActiveWorkspaceID != "ws-root" {
		t.Fatalf("expected runtime rehomed to root workspace, got %s", rt.ActiveWorkspaceID)
	}
	if rt.ActiveSessionID != "s-root" {
		t.Fatalf("expected runtime rehomed to root session, got %s", rt.ActiveSessionID)
	}
}

func TestPruneWorkspaceLineageRemovesStoppedRootAndMetadata(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	for _, sess := range []event.Session{
		testSession("s-root", now),
		testSession("s-child", now),
	} {
		if err := s.CreateSession(sess); err != nil {
			t.Fatal(err)
		}
	}

	branchSeq := int64(2)
	for _, ws := range []workspace.Workspace{
		{
			ID:           "ws-root",
			Status:       workspace.StatusFrozen,
			ProjectPath:  "/test",
			WorktreePath: "/test",
			IsRoot:       true,
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			ID:                "ws-child",
			ParentWorkspaceID: "ws-root",
			Status:            workspace.StatusFrozen,
			ProjectPath:       "/test",
			WorktreePath:      "/tmp/ws-child",
			BranchFromSeq:     &branchSeq,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
	} {
		if err := s.CreateWorkspace(ws); err != nil {
			t.Fatal(err)
		}
	}

	for _, link := range []workspace.WorkspaceSession{
		{WorkspaceID: "ws-root", SessionID: "s-root", CreatedAt: now},
		{WorkspaceID: "ws-child", SessionID: "s-child", CreatedAt: now},
	} {
		if err := s.LinkWorkspaceSession(link); err != nil {
			t.Fatal(err)
		}
	}

	for _, seg := range []workspace.BranchPathSegment{
		{WorkspaceID: "ws-root", Depth: 0, Ordinal: 0},
		{WorkspaceID: "ws-child", ParentWorkspaceID: "ws-root", BranchSeq: branchSeq, Depth: 1, Ordinal: 0},
	} {
		if err := s.SaveBranchPath(seg); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.CreateCheckpoint(workspace.CheckpointRef{
		ID:          "cp-1",
		WorkspaceID: "ws-child",
		SessionID:   "s-child",
		Seq:         2,
		Kind:        workspace.SnapshotPreTool,
		GitRef:      "refs/peek/ws-child/2/pre_tool",
		CreatedAt:   now,
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.UpsertManagedRuntime(ManagedRuntime{
		ID:                "rt-1",
		ProjectPath:       "/test",
		RootWorkspaceID:   "ws-root",
		ActiveWorkspaceID: "ws-root",
		ActiveSessionID:   "s-root",
		Source:            "claude",
		Status:            ManagedRuntimeStopped,
		HeartbeatAt:       now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertCheckoutLease(CheckoutLease{
		CheckoutPath: "/test",
		RuntimeID:    "rt-1",
		WorkspaceID:  "ws-root",
		ClaimedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateManagedRuntimeRequest(ManagedRuntimeRequest{
		ID:        "req-1",
		RuntimeID: "rt-1",
		Kind:      "branch",
		Status:    ManagedRuntimeRequestPending,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertDetachedCompanionRuntime(DetachedCompanionRuntime{
		RuntimeID:         "rt-1",
		ActiveWorkspaceID: "ws-root",
		OwnerSessionID:    "s-root",
		ConfigSource:      "peek.runtime.json",
		Phase:             "idle",
		UpdatedAt:         now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceCompanionServiceStates("rt-1", []CompanionServiceState{{
		RuntimeID:   "rt-1",
		WorkspaceID: "ws-root",
		ServiceName: "web",
		Status:      CompanionServiceStopped,
		UpdatedAt:   now,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertPortLease(PortLease{
		RuntimeID:   "rt-1",
		ServiceName: "web",
		Host:        "127.0.0.1",
		Port:        4318,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []WorkspaceBootstrapState{
		{WorkspaceID: "ws-root", Status: BootstrapSucceeded, UpdatedAt: now},
		{WorkspaceID: "ws-child", Status: BootstrapSucceeded, UpdatedAt: now},
	} {
		if err := s.UpsertWorkspaceBootstrapState(state); err != nil {
			t.Fatal(err)
		}
	}

	deleted, workspaceIDs, runtimeIDs, err := s.PruneWorkspaceLineage("ws-root")
	if err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("expected workspace lineage to be deleted")
	}
	if len(workspaceIDs) != 2 {
		t.Fatalf("expected 2 deleted workspaces, got %d", len(workspaceIDs))
	}
	if len(runtimeIDs) != 1 || runtimeIDs[0] != "rt-1" {
		t.Fatalf("unexpected runtime ids: %v", runtimeIDs)
	}

	for _, workspaceID := range []string{"ws-root", "ws-child"} {
		if _, err := s.GetWorkspace(workspaceID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected workspace %s to be deleted, got %v", workspaceID, err)
		}
	}
	if _, err := s.GetManagedRuntime("rt-1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected runtime to be deleted, got %v", err)
	}
	if _, err := s.GetCheckoutLease("/test"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected checkout lease to be deleted, got %v", err)
	}
	if _, err := s.GetCheckpoint("cp-1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected checkpoint to be deleted, got %v", err)
	}
	if _, err := s.GetWorkspaceBootstrapState("ws-root"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected root bootstrap state to be deleted, got %v", err)
	}
	if _, err := s.GetWorkspaceBootstrapState("ws-child"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected child bootstrap state to be deleted, got %v", err)
	}
	if _, err := s.GetSession("s-root"); err != nil {
		t.Fatalf("expected root session to remain, got %v", err)
	}
}

func TestPruneWorkspaceLineageRejectsLiveRuntime(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	if err := s.CreateSession(testSession("s-root", now)); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateWorkspace(workspace.Workspace{
		ID:           "ws-root",
		Status:       workspace.StatusFrozen,
		ProjectPath:  "/test",
		WorktreePath: "/test",
		IsRoot:       true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.LinkWorkspaceSession(workspace.WorkspaceSession{
		WorkspaceID: "ws-root",
		SessionID:   "s-root",
		CreatedAt:   now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveBranchPath(workspace.BranchPathSegment{WorkspaceID: "ws-root", Depth: 0, Ordinal: 0}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertManagedRuntime(ManagedRuntime{
		ID:                "rt-1",
		ProjectPath:       "/test",
		RootWorkspaceID:   "ws-root",
		ActiveWorkspaceID: "ws-root",
		ActiveSessionID:   "s-root",
		Source:            "claude",
		Status:            ManagedRuntimeRunning,
		HeartbeatAt:       now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatal(err)
	}

	deleted, _, _, err := s.PruneWorkspaceLineage("ws-root")
	if err == nil {
		t.Fatal("expected prune to fail for live runtime")
	}
	if deleted {
		t.Fatal("expected prune result to be false")
	}
	if _, err := s.GetWorkspace("ws-root"); err != nil {
		t.Fatalf("expected workspace to remain, got %v", err)
	}
}

func TestMigrationRepairsLegacyManagedRuntimeProjectPath(t *testing.T) {
	dbPath := t.TempDir() + "/legacy.db"

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if _, err := db.Exec(`
CREATE TABLE sessions (
	id TEXT PRIMARY KEY,
	source TEXT NOT NULL,
	project_path TEXT NOT NULL DEFAULT '',
	source_session_id TEXT NOT NULL DEFAULT '',
	parent_session_id TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE workspaces (
	id TEXT PRIMARY KEY,
	parent_workspace_id TEXT REFERENCES workspaces(id),
	status TEXT NOT NULL DEFAULT 'active',
	project_path TEXT NOT NULL DEFAULT '',
	worktree_path TEXT NOT NULL DEFAULT '',
	git_ref TEXT NOT NULL DEFAULT '',
	branch_from_seq INTEGER,
	sibling_ordinal INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE managed_runtimes (
	id TEXT PRIMARY KEY,
	root_workspace_id TEXT NOT NULL REFERENCES workspaces(id),
	active_workspace_id TEXT NOT NULL REFERENCES workspaces(id),
	active_session_id TEXT NOT NULL REFERENCES sessions(id),
	source TEXT NOT NULL DEFAULT '',
	launch_args_json TEXT NOT NULL DEFAULT '[]',
	status TEXT NOT NULL DEFAULT 'running',
	heartbeat_at TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

INSERT INTO sessions (id, source, project_path, source_session_id, parent_session_id, created_at, updated_at)
VALUES ('s-1', 'codex', '/test', '', NULL, '2026-03-15T00:00:00Z', '2026-03-15T00:00:00Z');

INSERT INTO workspaces (id, parent_workspace_id, status, project_path, worktree_path, git_ref, branch_from_seq, sibling_ordinal, created_at, updated_at)
VALUES ('ws-1', NULL, 'active', '/test', '/test', '', NULL, 0, '2026-03-15T00:00:00Z', '2026-03-15T00:00:00Z');

INSERT INTO managed_runtimes (id, root_workspace_id, active_workspace_id, active_session_id, source, launch_args_json, status, heartbeat_at, created_at, updated_at)
VALUES ('rt-1', 'ws-1', 'ws-1', 's-1', 'codex', '[]', 'running', '2026-03-15T00:00:00Z', '2026-03-15T00:00:00Z', '2026-03-15T00:00:00Z');
`); err != nil {
		db.Close()
		t.Fatalf("seed legacy schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Millisecond)
	rt, err := s.GetManagedRuntime("rt-1")
	if err != nil {
		t.Fatalf("get managed runtime after migration: %v", err)
	}
	if rt.ProjectPath != "/test" {
		t.Fatalf("expected migrated runtime project path, got %q", rt.ProjectPath)
	}

	if err := s.UpsertManagedRuntime(ManagedRuntime{
		ID:                "rt-1",
		ProjectPath:       "/test",
		RootWorkspaceID:   "ws-1",
		ActiveWorkspaceID: "ws-1",
		ActiveSessionID:   "s-1",
		Source:            "codex",
		Status:            ManagedRuntimeRunning,
		HeartbeatAt:       now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatalf("upsert managed runtime after migration: %v", err)
	}

	var indexName string
	if err := s.db.QueryRow(`
SELECT name
  FROM sqlite_master
 WHERE type = 'index' AND name = 'idx_managed_runtimes_project'
`).Scan(&indexName); err != nil {
		t.Fatalf("lookup managed runtime project index: %v", err)
	}
}

func TestMigrationIdempotency(t *testing.T) {
	dbPath := t.TempDir() + "/test.db"

	// First open
	s1, err := Open(dbPath)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := s1.CreateWorkspace(workspace.Workspace{
		ID: "ws-1", Status: workspace.StatusActive, ProjectPath: "/test",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	s1.Close()

	// Second open (re-runs migration)
	s2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer s2.Close()

	got, err := s2.GetWorkspace("ws-1")
	if err != nil {
		t.Fatalf("get after reopen: %v", err)
	}
	if got.ID != "ws-1" || got.Status != workspace.StatusActive {
		t.Errorf("workspace lost after reopen: %+v", got)
	}
}

func TestWorkspaceSummaryCounters(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	if err := s.CreateSession(testSession("s1", now)); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(testSession("s2", now)); err != nil {
		t.Fatal(err)
	}

	w := workspace.Workspace{
		ID: "ws-1", Status: workspace.StatusActive, ProjectPath: "/test",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CreateWorkspace(w); err != nil {
		t.Fatal(err)
	}

	// Link 2 sessions
	for _, sid := range []string{"s1", "s2"} {
		if err := s.LinkWorkspaceSession(workspace.WorkspaceSession{
			WorkspaceID: "ws-1", SessionID: sid, CreatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Create 3 checkpoints
	for seq := int64(0); seq < 3; seq++ {
		if err := s.CreateCheckpoint(workspace.CheckpointRef{
			ID:          CheckpointID("ws-1", seq, workspace.SnapshotPreTool),
			WorkspaceID: "ws-1", SessionID: "s1", Seq: seq,
			Kind: workspace.SnapshotPreTool, GitRef: "ref", CreatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}

	summary, err := s.GetWorkspaceSummary("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if summary.SessionCount != 2 {
		t.Errorf("expected 2 sessions, got %d", summary.SessionCount)
	}
	if summary.CheckpointCount != 3 {
		t.Errorf("expected 3 checkpoints, got %d", summary.CheckpointCount)
	}
}

// testSession is a helper for creating test sessions.
func testSession(id string, now time.Time) event.Session {
	return event.Session{
		ID:        id,
		Source:    "claude",
		CreatedAt: now,
		UpdatedAt: now,
	}
}
