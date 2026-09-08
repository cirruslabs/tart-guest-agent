//go:build !windows

package rpc

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/cirruslabs/tart-guest-agent/internal/execuser"
)

func applyUserOverride(cmd *exec.Cmd, user string) error {
	if user == "" {
		return nil
	}

	credential, err := execuser.Resolve(user)
	if err != nil {
		return fmt.Errorf("failed to apply user override %q: %w", user, err)
	}

	if credential.Uid == uint32(os.Geteuid()) && credential.Gid == uint32(os.Getegid()) {
		return nil
	}

	cmd.SysProcAttr.Credential = credential
	return nil
}

func configureDetachedSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr.Setsid = true
}

func configurePgidSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr.Setpgid = true
}

func signalProcessGroup(process *os.Process, signal syscall.Signal) error {
	if err := syscall.Kill(-process.Pid, signal); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return nil
}
