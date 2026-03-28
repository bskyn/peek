//go:build windows

package companion

import (
	"os"
	"os/exec"
)

func configureServiceCommand(cmd *exec.Cmd) {}

func processAlive(pid int) bool {
	return pid > 0
}

func interruptProcess(pid int) error {
	return nil
}

func killProcess(pid int) error {
	if pid <= 0 {
		return nil
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}
