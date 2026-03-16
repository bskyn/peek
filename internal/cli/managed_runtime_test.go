package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bskyn/peek/internal/event"
	"github.com/bskyn/peek/internal/managed"
	"github.com/bskyn/peek/internal/store"
	"github.com/bskyn/peek/internal/workspace"
)

func TestManagedSupervisorBranchAndSwitchHandoff(t *testing.T) {
	tests := []struct {
		name         string
		source       managed.Source
		sourceID     string
		resumeNeedle string
	}{
		{name: "claude", source: managed.SourceClaude, sourceID: "claude-root-source", resumeNeedle: "--resume\tclaude-root-source"},
		{name: "codex", source: managed.SourceCodex, sourceID: "codex-root-source", resumeNeedle: "resume\tcodex-root-source"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repoDir := testManagedRepo(t)
			worktreeBase := filepath.Join(t.TempDir(), "worktrees")
			logPath := filepath.Join(t.TempDir(), "launches.log")
			scriptPath := writeManagedStub(t, logPath)
			st := openManagedStore(t)
			now := time.Now().UTC()

			if err := st.CreateSession(event.Session{
				ID:              "s-root",
				Source:          string(tc.source),
				ProjectPath:     repoDir,
				SourceSessionID: tc.sourceID,
				CreatedAt:       now,
				UpdatedAt:       now,
			}); err != nil {
				t.Fatal(err)
			}
			if err := st.CreateWorkspace(workspace.Workspace{
				ID: "ws-root", Status: workspace.StatusActive, ProjectPath: repoDir, WorktreePath: repoDir, IsRoot: true, CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				t.Fatal(err)
			}
			if err := st.LinkWorkspaceSession(workspace.WorkspaceSession{
				WorkspaceID: "ws-root", SessionID: "s-root", CreatedAt: now,
			}); err != nil {
				t.Fatal(err)
			}
			if err := st.SaveBranchPath(workspace.BranchPathSegment{
				WorkspaceID: "ws-root", Depth: 0, Ordinal: 0,
			}); err != nil {
				t.Fatal(err)
			}

			if _, err := st.AppendEvents([]event.Event{
				{ID: "e0", SessionID: "s-root", Timestamp: now, Seq: 0, Type: event.EventUserMessage, PayloadJSON: mustJSONForCLI(t, map[string]string{"text": "branch this"})},
				{ID: "e3", SessionID: "s-root", Timestamp: now, Seq: 3, Type: event.EventToolCall, PayloadJSON: mustJSONForCLI(t, map[string]any{"tool_name": "Bash", "input": map[string]string{"command": "ls"}})},
			}); err != nil {
				t.Fatal(err)
			}

			ce := managed.NewCheckpointEngine(st, "ws-root", "s-root", repoDir)
			if err := ce.CapturePreTool(3); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(repoDir, "after-tool.txt"), []byte("post\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := ce.CapturePostTool(3); err != nil {
				t.Fatal(err)
			}

			orch := managed.NewOrchestrator(st, repoDir, worktreeBase)
			supervisor := newManagedSupervisor(
				st,
				nil,
				orch,
				tc.source,
				nil,
				"rt-root",
				"ws-root",
				"ws-root",
				"s-root",
				repoDir,
				nil,
				managedLaunchConfig{
					command: scriptPath,
					env:     []string{"PEEK_TEST_LAUNCH_LOG=" + logPath},
				},
			)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			errCh := make(chan error, 1)
			go func() {
				errCh <- supervisor.Run(ctx)
			}()

			waitForManagedRuntime(t, st, "ws-root")
			waitForLaunches(t, logPath, 1)

			branchReq, err := enqueueManagedBranchRequest(st, "ws-root", 3)
			if err != nil {
				t.Fatal(err)
			}
			if branchReq.ResponseWorkspaceID == "" || branchReq.ResponseSessionID == "" {
				t.Fatalf("expected branch response payload, got %#v", branchReq)
			}

			lines := waitForLaunches(t, logPath, 2)
			if !sameLaunchPath(lines[0], repoDir) {
				t.Fatalf("expected first launch in %s, got %q", repoDir, lines[0])
			}
			if !sameLaunchPath(lines[1], branchReq.ResponseWorktreePath) {
				t.Fatalf("expected branched launch in %s, got %q", branchReq.ResponseWorktreePath, lines[1])
			}
			if !strings.Contains(lines[1], "Resume this Peek-managed session") {
				t.Fatalf("expected transcript seed in branch launch args, got %q", lines[1])
			}

			switchReq, err := enqueueManagedSwitchRequest(st, "ws-root")
			if err != nil {
				t.Fatal(err)
			}
			if switchReq.ResponseWorkspaceID != "ws-root" || switchReq.ResponseSessionID != "s-root" {
				t.Fatalf("unexpected switch response: %#v", switchReq)
			}

			lines = waitForLaunches(t, logPath, 3)
			if !sameLaunchPath(lines[2], repoDir) {
				t.Fatalf("expected switched launch in %s, got %q", repoDir, lines[2])
			}
			if !strings.Contains(lines[2], tc.resumeNeedle) {
				t.Fatalf("expected resume args %q, got %q", tc.resumeNeedle, lines[2])
			}

			cancel()
			if err := <-errCh; err != nil {
				var exitCoder interface{ ExitCode() int }
				if !errors.As(err, &exitCoder) {
					t.Fatalf("expected exit-coded error on shutdown, got %v", err)
				}
			}
		})
	}
}

func TestManagedSupervisorParksStoppedRuntimeAtRoot(t *testing.T) {
	repoDir := testManagedRepo(t)
	worktreeBase := filepath.Join(t.TempDir(), "worktrees")
	logPath := filepath.Join(t.TempDir(), "launches.log")
	scriptPath := writeManagedStub(t, logPath)
	st := openManagedStore(t)
	now := time.Now().UTC()

	if err := st.CreateSession(event.Session{
		ID:              "s-root",
		Source:          string(managed.SourceClaude),
		ProjectPath:     repoDir,
		SourceSessionID: "claude-root-source",
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateWorkspace(workspace.Workspace{
		ID: "ws-root", Status: workspace.StatusActive, ProjectPath: repoDir, WorktreePath: repoDir, IsRoot: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.LinkWorkspaceSession(workspace.WorkspaceSession{
		WorkspaceID: "ws-root", SessionID: "s-root", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveBranchPath(workspace.BranchPathSegment{
		WorkspaceID: "ws-root", Depth: 0, Ordinal: 0,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := st.AppendEvents([]event.Event{
		{ID: "e0", SessionID: "s-root", Timestamp: now, Seq: 0, Type: event.EventUserMessage, PayloadJSON: mustJSONForCLI(t, map[string]string{"text": "branch this"})},
		{ID: "e3", SessionID: "s-root", Timestamp: now, Seq: 3, Type: event.EventToolCall, PayloadJSON: mustJSONForCLI(t, map[string]any{"tool_name": "Bash", "input": map[string]string{"command": "ls"}})},
	}); err != nil {
		t.Fatal(err)
	}

	ce := managed.NewCheckpointEngine(st, "ws-root", "s-root", repoDir)
	if err := ce.CapturePreTool(3); err != nil {
		t.Fatal(err)
	}

	supervisor := newManagedSupervisor(
		st,
		nil,
		managed.NewOrchestrator(st, repoDir, worktreeBase),
		managed.SourceClaude,
		nil,
		"rt-root",
		"ws-root",
		"ws-root",
		"s-root",
		repoDir,
		nil,
		managedLaunchConfig{
			command: scriptPath,
			env:     []string{"PEEK_TEST_LAUNCH_LOG=" + logPath},
		},
	)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- supervisor.Run(ctx)
	}()

	waitForManagedRuntime(t, st, "ws-root")

	branchReq, err := enqueueManagedBranchRequest(st, "ws-root", 3)
	if err != nil {
		t.Fatal(err)
	}
	if branchReq.ResponseWorkspaceID == "" {
		t.Fatalf("expected branch response payload, got %#v", branchReq)
	}
	waitForSupervisorWorkspace(t, supervisor, branchReq.ResponseWorkspaceID, branchReq.ResponseSessionID)

	cancel()
	if err := <-errCh; err != nil {
		var exitCoder interface{ ExitCode() int }
		if !errors.As(err, &exitCoder) {
			t.Fatalf("expected exit-coded error on shutdown, got %v", err)
		}
	}

	rt, err := st.GetManagedRuntime("rt-root")
	if err != nil {
		t.Fatal(err)
	}
	if rt.Status != store.ManagedRuntimeStopped {
		t.Fatalf("expected stopped runtime, got %s", rt.Status)
	}
	if rt.ActiveWorkspaceID != "ws-root" {
		t.Fatalf("expected runtime parked at root, got %s", rt.ActiveWorkspaceID)
	}
	if rt.ActiveSessionID != "s-root" {
		t.Fatalf("expected runtime parked at root session, got %s", rt.ActiveSessionID)
	}
}

func TestEnqueueManagedSwitchRequestRejectsStaleRuntime(t *testing.T) {
	st := openManagedStore(t)
	now := time.Now().UTC()

	if err := st.CreateSession(event.Session{ID: "s-root", Source: "claude", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateWorkspace(workspace.Workspace{
		ID: "ws-root", Status: workspace.StatusActive, ProjectPath: "/repo", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveBranchPath(workspace.BranchPathSegment{WorkspaceID: "ws-root", Depth: 0, Ordinal: 0}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertManagedRuntime(store.ManagedRuntime{
		ID:                "rt-stale",
		RootWorkspaceID:   "ws-root",
		ActiveWorkspaceID: "ws-root",
		ActiveSessionID:   "s-root",
		Source:            "claude",
		Status:            store.ManagedRuntimeRunning,
		HeartbeatAt:       now.Add(-10 * time.Second),
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatal(err)
	}

	_, err := enqueueManagedSwitchRequest(st, "ws-root")
	if err == nil {
		t.Fatal("expected stale runtime error")
	}
	var staleErr *staleManagedRuntimeError
	if !errors.As(err, &staleErr) {
		t.Fatalf("expected stale runtime error, got %v", err)
	}
}

func TestManagedSupervisorPropagatesProviderExitCode(t *testing.T) {
	repoDir := testManagedRepo(t)
	st := openManagedStore(t)
	now := time.Now().UTC()

	if err := st.CreateSession(event.Session{ID: "s-root", Source: "claude", ProjectPath: repoDir, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateWorkspace(workspace.Workspace{
		ID: "ws-root", Status: workspace.StatusActive, ProjectPath: repoDir, WorktreePath: repoDir, IsRoot: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveBranchPath(workspace.BranchPathSegment{WorkspaceID: "ws-root", Depth: 0, Ordinal: 0}); err != nil {
		t.Fatal(err)
	}

	scriptPath := writeExitStub(t, 17)
	supervisor := newManagedSupervisor(
		st,
		nil,
		managed.NewOrchestrator(st, repoDir, filepath.Join(t.TempDir(), "worktrees")),
		managed.SourceClaude,
		nil,
		"rt-exit",
		"ws-root",
		"ws-root",
		"s-root",
		repoDir,
		nil,
		managedLaunchConfig{command: scriptPath},
	)

	err := supervisor.Run(context.Background())
	if err == nil {
		t.Fatal("expected exit error")
	}
	var exitCoder interface{ ExitCode() int }
	if !errors.As(err, &exitCoder) {
		t.Fatalf("expected exit-coded error, got %v", err)
	}
	if exitCoder.ExitCode() != 17 {
		t.Fatalf("expected exit code 17, got %d", exitCoder.ExitCode())
	}
}

func TestPrepareManagedStartReattachesStoppedLeaseOwner(t *testing.T) {
	repoDir := testManagedRepo(t)
	st := openManagedStore(t)
	now := time.Now().UTC()

	if err := st.CreateSession(event.Session{
		ID:          "s-root",
		Source:      "claude",
		ProjectPath: repoDir,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateWorkspace(workspace.Workspace{
		ID:           "ws-root",
		Status:       workspace.StatusFrozen,
		ProjectPath:  repoDir,
		WorktreePath: repoDir,
		IsRoot:       true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.LinkWorkspaceSession(workspace.WorkspaceSession{
		WorkspaceID: "ws-root",
		SessionID:   "s-root",
		CreatedAt:   now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveBranchPath(workspace.BranchPathSegment{WorkspaceID: "ws-root", Depth: 0, Ordinal: 0}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertManagedRuntime(store.ManagedRuntime{
		ID:                "rt-root",
		ProjectPath:       repoDir,
		RootWorkspaceID:   "ws-root",
		ActiveWorkspaceID: "ws-root",
		ActiveSessionID:   "s-root",
		Source:            "claude",
		Status:            store.ManagedRuntimeStopped,
		HeartbeatAt:       now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertCheckoutLease(store.CheckoutLease{
		CheckoutPath: repoDir,
		RuntimeID:    "rt-root",
		WorkspaceID:  "ws-root",
		ClaimedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}

	start, err := prepareManagedStart(
		st,
		managed.NewOrchestrator(st, repoDir, filepath.Join(t.TempDir(), "worktrees")),
		managed.SourceClaude,
		repoDir,
		"",
		[]string{"--model", "sonnet"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !start.reusedRuntime {
		t.Fatal("expected runtime reattach")
	}
	if start.runtimeID != "rt-root" {
		t.Fatalf("expected rt-root, got %s", start.runtimeID)
	}
	if start.activeWorktree != repoDir {
		t.Fatalf("expected active worktree %s, got %s", repoDir, start.activeWorktree)
	}

	runtime, err := st.GetManagedRuntime("rt-root")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.ActiveSessionID == "s-root" {
		t.Fatal("expected a new reattach session id")
	}
	reattachSession, err := st.GetSession(runtime.ActiveSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if reattachSession.ProjectPath != repoDir {
		t.Fatalf("expected reattach session project path %s, got %s", repoDir, reattachSession.ProjectPath)
	}
}

func TestPrepareManagedStartIsolatesRootOnContention(t *testing.T) {
	repoDir := testManagedRepo(t)
	st := openManagedStore(t)
	now := time.Now().UTC()
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# changed in root checkout\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := st.CreateSession(event.Session{
		ID:          "s-root",
		Source:      "claude",
		ProjectPath: repoDir,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateWorkspace(workspace.Workspace{
		ID:           "ws-root",
		Status:       workspace.StatusActive,
		ProjectPath:  repoDir,
		WorktreePath: repoDir,
		IsRoot:       true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.LinkWorkspaceSession(workspace.WorkspaceSession{
		WorkspaceID: "ws-root",
		SessionID:   "s-root",
		CreatedAt:   now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveBranchPath(workspace.BranchPathSegment{WorkspaceID: "ws-root", Depth: 0, Ordinal: 0}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertManagedRuntime(store.ManagedRuntime{
		ID:                "rt-root",
		ProjectPath:       repoDir,
		RootWorkspaceID:   "ws-root",
		ActiveWorkspaceID: "ws-root",
		ActiveSessionID:   "s-root",
		Source:            "claude",
		Status:            store.ManagedRuntimeRunning,
		HeartbeatAt:       now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertCheckoutLease(store.CheckoutLease{
		CheckoutPath: repoDir,
		RuntimeID:    "rt-root",
		WorkspaceID:  "ws-root",
		ClaimedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}

	start, err := prepareManagedStart(
		st,
		managed.NewOrchestrator(st, repoDir, filepath.Join(t.TempDir(), "worktrees")),
		managed.SourceClaude,
		repoDir,
		"",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !start.isolatedRoot {
		t.Fatal("expected isolated root worktree")
	}
	if start.activeWorktree == repoDir {
		t.Fatal("expected a distinct worktree path under contention")
	}
	if !strings.Contains(start.activeWorktree, start.runtimeID) {
		t.Fatalf("expected isolated worktree to include runtime id, got %s", start.activeWorktree)
	}
	if _, err := os.Stat(start.activeWorktree); err != nil {
		t.Fatalf("expected isolated worktree on disk: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(start.activeWorktree, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "# changed in root checkout\n" {
		t.Fatalf("expected isolated runtime to inherit current checkout state, got %q", string(data))
	}
}

func TestPrepareManagedStartExplicitRuntimeIDReattachesStoppedIsolatedRuntime(t *testing.T) {
	repoDir := testManagedRepo(t)
	st := openManagedStore(t)
	now := time.Now().UTC()
	worktreePath := filepath.Join(t.TempDir(), "isolated-root")
	if err := managed.MaterializeRef("HEAD", worktreePath, repoDir); err != nil {
		t.Fatal(err)
	}

	if err := st.CreateSession(event.Session{
		ID:          "s-isolated",
		Source:      "claude",
		ProjectPath: worktreePath,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateWorkspace(workspace.Workspace{
		ID:           "ws-isolated",
		Status:       workspace.StatusFrozen,
		ProjectPath:  repoDir,
		WorktreePath: worktreePath,
		GitRef:       "HEAD",
		IsRoot:       true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.LinkWorkspaceSession(workspace.WorkspaceSession{
		WorkspaceID: "ws-isolated",
		SessionID:   "s-isolated",
		CreatedAt:   now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveBranchPath(workspace.BranchPathSegment{WorkspaceID: "ws-isolated", Depth: 0, Ordinal: 0}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertManagedRuntime(store.ManagedRuntime{
		ID:                "rt-isolated",
		ProjectPath:       repoDir,
		RootWorkspaceID:   "ws-isolated",
		ActiveWorkspaceID: "ws-isolated",
		ActiveSessionID:   "s-isolated",
		Source:            "claude",
		Status:            store.ManagedRuntimeStopped,
		HeartbeatAt:       now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatal(err)
	}

	start, err := prepareManagedStart(
		st,
		managed.NewOrchestrator(st, repoDir, filepath.Join(t.TempDir(), "worktrees")),
		managed.SourceClaude,
		repoDir,
		"rt-isolated",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !start.reusedRuntime {
		t.Fatal("expected explicit runtime reattach")
	}
	if start.runtimeID != "rt-isolated" {
		t.Fatalf("expected rt-isolated, got %s", start.runtimeID)
	}
	if start.activeWorktree != worktreePath {
		t.Fatalf("expected explicit reattach to use existing isolated worktree %s, got %s", worktreePath, start.activeWorktree)
	}
}

func testManagedRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}

	run("init")
	run("config", "user.name", "test")
	run("config", "user.email", "test@test.com")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "initial")
	return dir
}

func openManagedStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "managed.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func writeManagedStub(t *testing.T, logPath string) string {
	t.Helper()
	scriptPath := filepath.Join(t.TempDir(), "managed-stub.sh")
	script := `#!/bin/sh
args=$(printf '%s\n' "$@" | tr '\n' '\t')
printf '%s|%s\n' "$PWD" "$args" >> "$PEEK_TEST_LAUNCH_LOG"
trap 'exit 0' INT TERM
while :; do
  sleep 0.1
done
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return scriptPath
}

func writeExitStub(t *testing.T, code int) string {
	t.Helper()
	scriptPath := filepath.Join(t.TempDir(), "exit-stub.sh")
	script := "#!/bin/sh\nexit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return scriptPath
}

func waitForManagedRuntime(t *testing.T, st *store.Store, workspaceID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := liveManagedRuntimeForWorkspace(st, workspaceID); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for live managed runtime for %s", workspaceID)
}

func waitForSupervisorWorkspace(t *testing.T, supervisor *managedSupervisor, workspaceID, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if supervisor.activeWorkspaceID == workspaceID && supervisor.activeSessionID == sessionID {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for supervisor to switch to %s/%s", workspaceID, sessionID)
}

func waitForLaunches(t *testing.T, logPath string, want int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatal(err)
		}
		lines := splitNonEmptyLines(string(data))
		if len(lines) >= want {
			return lines
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d launches in %s", want, logPath)
	return nil
}

func splitNonEmptyLines(s string) []string {
	raw := strings.Split(strings.TrimSpace(s), "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func mustJSONForCLI(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func sameLaunchPath(line, want string) bool {
	gotPath := line
	if idx := strings.IndexByte(line, '|'); idx >= 0 {
		gotPath = line[:idx]
	}
	return normalizeTestPath(gotPath) == normalizeTestPath(want)
}

func normalizeTestPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}
