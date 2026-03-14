package cli

import (
	"github.com/spf13/cobra"
)

var (
	version     = "dev"
	dbPath      string
	verbose     bool
	webEnabled  bool
	noWeb       bool
	openBrowser bool
	webPort     int
)

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "peek",
		Short: "Agent introspection dashboard",
		Long:  "Launch managed agent sessions with branching and checkpoints, or observe existing sessions in real-time.\n\n  peek run claude    Launch a managed Claude session (recommended)\n  peek run codex     Launch a managed Codex session\n  peek claude        Monitor an existing Claude session\n  peek codex         Monitor an existing Codex session",
	}

	cmd.PersistentFlags().StringVar(&dbPath, "db-path", defaultDBPath(), "Path to SQLite database")
	cmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "Enable verbose output")

	cmd.Version = version

	cmd.AddCommand(newClaudeCmd())
	cmd.AddCommand(newCodexCmd())
	cmd.AddCommand(newRunCmd())
	cmd.AddCommand(newWorkspaceCmd())
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
