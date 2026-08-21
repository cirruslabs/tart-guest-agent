//go:build windows

package rpc

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

func applyUserOverride(cmd *exec.Cmd, user string) error {
	if user == "" {
		return nil
	}
	return fmt.Errorf("user override is not supported on Windows")
}

func configureDetachedSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr.CreationFlags = syscall.CREATE_NEW_PROCESS_GROUP
}

func configurePgidSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr.CreationFlags = syscall.CREATE_NEW_PROCESS_GROUP
}

func signalProcessGroup(process *os.Process, signal syscall.Signal) error {
	if process == nil {
		return os.ErrProcessDone
	}

	// Terminate process tree (/T) forcefully (/F) on Windows
	killCmd := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(process.Pid))
	if err := killCmd.Run(); err == nil {
		return nil
	}

	return process.Kill()
}
