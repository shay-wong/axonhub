//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/fx"

	"github.com/looplj/axonhub/conf"
	"github.com/looplj/axonhub/internal/server/middleware"
)

const (
	configReloadSIGHUPChild = "AXONHUB_TEST_SIGHUP_CHILD"
	configReloadPIDFile     = "AXONHUB_TEST_PID_FILE"
)

type configReloadTestLifecycle struct {
	hook fx.Hook
}

func (l *configReloadTestLifecycle) Append(hook fx.Hook) {
	l.hook = hook
}

func TestReloadIPAccessControl(t *testing.T) {
	loader, runtime, configFile := newIPAccessControlReloadFixture(t, "192.0.2.1")

	writeReloadConfig(t, configFile, "203.0.113.0/24")
	if err := reloadIPAccessControl(context.Background(), loader, runtime); err != nil {
		t.Fatalf("reloadIPAccessControl() error = %v", err)
	}

	if ipAccessAllowed(t, runtime, "192.0.2.1") {
		t.Fatal("old IP should not remain allowed after a successful reload")
	}
	if !ipAccessAllowed(t, runtime, "203.0.113.10") {
		t.Fatal("new CIDR should be allowed after a successful reload")
	}
}

func TestReloadIPAccessControlKeepsPreviousStateOnInvalidConfig(t *testing.T) {
	loader, runtime, configFile := newIPAccessControlReloadFixture(t, "192.0.2.1")

	writeReloadConfig(t, configFile, "not-an-ip")
	if err := reloadIPAccessControl(context.Background(), loader, runtime); err == nil {
		t.Fatal("reloadIPAccessControl() error = nil, want invalid IP error")
	}

	if !ipAccessAllowed(t, runtime, "192.0.2.1") {
		t.Fatal("invalid reload must leave the previous IP rules active")
	}
}

func TestRegisterConfigReloadHandlesSIGHUPWithoutConfigFile(t *testing.T) {
	const readyMarker = "config-reload-ready"

	if os.Getenv(configReloadSIGHUPChild) == "1" {
		if pidFileSupported() {
			release, err := acquirePidFile(os.Getenv(configReloadPIDFile))
			if err != nil {
				os.Exit(2)
			}
			defer release()
		}

		runtime, err := middleware.NewIPAccessControlConfig(false, nil, "")
		if err != nil {
			os.Exit(2)
		}
		lifecycle := &configReloadTestLifecycle{}
		registerConfigReload(lifecycle, &conf.Loader{}, runtime)
		if lifecycle.hook.OnStart == nil || lifecycle.hook.OnStart(context.Background()) != nil {
			os.Exit(2)
		}
		fmt.Fprintln(os.Stdout, readyMarker)
		select {}
	}

	pidFile := filepath.Join(t.TempDir(), "axonhub.pid")
	cmd := exec.Command(os.Args[0], "-test.run=^TestRegisterConfigReloadHandlesSIGHUPWithoutConfigFile$")
	cmd.Env = append(os.Environ(), configReloadSIGHUPChild+"=1", configReloadPIDFile+"="+pidFile)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe() error = %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	ready := false
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if scanner.Text() == readyMarker {
			ready = true
			break
		}
	}
	if !ready {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("child did not become ready: %v", scanner.Err())
	}

	exited := make(chan error, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				fmt.Fprintf(os.Stderr, "wait for SIGHUP test child panicked: %v\n", recovered)
				exited <- fmt.Errorf("wait for child panicked: %v", recovered)
			}
		}()
		exited <- cmd.Wait()
	}()
	finished := false
	t.Cleanup(func() {
		if finished {
			return
		}
		_ = cmd.Process.Kill()
		<-exited
	})
	if pidFileSupported() {
		if err := reloadRunningServer(pidFile); err != nil {
			t.Fatalf("reloadRunningServer() error = %v", err)
		}
	} else if err := cmd.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatalf("send SIGHUP: %v", err)
	}

	select {
	case err := <-exited:
		finished = true
		t.Fatalf("child exited after SIGHUP: %v", err)
	case <-time.After(250 * time.Millisecond):
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill child: %v", err)
	}
	<-exited
	finished = true
}

func TestAcquirePidFileRejectsLiveOwner(t *testing.T) {
	requirePidFileSupport(t)

	pidFile := filepath.Join(t.TempDir(), "axonhub.pid")
	original := strconv.Itoa(os.Getppid()) + "\n"
	if err := os.WriteFile(pidFile, []byte(original), 0o600); err != nil {
		t.Fatalf("write existing PID file: %v", err)
	}

	if _, err := acquirePidFile(pidFile); err == nil {
		t.Fatal("acquirePidFile() error = nil, want live owner error")
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read existing PID file: %v", err)
	}
	if string(data) != original {
		t.Fatalf("PID file = %q, want unchanged %q", data, original)
	}
}

func TestAcquirePidFileReplacesStaleOwner(t *testing.T) {
	requirePidFileSupport(t)

	const stalePID = 1 << 30
	if processExists(stalePID) {
		t.Skip("selected stale PID unexpectedly exists")
	}

	pidFile := filepath.Join(t.TempDir(), "axonhub.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(stalePID)+"\n"), 0o600); err != nil {
		t.Fatalf("write stale PID file: %v", err)
	}
	release, err := acquirePidFile(pidFile)
	if err != nil {
		t.Fatalf("acquirePidFile() error = %v", err)
	}

	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read replacement PID file: %v", err)
	}
	want := strconv.Itoa(os.Getpid()) + "\n"
	if string(data) != want {
		t.Fatalf("PID file = %q, want %q", data, want)
	}
	if err := release(); err != nil {
		t.Fatalf("release PID file: %v", err)
	}
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Fatalf("released PID file still exists: %v", err)
	}
}

func TestAcquirePidFileSerializesOwners(t *testing.T) {
	requirePidFileSupport(t)

	pidFile := filepath.Join(t.TempDir(), "axonhub.pid")
	firstRelease, err := acquirePidFile(pidFile)
	if err != nil {
		t.Fatalf("first acquirePidFile() error = %v", err)
	}
	if _, err := acquirePidFile(pidFile); err == nil {
		t.Fatal("second acquirePidFile() error = nil, want lock error")
	}
	if err := firstRelease(); err != nil {
		t.Fatalf("first release PID file: %v", err)
	}

	secondRelease, err := acquirePidFile(pidFile)
	if err != nil {
		t.Fatalf("acquire after release error = %v", err)
	}
	if err := secondRelease(); err != nil {
		t.Fatalf("second release PID file: %v", err)
	}
}

func TestReleasePidFilePreservesReplacedPath(t *testing.T) {
	requirePidFileSupport(t)

	pidFile := filepath.Join(t.TempDir(), "axonhub.pid")
	release, err := acquirePidFile(pidFile)
	if err != nil {
		t.Fatalf("acquirePidFile() error = %v", err)
	}

	if err := os.Remove(pidFile); err != nil {
		t.Fatalf("remove owned path: %v", err)
	}
	foreign := strconv.Itoa(os.Getpid()+1) + "\n"
	if err := os.WriteFile(pidFile, []byte(foreign), 0o600); err != nil {
		t.Fatalf("write replacement PID file: %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("release replaced PID file: %v", err)
	}

	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("replacement PID file should remain: %v", err)
	}
	if string(data) != foreign {
		t.Fatalf("replacement PID file = %q, want %q", data, foreign)
	}
}

func TestReloadRunningServerRejectsNonPositivePID(t *testing.T) {
	requirePidFileSupport(t)

	for _, pid := range []string{"0", "-2"} {
		t.Run(pid, func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "axonhub.pid")
			if err := os.WriteFile(pidFile, []byte(pid+"\n"), 0o600); err != nil {
				t.Fatalf("write PID file: %v", err)
			}
			if err := reloadRunningServer(pidFile); err == nil {
				t.Fatal("reloadRunningServer() error = nil, want invalid PID error")
			} else if !strings.Contains(err.Error(), "PID must be positive") {
				t.Fatalf("reloadRunningServer() error = %v, want positive PID validation", err)
			}
		})
	}
}

func TestReloadRunningServerRejectsUnlockedPidFile(t *testing.T) {
	requirePidFileSupport(t)

	pidFile := filepath.Join(t.TempDir(), "axonhub.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatalf("write PID file: %v", err)
	}
	if err := reloadRunningServer(pidFile); err == nil {
		t.Fatal("reloadRunningServer() error = nil, want unlocked PID file error")
	}
}

func requirePidFileSupport(t *testing.T) {
	t.Helper()
	if !pidFileSupported() {
		t.Skip("PID-file reload is not supported on this platform")
	}
}

func newIPAccessControlReloadFixture(t *testing.T, allowedIP string) (*conf.Loader, *middleware.IPAccessControlConfig, string) {
	t.Helper()

	configDir := t.TempDir()
	t.Chdir(configDir)

	configFile := filepath.Join(configDir, "config.yml")
	writeReloadConfig(t, configFile, allowedIP)

	initial, loader, err := conf.NewLoader()
	if err != nil {
		t.Fatalf("NewLoader() error = %v", err)
	}
	runtime, err := middleware.NewIPAccessControlConfig(
		initial.APIServer.IPAccessControl.Enabled,
		initial.APIServer.IPAccessControl.AllowedIPs,
		initial.APIServer.IPAccessControl.RedirectURL,
	)
	if err != nil {
		t.Fatalf("NewIPAccessControlConfig() error = %v", err)
	}

	return loader, runtime, configFile
}

func writeReloadConfig(t *testing.T, configFile string, allowedIP string) {
	t.Helper()

	contents := "server:\n  ip_access_control:\n    enabled: true\n    allowed_ips:\n      - " + allowedIP + "\n"
	if err := os.WriteFile(configFile, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func ipAccessAllowed(t *testing.T, config *middleware.IPAccessControlConfig, clientIP string) bool {
	t.Helper()
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	ctx, engine := gin.CreateTestContext(recorder)
	if err := engine.SetTrustedProxies(nil); err != nil {
		t.Fatalf("set trusted proxies: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = clientIP + ":12345"
	ctx.Request = request

	middleware.WithIPAccessControl(config)(ctx)
	return recorder.Code == http.StatusOK
}
