package tunrun

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

func ElevateWithSudo(originalArgs []string, proxyURL string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if _, err := exec.LookPath("sudo"); err != nil {
		return fmt.Errorf("root is required and sudo was not found")
	}

	proxyFile, err := writeProxyFile(proxyURL)
	if err != nil {
		return err
	}
	defer os.Remove(proxyFile)

	args := sudoCommandArgs(exe, proxyFile, originalArgs, os.Getenv("PATH"))

	cmd := exec.Command("sudo", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = EnvironmentWithoutProxy(os.Environ())

	err = cmd.Run()
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			if status.Signaled() {
				return ExitError{Code: 128 + int(status.Signal())}
			}
			return ExitError{Code: status.ExitStatus()}
		}
	}
	return err
}

func sudoCommandArgs(exe, proxyFile string, originalArgs []string, targetPath string) []string {
	args := []string{exe, "_sudo", "-proxy-file", proxyFile}
	if targetPath != "" {
		args = append(args, "-target-path", targetPath)
	}
	args = append(args, "--")
	args = append(args, originalArgs...)
	return args
}

func writeProxyFile(proxyURL string) (string, error) {
	f, err := os.CreateTemp("", "tunrun-proxy-*")
	if err != nil {
		return "", err
	}
	path := f.Name()
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()

	if err := f.Chmod(0o600); err != nil {
		return "", err
	}
	if _, err := f.WriteString(proxyURL + "\n"); err != nil {
		return "", err
	}
	ok = true
	return path, nil
}

func ReadProxyFile(path string) (string, error) {
	defer os.Remove(path)

	ctx, cancel := context.WithTimeout(context.Background(), ShutdownGrace)
	defer cancel()

	done := make(chan struct{})
	var body []byte
	var err error
	go func() {
		body, err = os.ReadFile(path)
		close(done)
	}()

	select {
	case <-ctx.Done():
		return "", fmt.Errorf("read proxy file %s: %w", path, ctx.Err())
	case <-done:
	}
	if err != nil {
		return "", fmt.Errorf("read proxy file %s: %w", path, err)
	}

	proxyURL := strings.TrimSpace(string(body))
	if proxyURL == "" {
		return "", fmt.Errorf("proxy file %s is empty", path)
	}
	return proxyURL, nil
}
