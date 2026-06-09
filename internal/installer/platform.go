// Package installer contains the helpers used by the `farmhand setup`,
// `farmhand update`, `farmhand uninstall`, and `farmhand doctor` subcommands.
// It is intentionally separate from `internal/config` so that the runtime
// config package stays read-only — only the installer writes farmhand.yaml.
package installer

import (
	"fmt"
	"os/exec"
	"runtime"
)

// Platform describes the host the installer is running on.
type Platform struct {
	OS   string // "linux" or "darwin"
	Arch string // "amd64" or "arm64"
}

// DetectPlatform reports the current OS/arch. It returns an error for any
// combination we don't ship a release binary for.
func DetectPlatform() (Platform, error) {
	p := Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}
	switch p.OS {
	case "linux", "darwin":
	default:
		return p, fmt.Errorf("unsupported OS %q (farmhand ships linux and darwin binaries)", p.OS)
	}
	switch p.Arch {
	case "amd64", "arm64":
	default:
		return p, fmt.Errorf("unsupported architecture %q (farmhand ships amd64 and arm64 binaries)", p.Arch)
	}
	return p, nil
}

// AssetName returns the GitHub release asset filename for this platform —
// e.g. "farmhand-darwin-arm64". Matches the names produced by
// `.github/workflows/release.yml`.
func (p Platform) AssetName() string {
	return fmt.Sprintf("farmhand-%s-%s", p.OS, p.Arch)
}

// DaemonManager reports the service manager we'll install the daemon under.
type DaemonManager string

const (
	DaemonSystemd DaemonManager = "systemd"
	DaemonLaunchd DaemonManager = "launchd"
	DaemonNone    DaemonManager = "none"
)

// DetectDaemonManager returns the canonical service manager for this platform.
// Linux → systemd (we don't currently support sysvinit / openrc / runit),
// macOS → launchd. Anything else → none.
func (p Platform) DetectDaemonManager() DaemonManager {
	switch p.OS {
	case "linux":
		if _, err := exec.LookPath("systemctl"); err == nil {
			return DaemonSystemd
		}
		return DaemonNone
	case "darwin":
		if _, err := exec.LookPath("launchctl"); err == nil {
			return DaemonLaunchd
		}
		return DaemonNone
	}
	return DaemonNone
}
