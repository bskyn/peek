package cli

import (
	"os"
	"path/filepath"
	"strings"
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

func TestWorkspaceListShowsRuntimeProviderAndState(t *testing.T) {
	repoDir := t.TempDir()
	dbFile := filepath.Join(t.TempDir(), "peek.db")
	now := time.Now().UTC().Truncate(time.Millisecond)

	st, err := store.Open(dbFile)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	for _, sess := range []event.Session{
		{ID: "s-live", Source: "codex", ProjectPath: repoDir, CreatedAt: now, UpdatedAt: now},
		{ID: "s-stopped", Source: "claude", ProjectPath: repoDir, CreatedAt: now, UpdatedAt: now},
		{ID: "s-stale", Source: "claude", ProjectPath: repoDir, CreatedAt: now, UpdatedAt: now},
	} {
		if err := st.CreateSession(sess); err != nil {
			t.Fatal(err)
		}
	}

	branchSeq := int64(37)
	for _, ws := range []workspace.Workspace{
		{
			ID:           "ws-live",
			Status:       workspace.StatusActive,
			ProjectPath:  repoDir,
			WorktreePath: repoDir,
			IsRoot:       true,
			CreatedAt:    now.Add(1 * time.Second),
			UpdatedAt:    now.Add(1 * time.Second),
		},
		{
			ID:                "ws-child",
			ParentWorkspaceID: "ws-live",
			Status:            workspace.StatusFrozen,
			ProjectPath:       repoDir,
			WorktreePath:      filepath.Join(repoDir, ".peek-child"),
			GitRef:            "refs/peek/ws-live/37/pre_tool",
			BranchFromSeq:     &branchSeq,
			CreatedAt:         now.Add(2 * time.Second),
			UpdatedAt:         now.Add(2 * time.Second),
		},
		{
			ID:           "ws-stopped",
			Status:       workspace.StatusFrozen,
			ProjectPath:  repoDir,
			WorktreePath: filepath.Join(repoDir, ".peek-stopped"),
			IsRoot:       true,
			CreatedAt:    now.Add(3 * time.Second),
			UpdatedAt:    now.Add(3 * time.Second),
		},
		{
			ID:           "ws-stale",
			Status:       workspace.StatusFrozen,
			ProjectPath:  repoDir,
			WorktreePath: filepath.Join(repoDir, ".peek-stale"),
			IsRoot:       true,
			CreatedAt:    now.Add(4 * time.Second),
			UpdatedAt:    now.Add(4 * time.Second),
		},
		{
			ID:           "ws-no-runtime",
			Status:       workspace.StatusFrozen,
			ProjectPath:  repoDir,
			WorktreePath: filepath.Join(repoDir, ".peek-none"),
			IsRoot:       true,
			CreatedAt:    now.Add(5 * time.Second),
			UpdatedAt:    now.Add(5 * time.Second),
		},
	} {
		if err := st.CreateWorkspace(ws); err != nil {
			t.Fatal(err)
		}
	}

	for _, seg := range []workspace.BranchPathSegment{
		{WorkspaceID: "ws-live", Depth: 0, Ordinal: 0},
		{WorkspaceID: "ws-child", ParentWorkspaceID: "ws-live", BranchSeq: branchSeq, Depth: 1, Ordinal: 0},
		{WorkspaceID: "ws-stopped", Depth: 0, Ordinal: 0},
		{WorkspaceID: "ws-stale", Depth: 0, Ordinal: 0},
		{WorkspaceID: "ws-no-runtime", Depth: 0, Ordinal: 0},
	} {
		if err := st.SaveBranchPath(seg); err != nil {
			t.Fatal(err)
		}
	}

	for _, rt := range []store.ManagedRuntime{
		{
			ID:                "rt-live",
			ProjectPath:       repoDir,
			RootWorkspaceID:   "ws-live",
			ActiveWorkspaceID: "ws-live",
			ActiveSessionID:   "s-live",
			Source:            "codex",
			Status:            store.ManagedRuntimeRunning,
			HeartbeatAt:       now,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
		{
			ID:                "rt-stopped",
			ProjectPath:       repoDir,
			RootWorkspaceID:   "ws-stopped",
			ActiveWorkspaceID: "ws-stopped",
			ActiveSessionID:   "s-stopped",
			Source:            "claude",
			Status:            store.ManagedRuntimeStopped,
			HeartbeatAt:       now,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
		{
			ID:                "rt-stale",
			ProjectPath:       repoDir,
			RootWorkspaceID:   "ws-stale",
			ActiveWorkspaceID: "ws-stale",
			ActiveSessionID:   "s-stale",
			Source:            "claude",
			Status:            store.ManagedRuntimeRunning,
			HeartbeatAt:       now.Add(-managedRuntimeStaleAfter - time.Second),
			CreatedAt:         now,
			UpdatedAt:         now,
		},
	} {
		if err := st.UpsertManagedRuntime(rt); err != nil {
			t.Fatal(err)
		}
	}

	output, err := runRootCommandForTest(t, repoDir, "--db-path", dbFile, "ws", "list")
	if err != nil {
		t.Fatalf("run ws list: %v", err)
	}

	for _, want := range []string{
		"ws-live  [active]  [codex live]",
		"ws-child  [frozen]  [codex live]  " + repoDir + " (from ws-live @seq 37)",
		"ws-stopped  [frozen]  [claude stopped rt-stopped]",
		"ws-stale  [frozen]  [claude stale rt-stale]",
		"ws-no-runtime  [frozen]  [no-runtime]",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, output)
		}
	}
}
