package viewer

import (
	"fmt"
	"os/exec"
	"runtime"
)

// BrowserOpener opens a URL in the user's default browser.
type BrowserOpener interface {
	Open(target string) error
}

type commandBrowserOpener struct{}

// NewBrowserOpener returns the platform opener used by the viewer runtime.
func NewBrowserOpener() BrowserOpener {
	return commandBrowserOpener{}
}

func (commandBrowserOpener) Open(target string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	return nil
}
