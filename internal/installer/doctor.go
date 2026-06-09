package installer

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/caffeaun/farmhand/internal/config"
)

// CheckResult is one row of `farmhand doctor` output.
type CheckResult struct {
	Name   string
	OK     bool
	Detail string // human-readable status (one line)
}

// RunDoctor inspects an existing install and returns a list of checks. It
// never panics — every failure becomes a CheckResult with OK=false.
func RunDoctor(layout Layout, cfg *config.Config) []CheckResult {
	checks := []CheckResult{}

	// 1. Binary
	checks = append(checks, checkBinary(layout.BinaryPath))

	// 2. Config file exists + is loadable
	checks = append(checks, checkConfigFile(layout.ConfigPath))

	// 3. Daemon active
	checks = append(checks, checkDaemon())

	// 4. /api/v1/health reachable
	if cfg != nil {
		checks = append(checks, checkHealth(cfg.Server.Host, cfg.Server.Port))
	}

	// 5. Runtime dirs writable
	for _, dir := range layout.RuntimeDirs() {
		checks = append(checks, checkDir(dir))
	}

	// 6. adb on PATH
	checks = append(checks, checkExecutable("adb"))

	// 7. macOS-only: full Xcode for xctrace (physical iOS) — warn-only
	if runtime.GOOS == "darwin" {
		checks = append(checks, checkXctrace())
	}

	return checks
}

func checkBinary(path string) CheckResult {
	if _, err := os.Stat(path); err != nil {
		return CheckResult{Name: "binary", OK: false, Detail: fmt.Sprintf("%s: %v", path, err)}
	}
	return CheckResult{Name: "binary", OK: true, Detail: path}
}

func checkConfigFile(path string) CheckResult {
	if _, err := config.Load(path); err != nil {
		return CheckResult{Name: "config", OK: false, Detail: fmt.Sprintf("%s: %v", path, err)}
	}
	return CheckResult{Name: "config", OK: true, Detail: path}
}

func checkDaemon() CheckResult {
	plat, _ := DetectPlatform()
	switch plat.DetectDaemonManager() {
	case DaemonSystemd:
		active, raw, err := SystemdStatus()
		if err != nil {
			return CheckResult{Name: "daemon", OK: false, Detail: err.Error()}
		}
		return CheckResult{Name: "daemon", OK: active, Detail: "systemd: " + raw}
	case DaemonLaunchd:
		active, _, err := LaunchdStatus()
		if err != nil {
			return CheckResult{Name: "daemon", OK: false, Detail: err.Error()}
		}
		state := "loaded but no PID"
		if active {
			state = "running"
		}
		return CheckResult{Name: "daemon", OK: active, Detail: "launchd: " + state}
	}
	return CheckResult{Name: "daemon", OK: false, Detail: "no service manager detected"}
}

func checkHealth(host string, port int) CheckResult {
	if host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	url := fmt.Sprintf("http://%s:%d/api/v1/health", host, port)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return CheckResult{Name: "health endpoint", OK: false, Detail: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return CheckResult{Name: "health endpoint", OK: false, Detail: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	return CheckResult{Name: "health endpoint", OK: true, Detail: url}
}

func checkDir(path string) CheckResult {
	info, err := os.Stat(path)
	if err != nil {
		return CheckResult{Name: "dir " + path, OK: false, Detail: err.Error()}
	}
	if !info.IsDir() {
		return CheckResult{Name: "dir " + path, OK: false, Detail: "not a directory"}
	}
	return CheckResult{Name: "dir " + path, OK: true, Detail: "exists"}
}

func checkExecutable(name string) CheckResult {
	path, err := exec.LookPath(name)
	if err != nil {
		return CheckResult{Name: name + " on PATH", OK: false, Detail: "not found (Android device support requires adb)"}
	}
	return CheckResult{Name: name + " on PATH", OK: true, Detail: path}
}

func checkXctrace() CheckResult {
	out, err := exec.Command("xcrun", "--find", "xctrace").CombinedOutput()
	if err != nil {
		return CheckResult{Name: "xctrace (full Xcode)", OK: false, Detail: "not available — install Xcode.app from the App Store for physical iOS support"}
	}
	return CheckResult{Name: "xctrace (full Xcode)", OK: true, Detail: strings.TrimSpace(string(out))}
}
