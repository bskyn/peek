package cli

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bskyn/peek/internal/store"
)

func newSessionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "Manage stored sessions",
	}

	cmd.AddCommand(newSessionsListCmd())
	cmd.AddCommand(newSessionsDeleteCmd())
	cmd.AddCommand(newSessionsLoadCmd())
	return cmd
}

func newSessionsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all stored sessions",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runSessionsList()
		},
	}
}

func newSessionsDeleteCmd() *cobra.Command {
	var deleteAll bool
	var cmd *cobra.Command

	cmd = &cobra.Command{
		Use:   "delete [session-id]",
		Short: "Delete stored sessions",
		Long:  "Delete one stored session by internal ID or raw source session ID, or wipe all stored sessions with --all.",
		Args: func(_ *cobra.Command, args []string) error {
			if deleteAll {
				if len(args) != 0 {
					return fmt.Errorf("--all does not take a session ID")
				}
				return nil
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(_ *cobra.Command, args []string) error {
			var sessionID string
			if len(args) > 0 {
				sessionID = args[0]
			}
			return runSessionsDelete(sessionID, deleteAll)
		},
	}

	cmd.Flags().BoolVar(&deleteAll, "all", false, "Delete all stored sessions, events, and cursors")

	return cmd
}

func runSessionsList() error {
	st, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Close()

	sessions, err := st.ListSessions()
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}

	if len(sessions) == 0 {
		fmt.Println("No sessions stored yet.")
		fmt.Println("Run 'peek claude' to start tailing a session.")
		return nil
	}

	fmt.Printf("%-40s  %-8s  %-20s  %s\n", "SESSION ID", "SOURCE", "CREATED", "PROJECT")
	fmt.Printf("%-40s  %-8s  %-20s  %s\n", "----------", "------", "-------", "-------")
	for _, s := range sessions {
		created := s.CreatedAt.Format(time.RFC3339)
		project := s.ProjectPath
		if len(project) > 40 {
			project = "..." + project[len(project)-37:]
		}
		fmt.Printf("%-40s  %-8s  %-20s  %s\n", s.ID, s.Source, created, project)
	}

	return nil
}

func runSessionsDelete(sessionID string, deleteAll bool) error {
	st, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Close()

	if deleteAll {
		deleted, err := st.DeleteAllSessions()
		if err != nil {
			return fmt.Errorf("delete all sessions: %w", err)
		}
		fmt.Printf("Deleted %d stored session(s).\n", deleted)
		return nil
	}

	resolvedID, err := st.ResolveSessionID(sessionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("session %q not found", sessionID)
		}

		var ambiguousErr *store.AmbiguousSessionIDError
		if errors.As(err, &ambiguousErr) {
			return fmt.Errorf(
				"session %q is ambiguous; use one of the internal IDs instead: %s",
				sessionID,
				strings.Join(ambiguousErr.Matches, ", "),
			)
		}

		return fmt.Errorf("resolve session %q: %w", sessionID, err)
	}

	deleted, err := st.DeleteSession(resolvedID)
	if err != nil {
		return fmt.Errorf("delete session %q: %w", resolvedID, err)
	}
	if !deleted {
		return fmt.Errorf("session %q not found", sessionID)
	}

	fmt.Printf("Deleted stored session %s.\n", resolvedID)
	return nil
}
