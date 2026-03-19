package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/bskyn/peek/internal/event"
	"github.com/bskyn/peek/internal/store"
	"github.com/bskyn/peek/internal/workspace"
)

func TestShellQuote(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "'simple'"},
		{"path with spaces", "'path with spaces'"},
		{"it's", "'it'\\''s'"},
		{"", "''"},
		{"/home/user/project", "'/home/user/project'"},
		{"double\"quotes", "'double\"quotes'"},
	}
	for _, tc := range tests {
		got := shellQuote(tc.input)
		if got != tc.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestRenderTransitionShellBranch(t *testing.T) {
	tr := workspaceTransitionResult{
		RuntimeID:    "rt-abc12345",
		WorkspaceID:  "ws-xyz67890",
		SessionID:    "claude-managed-12345678",
		ProjectPath:  "/home/user/project",
		WorktreePath: "/home/user/.peek/worktrees/rt-abc12345/ws-xyz67890",
		Kind:         "branch",
		SourceFrozen: "ws-root",
	}
	got := renderTransitionShell(tr)

	for _, want := range []string{
		"__PEEK_RUNTIME_ID='rt-abc12345'",
		"__PEEK_WORKSPACE_ID='ws-xyz67890'",
		"__PEEK_SESSION_ID='claude-managed-12345678'",
		"__PEEK_WORKTREE='/home/user/.peek/worktrees/rt-abc12345/ws-xyz67890'",
		"__PEEK_PROJECT='/home/user/project'",
		"__PEEK_KIND='branch'",
		"__PEEK_SOURCE_FROZEN='ws-root'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("renderTransitionShell missing %q in:\n%s", want, got)
		}
	}
}

func TestRenderTransitionShellOmitsSourceFrozenOnSwitch(t *testing.T) {
	tr := workspaceTransitionResult{
		RuntimeID:    "rt-abc",
		WorkspaceID:  "ws-xyz",
		SessionID:    "s-123",
		WorktreePath: "/tmp/worktree",
		Kind:         "switch",
	}
	got := renderTransitionShell(tr)
	if strings.Contains(got, "__PEEK_SOURCE_FROZEN") {
		t.Errorf("expected no __PEEK_SOURCE_FROZEN for switch, got:\n%s", got)
	}
}

func TestRenderTransitionShellQuotesSpaces(t *testing.T) {
	tr := workspaceTransitionResult{
		RuntimeID:    "rt-abc",
		WorkspaceID:  "ws-xyz",
		WorktreePath: "/path/with spaces/worktree",
		Kind:         "branch",
	}
	got := renderTransitionShell(tr)
	if !strings.Contains(got, "__PEEK_WORKTREE='/path/with spaces/worktree'") {
		t.Errorf("expected quoted path with spaces, got:\n%s", got)
	}
}

func TestShellSyncWorktreeFollowsRunningRuntime(t *testing.T) {
	st := openManagedStore(t)
	now := time.Now().UTC()

	if err := st.CreateSession(event.Session{ID: "s-root", Source: "claude", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateWorkspace(workspace.Workspace{
		ID: "ws-root", Status: workspace.StatusActive, ProjectPath: "/repo",
		WorktreePath: "/repo", IsRoot: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertManagedRuntime(store.ManagedRuntime{
		ID: "rt-root", ProjectPath: "/repo", RootWorkspaceID: "ws-root",
		ActiveWorkspaceID: "ws-root", ActiveSessionID: "s-root",
		Source: "claude", Status: store.ManagedRuntimeRunning,
		HeartbeatAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	target := shellSyncWorktree(st, "rt-root", "follow", "/somewhere/else")
	if target != "/repo" {
		t.Fatalf("expected /repo, got %q", target)
	}
}

func TestShellSyncWorktreeIgnoresStaleRuntime(t *testing.T) {
	st := openManagedStore(t)
	now := time.Now().UTC()

	if err := st.CreateSession(event.Session{ID: "s-root", Source: "claude", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateWorkspace(workspace.Workspace{
		ID: "ws-root", Status: workspace.StatusActive, ProjectPath: "/repo",
		WorktreePath: "/repo", IsRoot: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertManagedRuntime(store.ManagedRuntime{
		ID: "rt-stale", ProjectPath: "/repo", RootWorkspaceID: "ws-root",
		ActiveWorkspaceID: "ws-root", ActiveSessionID: "s-root",
		Source: "claude", Status: store.ManagedRuntimeRunning,
		HeartbeatAt: now.Add(-10 * time.Second), CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	target := shellSyncWorktree(st, "rt-stale", "follow", "/somewhere/else")
	if target != "" {
		t.Fatalf("expected empty target for stale runtime, got %q", target)
	}
}

func TestShellSyncWorktreeIgnoresStoppedRuntime(t *testing.T) {
	st := openManagedStore(t)
	now := time.Now().UTC()

	if err := st.CreateSession(event.Session{ID: "s-root", Source: "claude", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateWorkspace(workspace.Workspace{
		ID: "ws-root", Status: workspace.StatusActive, ProjectPath: "/repo",
		WorktreePath: "/repo", IsRoot: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertManagedRuntime(store.ManagedRuntime{
		ID: "rt-stopped", ProjectPath: "/repo", RootWorkspaceID: "ws-root",
		ActiveWorkspaceID: "ws-root", ActiveSessionID: "s-root",
		Source: "claude", Status: store.ManagedRuntimeStopped,
		HeartbeatAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	target := shellSyncWorktree(st, "rt-stopped", "follow", "/somewhere/else")
	if target != "" {
		t.Fatalf("expected empty target for stopped runtime, got %q", target)
	}
}

func TestShellSyncWorktreeIgnoresPinnedShell(t *testing.T) {
	st := openManagedStore(t)
	now := time.Now().UTC()

	if err := st.CreateSession(event.Session{ID: "s-root", Source: "claude", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateWorkspace(workspace.Workspace{
		ID: "ws-root", Status: workspace.StatusActive, ProjectPath: "/repo",
		WorktreePath: "/repo", IsRoot: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertManagedRuntime(store.ManagedRuntime{
		ID: "rt-root", ProjectPath: "/repo", RootWorkspaceID: "ws-root",
		ActiveWorkspaceID: "ws-root", ActiveSessionID: "s-root",
		Source: "claude", Status: store.ManagedRuntimeRunning,
		HeartbeatAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	target := shellSyncWorktree(st, "rt-root", "pinned", "/somewhere/else")
	if target != "" {
		t.Fatalf("expected empty target for pinned shell, got %q", target)
	}
}

func TestShellSyncWorktreeNoopWhenAlreadyInCorrectDir(t *testing.T) {
	st := openManagedStore(t)
	now := time.Now().UTC()

	if err := st.CreateSession(event.Session{ID: "s-root", Source: "claude", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateWorkspace(workspace.Workspace{
		ID: "ws-root", Status: workspace.StatusActive, ProjectPath: "/repo",
		WorktreePath: "/repo", IsRoot: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertManagedRuntime(store.ManagedRuntime{
		ID: "rt-root", ProjectPath: "/repo", RootWorkspaceID: "ws-root",
		ActiveWorkspaceID: "ws-root", ActiveSessionID: "s-root",
		Source: "claude", Status: store.ManagedRuntimeRunning,
		HeartbeatAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	target := shellSyncWorktree(st, "rt-root", "follow", "/repo")
	if target != "" {
		t.Fatalf("expected empty target when already in correct dir, got %q", target)
	}
}

func TestShellSyncWorktreeEmptyForMissingRuntime(t *testing.T) {
	st := openManagedStore(t)

	target := shellSyncWorktree(st, "rt-nonexistent", "follow", "/somewhere")
	if target != "" {
		t.Fatalf("expected empty target for missing runtime, got %q", target)
	}
}

func TestShellSyncWorktreeEmptyForEmptyRuntimeID(t *testing.T) {
	st := openManagedStore(t)

	target := shellSyncWorktree(st, "", "follow", "/somewhere")
	if target != "" {
		t.Fatalf("expected empty target for empty runtime ID, got %q", target)
	}
}

func TestShellSyncWorktreeIsolatesRuntimes(t *testing.T) {
	st := openManagedStore(t)
	now := time.Now().UTC()

	// Runtime A
	if err := st.CreateSession(event.Session{ID: "s-a", Source: "claude", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateWorkspace(workspace.Workspace{
		ID: "ws-a", Status: workspace.StatusActive, ProjectPath: "/repo",
		WorktreePath: "/repo/worktree-a", IsRoot: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertManagedRuntime(store.ManagedRuntime{
		ID: "rt-a", ProjectPath: "/repo", RootWorkspaceID: "ws-a",
		ActiveWorkspaceID: "ws-a", ActiveSessionID: "s-a",
		Source: "claude", Status: store.ManagedRuntimeRunning,
		HeartbeatAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	// Runtime B
	if err := st.CreateSession(event.Session{ID: "s-b", Source: "codex", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateWorkspace(workspace.Workspace{
		ID: "ws-b", Status: workspace.StatusActive, ProjectPath: "/repo",
		WorktreePath: "/repo/worktree-b", IsRoot: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertManagedRuntime(store.ManagedRuntime{
		ID: "rt-b", ProjectPath: "/repo", RootWorkspaceID: "ws-b",
		ActiveWorkspaceID: "ws-b", ActiveSessionID: "s-b",
		Source: "codex", Status: store.ManagedRuntimeRunning,
		HeartbeatAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	// Shell attached to runtime A should follow A, not B
	target := shellSyncWorktree(st, "rt-a", "follow", "/somewhere")
	if target != "/repo/worktree-a" {
		t.Fatalf("expected /repo/worktree-a, got %q", target)
	}

	// Shell attached to runtime B should follow B, not A
	target = shellSyncWorktree(st, "rt-b", "follow", "/somewhere")
	if target != "/repo/worktree-b" {
		t.Fatalf("expected /repo/worktree-b, got %q", target)
	}
}

func TestShellSyncWorktreeNoopWhenInSubdirectory(t *testing.T) {
	st := openManagedStore(t)
	now := time.Now().UTC()

	if err := st.CreateSession(event.Session{ID: "s-root", Source: "claude", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateWorkspace(workspace.Workspace{
		ID: "ws-root", Status: workspace.StatusActive, ProjectPath: "/repo",
		WorktreePath: "/repo", IsRoot: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertManagedRuntime(store.ManagedRuntime{
		ID: "rt-root", ProjectPath: "/repo", RootWorkspaceID: "ws-root",
		ActiveWorkspaceID: "ws-root", ActiveSessionID: "s-root",
		Source: "claude", Status: store.ManagedRuntimeRunning,
		HeartbeatAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	// Shell navigated into a subdirectory of the active worktree — should not snap back
	for _, subdir := range []string{
		"/repo/internal/cli",
		"/repo/cmd",
		"/repo/a/b/c/d",
	} {
		target := shellSyncWorktree(st, "rt-root", "follow", subdir)
		if target != "" {
			t.Fatalf("expected no-op for subdirectory %q, got %q", subdir, target)
		}
	}
}

func TestShellSyncWorktreeIgnoresCooledWorkspace(t *testing.T) {
	st := openManagedStore(t)
	now := time.Now().UTC()

	if err := st.CreateSession(event.Session{ID: "s-root", Source: "claude", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	// Workspace with empty worktree path (cooled to ref-only)
	if err := st.CreateWorkspace(workspace.Workspace{
		ID: "ws-cooled", Status: workspace.StatusFrozen, ProjectPath: "/repo",
		WorktreePath: "", GitRef: "refs/peek/ws-cooled", IsRoot: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertManagedRuntime(store.ManagedRuntime{
		ID: "rt-root", ProjectPath: "/repo", RootWorkspaceID: "ws-cooled",
		ActiveWorkspaceID: "ws-cooled", ActiveSessionID: "s-root",
		Source: "claude", Status: store.ManagedRuntimeRunning,
		HeartbeatAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	target := shellSyncWorktree(st, "rt-root", "follow", "/somewhere")
	if target != "" {
		t.Fatalf("expected empty target for cooled workspace, got %q", target)
	}
}

func TestShellSyncWorktreeDefaultFollowMode(t *testing.T) {
	st := openManagedStore(t)
	now := time.Now().UTC()

	if err := st.CreateSession(event.Session{ID: "s-root", Source: "claude", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateWorkspace(workspace.Workspace{
		ID: "ws-root", Status: workspace.StatusActive, ProjectPath: "/repo",
		WorktreePath: "/repo", IsRoot: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertManagedRuntime(store.ManagedRuntime{
		ID: "rt-root", ProjectPath: "/repo", RootWorkspaceID: "ws-root",
		ActiveWorkspaceID: "ws-root", ActiveSessionID: "s-root",
		Source: "claude", Status: store.ManagedRuntimeRunning,
		HeartbeatAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	// Empty follow mode should behave like "follow" (not pinned)
	target := shellSyncWorktree(st, "rt-root", "", "/somewhere/else")
	if target != "/repo" {
		t.Fatalf("expected /repo with empty follow mode, got %q", target)
	}
}
