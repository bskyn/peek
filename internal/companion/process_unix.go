//go:build !windows

package companion

import (
	"errors"
	"os/exec"
	"syscall"
)

func configureServiceCommand(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func interruptProcess(pid int) error {
	return signalProcessGroupOrProcess(pid, syscall.SIGINT)
}

func killProcess(pid int) error {
	return signalProcessGroupOrProcess(pid, syscall.SIGKILL)
}

func signalProcessGroupOrProcess(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pid, sig); err == nil || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return syscall.Kill(pid, sig)
}
