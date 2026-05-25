package tunrun

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

func RunExec(ctx context.Context, cfg ExecConfig, args []string) int {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()

	attr := &syscall.SysProcAttr{}
	if _, err := unix.IoctlGetTermios(int(os.Stdin.Fd()), unix.TCGETS); err == nil {
		attr.Foreground = true
		attr.Ctty = int(os.Stdin.Fd())
	}
	if cfg.UID >= 0 || cfg.GID >= 0 {
		attr.Credential = &syscall.Credential{
			Uid:    uint32(cfg.UID),
			Gid:    uint32(cfg.GID),
			Groups: cfg.Groups,
		}
	}
	cmd.SysProcAttr = attr

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "tunrun: start target command: %v\n", err)
		return 127
	}

	err := cmd.Wait()
	if err == nil {
		return 0
	}
	if ctx.Err() != nil {
		return 130
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			if status.Signaled() {
				return 128 + int(status.Signal())
			}
			return status.ExitStatus()
		}
	}
	fmt.Fprintf(os.Stderr, "tunrun: target command failed: %v\n", err)
	return 1
}
