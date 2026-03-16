package companion

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bskyn/peek/internal/event"
	"github.com/bskyn/peek/internal/store"
	"github.com/bskyn/peek/internal/workspace"
)

func TestManagerActivateMaterializesBootstrapsAndReuses(t *testing.T) {
	repoDir := t.TempDir()
	worktreePath := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".env.local"), []byte("ROOT_SECRET=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktreePath, "package.json"), []byte(`{"name":"fixture"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktreePath, "message.txt"), []byte("workspace-root\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	spec := &ProjectRuntimeSpec{
		Bootstrap: BootstrapSpec{
			FingerprintPaths: []string{"package.json"},
			Commands: []CommandSpec{{
				Command: []string{"/bin/sh", "-c", "echo bootstrap >> bootstrap.log"},
			}},
		},
		EnvSources: []EnvSourceSpec{{Path: ".env.local"}},
		Services: []CompanionServiceSpec{{
			Name:    "web",
			Role:    ServiceRolePrimary,
			Command: []string{"/bin/sh", "-c", "touch ready.flag; trap 'exit 0' INT TERM; while :; do sleep 1; done"},
			Ready: ReadinessProbe{
				Type:           ProbeTypeFile,
				Path:           "ready.flag",
				TimeoutSeconds: 5,
				IntervalMillis: 100,
			},
		}},
		Browser: BrowserTargetSpec{Service: "web", PathPrefix: "/app/"},
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("validate spec: %v", err)
	}

	st := openCompanionStore(t)
	createRuntimeFixture(t, st, repoDir, worktreePath)

	manager := NewManager(st, repoDir, "rt-root", spec, nil)
	t.Cleanup(func() {
		_ = manager.Stop(context.Background())
	})

	result, err := manager.Activate(context.Background(), "ws-root", worktreePath)
	if err != nil {
		t.Fatalf("activate runtime: %v", err)
	}
	if result.PrimaryTargetURL != "" {
		t.Fatalf("unexpected primary target: %s", result.PrimaryTargetURL)
	}

	envData, err := os.ReadFile(filepath.Join(worktreePath, ".env.local"))
	if err != nil {
		t.Fatalf("read materialized env: %v", err)
	}
	if string(envData) != "ROOT_SECRET=1\n" {
		t.Fatalf("unexpected env contents: %q", string(envData))
	}

	bootstrapLog, err := os.ReadFile(filepath.Join(worktreePath, "bootstrap.log"))
	if err != nil {
		t.Fatalf("read bootstrap log: %v", err)
	}
	if strings.Count(string(bootstrapLog), "bootstrap") != 1 {
		t.Fatalf("expected single bootstrap execution, got %q", string(bootstrapLog))
	}

	bootstrapState, err := st.GetWorkspaceBootstrapState("ws-root")
	if err != nil {
		t.Fatalf("load bootstrap state: %v", err)
	}
	if bootstrapState.Status != store.BootstrapSucceeded {
		t.Fatalf("expected succeeded bootstrap state, got %s", bootstrapState.Status)
	}

	serviceStates, err := st.ListCompanionServiceStates("rt-root")
	if err != nil {
		t.Fatalf("load service states: %v", err)
	}
	if len(serviceStates) != 1 || serviceStates[0].Status != store.CompanionServiceReady {
		t.Fatalf("unexpected service states: %+v", serviceStates)
	}

	snapshot := manager.Snapshot()
	if snapshot.Phase != ActivationReady || snapshot.Bootstrap.Reused {
		t.Fatalf("unexpected initial snapshot: %+v", snapshot)
	}

	if _, err := manager.Activate(context.Background(), "ws-root", worktreePath); err != nil {
		t.Fatalf("reactivate runtime: %v", err)
	}
	bootstrapLog, err = os.ReadFile(filepath.Join(worktreePath, "bootstrap.log"))
	if err != nil {
		t.Fatalf("read bootstrap log after reuse: %v", err)
	}
	if strings.Count(string(bootstrapLog), "bootstrap") != 1 {
		t.Fatalf("expected bootstrap reuse, got %q", string(bootstrapLog))
	}

	snapshot = manager.Snapshot()
	if !snapshot.Bootstrap.Reused {
		t.Fatalf("expected reused bootstrap snapshot, got %+v", snapshot.Bootstrap)
	}
}

func openCompanionStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "peek.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func createRuntimeFixture(t *testing.T, st *store.Store, repoDir, worktreePath string) {
	t.Helper()
	now := time.Now().UTC()
	if err := st.CreateSession(event.Session{
		ID:          "s-root",
		Source:      "codex",
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
		WorktreePath: worktreePath,
		IsRoot:       true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveBranchPath(workspace.BranchPathSegment{
		WorkspaceID: "ws-root",
		Depth:       0,
		Ordinal:     0,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertManagedRuntime(store.ManagedRuntime{
		ID:                "rt-root",
		RootWorkspaceID:   "ws-root",
		ActiveWorkspaceID: "ws-root",
		ActiveSessionID:   "s-root",
		Source:            "codex",
		Status:            store.ManagedRuntimeRunning,
		HeartbeatAt:       now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatal(err)
	}
}
