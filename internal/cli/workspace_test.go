package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bskyn/peek/internal/event"
	"github.com/bskyn/peek/internal/store"
	"github.com/bskyn/peek/internal/workspace"
)

func TestRunWorkspacePruneSkipsLeaseOwnerAndPrunesStoppedRoots(t *testing.T) {
	repoDir := testManagedRepo(t)
	now := time.Now().UTC()

	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})
	repoDir, err = os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	prevDBPath := dbPath
	dbPath = filepath.Join(t.TempDir(), "peek.db")
	t.Cleanup(func() { dbPath = prevDBPath })

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	for _, sess := range []event.Session{
		{ID: "s-old", Source: "claude", ProjectPath: repoDir, CreatedAt: now, UpdatedAt: now},
		{ID: "s-current", Source: "claude", ProjectPath: repoDir, CreatedAt: now, UpdatedAt: now},
	} {
		if err := st.CreateSession(sess); err != nil {
			t.Fatal(err)
		}
	}

	for _, ws := range []workspace.Workspace{
		{
			ID:           "ws-old",
			Status:       workspace.StatusFrozen,
			ProjectPath:  repoDir,
			WorktreePath: repoDir,
			IsRoot:       true,
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			ID:           "ws-current",
			Status:       workspace.StatusFrozen,
			ProjectPath:  repoDir,
			WorktreePath: repoDir,
			IsRoot:       true,
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	} {
		if err := st.CreateWorkspace(ws); err != nil {
			t.Fatal(err)
		}
		if err := st.SaveBranchPath(workspace.BranchPathSegment{WorkspaceID: ws.ID, Depth: 0, Ordinal: 0}); err != nil {
			t.Fatal(err)
		}
	}

	for _, link := range []workspace.WorkspaceSession{
		{WorkspaceID: "ws-old", SessionID: "s-old", CreatedAt: now},
		{WorkspaceID: "ws-current", SessionID: "s-current", CreatedAt: now},
	} {
		if err := st.LinkWorkspaceSession(link); err != nil {
			t.Fatal(err)
		}
	}

	for _, rt := range []store.ManagedRuntime{
		{
			ID:                "rt-old",
			ProjectPath:       repoDir,
			RootWorkspaceID:   "ws-old",
			ActiveWorkspaceID: "ws-old",
			ActiveSessionID:   "s-old",
			Source:            "claude",
			Status:            store.ManagedRuntimeStopped,
			HeartbeatAt:       now,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
		{
			ID:                "rt-current",
			ProjectPath:       repoDir,
			RootWorkspaceID:   "ws-current",
			ActiveWorkspaceID: "ws-current",
			ActiveSessionID:   "s-current",
			Source:            "claude",
			Status:            store.ManagedRuntimeStopped,
			HeartbeatAt:       now,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
	} {
		if err := st.UpsertManagedRuntime(rt); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpsertCheckoutLease(store.CheckoutLease{
		CheckoutPath: repoDir,
		RuntimeID:    "rt-current",
		WorkspaceID:  "ws-current",
		ClaimedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := runWorkspacePrune()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.PrunedRoots) != 1 || result.PrunedRoots[0] != "ws-old" {
		t.Fatalf("unexpected pruned roots: %v", result.PrunedRoots)
	}
	if len(result.SkippedRoots) != 1 || result.SkippedRoots[0] != "ws-current" {
		t.Fatalf("unexpected skipped roots: %v", result.SkippedRoots)
	}

	if _, err := st.GetWorkspace("ws-old"); err == nil {
		t.Fatal("expected stale root to be pruned")
	}
	if _, err := st.GetManagedRuntime("rt-old"); err == nil {
		t.Fatal("expected stale runtime to be pruned")
	}
	if _, err := st.GetWorkspace("ws-current"); err != nil {
		t.Fatalf("expected current root to remain, got %v", err)
	}
}

func TestRunWorkspaceDeletePrunesStoppedRootLineage(t *testing.T) {
	repoDir := testManagedRepo(t)
	now := time.Now().UTC()

	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})
	repoDir, err = os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	prevDBPath := dbPath
	dbPath = filepath.Join(t.TempDir(), "peek.db")
	t.Cleanup(func() { dbPath = prevDBPath })

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	for _, sess := range []event.Session{
		{ID: "s-old", Source: "claude", ProjectPath: repoDir, CreatedAt: now, UpdatedAt: now},
		{ID: "s-current", Source: "claude", ProjectPath: repoDir, CreatedAt: now, UpdatedAt: now},
	} {
		if err := st.CreateSession(sess); err != nil {
			t.Fatal(err)
		}
	}

	for _, ws := range []workspace.Workspace{
		{
			ID:           "ws-old",
			Status:       workspace.StatusFrozen,
			ProjectPath:  repoDir,
			WorktreePath: repoDir,
			IsRoot:       true,
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			ID:           "ws-current",
			Status:       workspace.StatusFrozen,
			ProjectPath:  repoDir,
			WorktreePath: repoDir,
			IsRoot:       true,
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	} {
		if err := st.CreateWorkspace(ws); err != nil {
			t.Fatal(err)
		}
		if err := st.SaveBranchPath(workspace.BranchPathSegment{WorkspaceID: ws.ID, Depth: 0, Ordinal: 0}); err != nil {
			t.Fatal(err)
		}
	}

	for _, link := range []workspace.WorkspaceSession{
		{WorkspaceID: "ws-old", SessionID: "s-old", CreatedAt: now},
		{WorkspaceID: "ws-current", SessionID: "s-current", CreatedAt: now},
	} {
		if err := st.LinkWorkspaceSession(link); err != nil {
			t.Fatal(err)
		}
	}

	for _, rt := range []store.ManagedRuntime{
		{
			ID:                "rt-old",
			ProjectPath:       repoDir,
			RootWorkspaceID:   "ws-old",
			ActiveWorkspaceID: "ws-old",
			ActiveSessionID:   "s-old",
			Source:            "claude",
			Status:            store.ManagedRuntimeStopped,
			HeartbeatAt:       now,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
		{
			ID:                "rt-current",
			ProjectPath:       repoDir,
			RootWorkspaceID:   "ws-current",
			ActiveWorkspaceID: "ws-current",
			ActiveSessionID:   "s-current",
			Source:            "claude",
			Status:            store.ManagedRuntimeStopped,
			HeartbeatAt:       now,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
	} {
		if err := st.UpsertManagedRuntime(rt); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpsertCheckoutLease(store.CheckoutLease{
		CheckoutPath: repoDir,
		RuntimeID:    "rt-current",
		WorkspaceID:  "ws-current",
		ClaimedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}

	if err := runWorkspaceDelete("ws-old"); err != nil {
		t.Fatal(err)
	}

	if _, err := st.GetWorkspace("ws-old"); err == nil {
		t.Fatal("expected root lineage to be deleted")
	}
	if _, err := st.GetManagedRuntime("rt-old"); err == nil {
		t.Fatal("expected root runtime to be deleted")
	}
	if _, err := st.GetWorkspace("ws-current"); err != nil {
		t.Fatalf("expected current root to remain, got %v", err)
	}
}
