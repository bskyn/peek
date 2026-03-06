package cli

import (
	"fmt"
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
