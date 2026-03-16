package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/bskyn/peek/internal/viewer"
)

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}

func addViewerFlags(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&webEnabled, "web", true, "Start the local web viewer alongside terminal output")
	cmd.Flags().BoolVar(&noWeb, "no-web", false, "Disable the local web viewer and keep terminal output only")
	cmd.Flags().BoolVar(&openBrowser, "open-browser", true, "Open the local viewer in the default browser")
	cmd.Flags().IntVar(&webPort, "web-port", 0, "Port for the local viewer (0 selects an ephemeral port)")
	cmd.MarkFlagsMutuallyExclusive("web", "no-web")
}

func buildViewerOptions(initialSessionID, runtimeID string) viewer.ViewerOptions {
	return viewer.NormalizeViewerOptions(viewer.ViewerOptions{
		Enabled:          webEnabled && !noWeb,
		OpenBrowser:      openBrowser,
		Port:             webPort,
		InitialSessionID: initialSessionID,
		CurrentRuntimeID: runtimeID,
	})
}
