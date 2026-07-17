//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
)

func reloadRunningServer(pidFile string) error {
	file, err := os.Open(pidFile)
	if err != nil {
		return fmt.Errorf("read PID file %s: %w", pidFile, err)
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("read PID file %s: %w", pidFile, err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return fmt.Errorf("invalid PID in %s: %w", pidFile, err)
	}
	if pid <= 0 {
		return fmt.Errorf("invalid PID in %s: PID must be positive", pidFile)
	}

	locked, err := pidFileLocked(file)
	if err != nil {
		return fmt.Errorf("check PID file lock %s: %w", pidFile, err)
	}
	if !locked {
		return fmt.Errorf("PID file %s is not owned by a running AxonHub process", pidFile)
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process %d: %w", pid, err)
	}

	if err := process.Signal(syscall.SIGHUP); err != nil {
		return fmt.Errorf("send SIGHUP to process %d: %w", pid, err)
	}

	return nil
}

func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
