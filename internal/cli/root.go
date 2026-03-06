package cli

import (
	"github.com/spf13/cobra"
)

var (
	version = "dev"
	dbPath  string
	verbose bool
)

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "peek",
		Short: "Agent introspection dashboard",
		Long:  "Observe and inspect AI agent sessions in real-time.",
	}

	cmd.PersistentFlags().StringVar(&dbPath, "db-path", defaultDBPath(), "Path to SQLite database")
	cmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "Enable verbose output")

	cmd.Version = version

	cmd.AddCommand(newClaudeCmd())
	cmd.AddCommand(newCodexCmd())
	cmd.AddCommand(newSessionsCmd())

	return cmd
}

func defaultDBPath() string {
	return homeDir() + "/.peek/data.db"
}

// Execute runs the root command.
func Execute() error {
	return newRootCmd().Execute()
}
