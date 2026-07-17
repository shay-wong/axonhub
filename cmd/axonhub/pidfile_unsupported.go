//go:build aix || windows

package main

import (
	"errors"
	"os"
)

var errPidFileUnsupported = errors.New("PID-file reload is not supported on this platform")

func pidFileSupported() bool {
	return false
}

func acquirePidFile(_ string) (func() error, error) {
	return nil, errPidFileUnsupported
}

func pidFileLocked(_ *os.File) (bool, error) {
	return false, errPidFileUnsupported
}
