package cli

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/bskyn/peek/internal/managed"
	"github.com/bskyn/peek/internal/store"
	"github.com/bskyn/peek/internal/workspace"
)

func newWorkspaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "workspace",
		Aliases: []string{"ws"},
		Short:   "Manage branching workspaces",
	}

	cmd.AddCommand(newWorkspaceListCmd())
	cmd.AddCommand(newWorkspaceBranchCmd())
	cmd.AddCommand(newWorkspaceSwitchCmd())
	cmd.AddCommand(newWorkspaceMergeCmd())
	cmd.AddCommand(newWorkspaceFreezeCmd())
	cmd.AddCommand(newWorkspaceCoolCmd())
	cmd.AddCommand(newWorkspaceDeleteCmd())
	cmd.AddCommand(newWorkspacePruneCmd())
	cmd.AddCommand(newWorkspaceStatusCmd())

	return cmd
}

func newWorkspaceListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all workspaces",
		RunE: func(_ *cobra.Command, _ []string) error {
			st, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer st.Close()

			summaries, err := st.ListWorkspaces()
			if err != nil {
				return err
			}
			if len(summaries) == 0 {
				fmt.Println("No workspaces found.")
				return nil
			}

			for _, s := range summaries {
				parent := ""
				if s.ParentWorkspaceID != "" {
					parent = fmt.Sprintf(" (from %s", s.ParentWorkspaceID)
					if s.BranchFromSeq != nil {
						parent += fmt.Sprintf(" @seq %d", *s.BranchFromSeq)
					}
					parent += ")"
				}
				fmt.Printf("  %s  [%s]  %s%s\n", s.ID, s.Status, s.ProjectPath, parent)
			}
			return nil
		},
	}
}

func newWorkspaceBranchCmd() *cobra.Command {
	var shellMode bool

	cmd := &cobra.Command{
		Use:   "branch <workspace-id> <seq>",
		Short: "Send a branch request to the live managed runtime",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			wsID := args[0]
			seq, err := strconv.ParseInt(args[1], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid sequence number: %w", err)
			}

			st, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer st.Close()

			result, err := enqueueManagedBranchRequest(st, wsID, seq)
			if err != nil {
				return err
			}

			if shellMode {
				tr := workspaceTransitionResult{
					RuntimeID:    result.RuntimeID,
					WorkspaceID:  result.ResponseWorkspaceID,
					SessionID:    result.ResponseSessionID,
					WorktreePath: result.ResponseWorktreePath,
					Kind:         "branch",
					SourceFrozen: wsID,
				}
				if rt, rtErr := st.GetManagedRuntime(result.RuntimeID); rtErr == nil {
					tr.ProjectPath = rt.ProjectPath
				}
				fmt.Fprintf(os.Stderr, "Branched from %s @seq %d\n", wsID, seq)
				fmt.Fprintf(os.Stderr, "  New workspace: %s\n", result.ResponseWorkspaceID)
				fmt.Fprintf(os.Stderr, "  Worktree:      %s\n", result.ResponseWorktreePath)
				fmt.Print(renderTransitionShell(tr))
			} else {
				fmt.Printf("Branched from %s @seq %d\n", wsID, seq)
				fmt.Printf("  New workspace: %s\n", result.ResponseWorkspaceID)
				fmt.Printf("  New session:   %s\n", result.ResponseSessionID)
				fmt.Printf("  Worktree:      %s\n", result.ResponseWorktreePath)
				fmt.Printf("  Source %s is now frozen.\n", wsID)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&shellMode, "shell", false, "Emit eval-safe shell variables (for hook consumers)")
	return cmd
}

func newWorkspaceSwitchCmd() *cobra.Command {
	var shellMode bool

	cmd := &cobra.Command{
		Use:   "switch <workspace-id>",
		Short: "Send a switch request to the live managed runtime",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			st, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer st.Close()

			result, err := enqueueManagedSwitchRequest(st, args[0])
			if err != nil {
				return err
			}

			if shellMode {
				tr := workspaceTransitionResult{
					RuntimeID:    result.RuntimeID,
					WorkspaceID:  result.ResponseWorkspaceID,
					SessionID:    result.ResponseSessionID,
					WorktreePath: result.ResponseWorktreePath,
					Kind:         "switch",
				}
				if rt, rtErr := st.GetManagedRuntime(result.RuntimeID); rtErr == nil {
					tr.ProjectPath = rt.ProjectPath
				}
				ws, wsErr := st.GetWorkspace(result.ResponseWorkspaceID)
				if wsErr == nil {
					fmt.Fprintf(os.Stderr, "Switched to workspace %s [%s]\n", ws.ID, ws.Status)
				}
				if result.ResponseWorktreePath != "" {
					fmt.Fprintf(os.Stderr, "  Worktree: %s\n", result.ResponseWorktreePath)
				}
				fmt.Print(renderTransitionShell(tr))
			} else {
				ws, err := st.GetWorkspace(result.ResponseWorkspaceID)
				if err != nil {
					return err
				}
				fmt.Printf("Switched to workspace %s [%s]\n", ws.ID, ws.Status)
				if result.ResponseWorktreePath != "" {
					fmt.Printf("  Worktree: %s\n", result.ResponseWorktreePath)
				}
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&shellMode, "shell", false, "Emit eval-safe shell variables (for hook consumers)")
	return cmd
}

func newWorkspaceMergeCmd() *cobra.Command {
	var targetID string

	cmd := &cobra.Command{
		Use:   "merge <branch-workspace-id>",
		Short: "Merge a branch workspace back into its parent",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			st, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer st.Close()

			projectDir, err := os.Getwd()
			if err != nil {
				return err
			}

			orch := managed.NewOrchestrator(st, projectDir, managedWorktreeBase())
			result, err := orch.Merge(managed.MergeRequest{
				BranchWorkspaceID: args[0],
				TargetWorkspaceID: targetID,
			})
			if err != nil {
				return err
			}

			if result.Clean {
				fmt.Printf("Merged %s cleanly into %s\n", args[0], result.TargetWorktree)
			} else {
				fmt.Printf("Merge conflicts detected in %s:\n", result.TargetWorktree)
				for _, f := range result.ConflictFiles {
					fmt.Printf("  - %s\n", f)
				}
				fmt.Println("\nResolve conflicts manually, then run:")
				fmt.Println("  cd", result.TargetWorktree)
				fmt.Println("  git add . && git commit")
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&targetID, "into", "", "Target workspace ID (defaults to parent)")
	return cmd
}

func newWorkspaceFreezeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "freeze <workspace-id>",
		Short: "Freeze a workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			st, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer st.Close()

			if err := st.UpdateWorkspaceStatus(args[0], workspace.StatusFrozen); err != nil {
				return err
			}
			fmt.Printf("Workspace %s frozen.\n", args[0])
			return nil
		},
	}
}

func newWorkspaceCoolCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cool <workspace-id>",
		Short: "Dematerialize a frozen workspace to ref-only storage",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			st, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer st.Close()

			projectDir, err := os.Getwd()
			if err != nil {
				return err
			}

			orch := managed.NewOrchestrator(st, projectDir, managedWorktreeBase())
			if err := orch.CoolWorkspace(args[0]); err != nil {
				return err
			}
			fmt.Printf("Workspace %s cooled to ref-only storage.\n", args[0])
			return nil
		},
	}
}

func newWorkspaceDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <workspace-id>",
		Short: "Delete an inactive branch workspace or prune a stopped root lineage",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := runWorkspaceDelete(args[0]); err != nil {
				return err
			}
			fmt.Printf("Deleted workspace %s.\n", args[0])
			return nil
		},
	}
}

func runWorkspaceDelete(workspaceID string) error {
	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	projectDir, err := os.Getwd()
	if err != nil {
		return err
	}
	projectDir, err = filepath.Abs(projectDir)
	if err != nil {
		return err
	}

	orch := managed.NewOrchestrator(st, projectDir, managedWorktreeBase())
	return orch.DeleteWorkspace(workspaceID)
}

func newWorkspacePruneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prune",
		Short: "Prune stopped root workspace lineages for the current repo",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			result, err := runWorkspacePrune()
			if err != nil {
				return err
			}
			if len(result.PrunedRoots) == 0 {
				fmt.Println("No pruneable root workspaces found.")
				return nil
			}

			fmt.Printf("Pruned %d root workspace lineage(s):\n", len(result.PrunedRoots))
			for _, rootID := range result.PrunedRoots {
				fmt.Printf("  %s\n", rootID)
			}
			if len(result.SkippedRoots) > 0 {
				fmt.Printf("Skipped %d root workspace lineage(s) that are still in use:\n", len(result.SkippedRoots))
				for _, rootID := range result.SkippedRoots {
					fmt.Printf("  %s\n", rootID)
				}
			}
			return nil
		},
	}
}

type workspacePruneResult struct {
	PrunedRoots  []string
	SkippedRoots []string
}

func runWorkspacePrune() (*workspacePruneResult, error) {
	st, err := store.Open(dbPath)
	if err != nil {
		return nil, err
	}
	defer st.Close()

	projectDir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	projectDir, err = filepath.Abs(projectDir)
	if err != nil {
		return nil, err
	}

	roots, err := st.ListRootWorkspacesByProjectPath(projectDir)
	if err != nil {
		return nil, err
	}

	skipped := make([]string, 0)
	if lease, err := st.GetCheckoutLease(projectDir); err == nil {
		skipped = append(skipped, lease.WorkspaceID)
	} else if err != sql.ErrNoRows {
		return nil, fmt.Errorf("load checkout lease: %w", err)
	}

	orch := managed.NewOrchestrator(st, projectDir, managedWorktreeBase())
	pruned := make([]string, 0)
	for _, root := range roots {
		if containsString(skipped, root.ID) {
			continue
		}

		runtime, err := st.GetManagedRuntimeByRootWorkspace(root.ID)
		if err == nil && runtime.Status != store.ManagedRuntimeStopped {
			skipped = append(skipped, root.ID)
			continue
		}
		if err != nil && err != sql.ErrNoRows {
			return nil, fmt.Errorf("load managed runtime for %s: %w", root.ID, err)
		}

		if _, err := orch.PruneWorkspaceLineage(root.ID); err != nil {
			return nil, err
		}
		pruned = append(pruned, root.ID)
	}

	return &workspacePruneResult{PrunedRoots: pruned, SkippedRoots: skipped}, nil
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func newWorkspaceStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <workspace-id>",
		Short: "Show workspace details",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			st, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer st.Close()

			summary, err := st.GetWorkspaceSummary(args[0])
			if err != nil {
				return fmt.Errorf("workspace not found: %w", err)
			}

			fmt.Printf("Workspace: %s\n", summary.ID)
			fmt.Printf("  Status:      %s\n", summary.Status)
			fmt.Printf("  Project:     %s\n", summary.ProjectPath)
			if summary.WorktreePath != "" {
				fmt.Printf("  Worktree:    %s\n", summary.WorktreePath)
			}
			if summary.GitRef != "" {
				fmt.Printf("  Git ref:     %s\n", summary.GitRef)
			}
			if summary.ParentWorkspaceID != "" {
				fmt.Printf("  Parent:      %s\n", summary.ParentWorkspaceID)
			}
			if summary.BranchFromSeq != nil {
				fmt.Printf("  Branch seq:  %d\n", *summary.BranchFromSeq)
			}
			fmt.Printf("  Sessions:    %d\n", summary.SessionCount)
			fmt.Printf("  Checkpoints: %d\n", summary.CheckpointCount)

			// Show branch path
			path, err := st.GetBranchPath(args[0])
			if err == nil && len(path) > 1 {
				fmt.Printf("  Lineage:     ")
				for i, seg := range path {
					if i > 0 {
						fmt.Printf(" -> ")
					}
					fmt.Printf("%s", seg.WorkspaceID)
					if seg.BranchSeq > 0 {
						fmt.Printf("@%d", seg.BranchSeq)
					}
				}
				fmt.Println()
			}

			// Show children
			children, err := st.ListChildWorkspaces(args[0])
			if err == nil && len(children) > 0 {
				fmt.Printf("  Children:\n")
				for _, child := range children {
					fmt.Printf("    - %s [%s]", child.ID, child.Status)
					if child.BranchFromSeq != nil {
						fmt.Printf(" @seq %d", *child.BranchFromSeq)
					}
					fmt.Println()
				}
			}

			return nil
		},
	}
}
