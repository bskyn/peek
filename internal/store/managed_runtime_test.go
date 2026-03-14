package store

import (
	"testing"
	"time"

	"github.com/bskyn/peek/internal/event"
	"github.com/bskyn/peek/internal/workspace"
)

func TestManagedRuntimeRoundTripAndLookupByWorkspace(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	if err := s.CreateSession(event.Session{ID: "s-root", Source: "claude", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(event.Session{ID: "s-child", Source: "claude", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	if err := s.CreateWorkspace(workspace.Workspace{
		ID: "ws-root", Status: workspace.StatusActive, ProjectPath: "/repo", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	branchSeq := int64(3)
	if err := s.CreateWorkspace(workspace.Workspace{
		ID: "ws-child", ParentWorkspaceID: "ws-root", Status: workspace.StatusFrozen, ProjectPath: "/repo", BranchFromSeq: &branchSeq, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveBranchPath(workspace.BranchPathSegment{WorkspaceID: "ws-root", Depth: 0, Ordinal: 0}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveBranchPath(workspace.BranchPathSegment{WorkspaceID: "ws-child", ParentWorkspaceID: "ws-root", BranchSeq: branchSeq, Depth: 1, Ordinal: 0}); err != nil {
		t.Fatal(err)
	}

	rt := ManagedRuntime{
		ID:                "rt-1",
		RootWorkspaceID:   "ws-root",
		ActiveWorkspaceID: "ws-child",
		ActiveSessionID:   "s-child",
		Source:            "claude",
		LaunchArgs:        []string{"--model", "sonnet"},
		Status:            ManagedRuntimeRunning,
		HeartbeatAt:       now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := s.UpsertManagedRuntime(rt); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetManagedRuntimeForWorkspace("ws-child")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != rt.ID {
		t.Fatalf("expected runtime %q, got %q", rt.ID, got.ID)
	}
	if len(got.LaunchArgs) != 2 || got.LaunchArgs[1] != "sonnet" {
		t.Fatalf("unexpected launch args: %#v", got.LaunchArgs)
	}
}

func TestManagedRuntimeRequestLifecycle(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	if err := s.CreateSession(event.Session{ID: "s-root", Source: "claude", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateWorkspace(workspace.Workspace{
		ID: "ws-root", Status: workspace.StatusActive, ProjectPath: "/repo", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveBranchPath(workspace.BranchPathSegment{WorkspaceID: "ws-root", Depth: 0, Ordinal: 0}); err != nil {
		t.Fatal(err)
	}

	if err := s.UpsertManagedRuntime(ManagedRuntime{
		ID:                "rt-1",
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

	req := ManagedRuntimeRequest{
		ID:                "req-1",
		RuntimeID:         "rt-1",
		Kind:              "branch",
		SourceWorkspaceID: "ws-root",
		Status:            ManagedRuntimeRequestPending,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	seq := int64(5)
	req.BranchFromSeq = &seq

	if err := s.CreateManagedRuntimeRequest(req); err != nil {
		t.Fatal(err)
	}

	claimed, err := s.ClaimNextManagedRuntimeRequest("rt-1")
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil || claimed.Status != ManagedRuntimeRequestProcessing {
		t.Fatalf("expected claimed processing request, got %#v", claimed)
	}
	if err := s.CompleteManagedRuntimeRequest("req-1", ManagedRuntimeResponse{
		WorkspaceID: "ws-child",
		SessionID:   "s-child",
		GitRef:      "refs/peek/ws-child/5/pre_tool",
	}); err != nil {
		t.Fatal(err)
	}

	done, err := s.GetManagedRuntimeRequest("req-1")
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != ManagedRuntimeRequestCompleted {
		t.Fatalf("expected completed request, got %s", done.Status)
	}
	if done.ResponseWorkspaceID != "ws-child" || done.ResponseSessionID != "s-child" {
		t.Fatalf("unexpected response payload: %#v", done)
	}
}
