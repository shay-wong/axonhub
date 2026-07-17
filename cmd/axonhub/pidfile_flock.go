//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func pidFileSupported() bool {
	return true
}

func acquirePidFile(path string) (func() error, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}

	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if flockWouldBlock(err) {
			return nil, errors.New("PID file is locked by another process")
		}
		return nil, fmt.Errorf("lock PID file: %w", err)
	}

	data, err := io.ReadAll(file)
	if err != nil {
		closePidFile(file)
		return nil, fmt.Errorf("read PID file: %w", err)
	}
	if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 && pid != os.Getpid() && processExists(pid) {
		closePidFile(file)
		return nil, fmt.Errorf("PID file belongs to running process %d", pid)
	}

	if err := file.Truncate(0); err != nil {
		closePidFile(file)
		return nil, fmt.Errorf("truncate PID file: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		closePidFile(file)
		return nil, fmt.Errorf("seek PID file: %w", err)
	}
	if _, err := fmt.Fprintf(file, "%d\n", os.Getpid()); err != nil {
		closePidFile(file)
		return nil, fmt.Errorf("write PID file: %w", err)
	}
	if err := file.Sync(); err != nil {
		closePidFile(file)
		return nil, fmt.Errorf("sync PID file: %w", err)
	}

	return func() error {
		return releasePidFile(file, path)
	}, nil
}

func releasePidFile(file *os.File, path string) error {
	var removeErr error
	lockedInfo, statErr := file.Stat()
	pathInfo, pathStatErr := os.Stat(path)

	switch {
	case statErr != nil:
		removeErr = statErr
	case pathStatErr != nil && !os.IsNotExist(pathStatErr):
		removeErr = pathStatErr
	case pathStatErr == nil && os.SameFile(lockedInfo, pathInfo):
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			removeErr = err
		} else if data, err := io.ReadAll(file); err != nil {
			removeErr = err
		} else if strings.TrimSpace(string(data)) == strconv.Itoa(os.Getpid()) {
			removeErr = os.Remove(path)
		}
	}

	unlockErr := unix.Flock(int(file.Fd()), unix.LOCK_UN)
	closeErr := file.Close()
	return errors.Join(removeErr, unlockErr, closeErr)
}

func pidFileLocked(file *os.File) (bool, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_SH|unix.LOCK_NB)
	if err == nil {
		if unlockErr := unix.Flock(int(file.Fd()), unix.LOCK_UN); unlockErr != nil {
			return false, unlockErr
		}
		return false, nil
	}
	if flockWouldBlock(err) {
		return true, nil
	}
	return false, err
}

func flockWouldBlock(err error) bool {
	return errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN)
}

func closePidFile(file *os.File) {
	_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
	_ = file.Close()
}
