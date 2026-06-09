package installer

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/template"

	"github.com/caffeaun/farmhand/internal/installer/templates"
)

const (
	// systemdUnitPath is the canonical location of the FarmHand systemd unit.
	systemdUnitPath = "/etc/systemd/system/farmhand.service"
	// daemonUser is the service account the unit runs under. Matches the
	// `useradd -r farmhand` documented in docs/use-cases/01-ubuntu-with-flutter-android.md.
	daemonUser = "farmhand"
)

// systemdRenderContext is the template payload for farmhand.service.tmpl.
type systemdRenderContext struct {
	User         string
	InstallDir   string
	BinaryPath   string
	ConfigPath   string
}

// RenderSystemdUnit returns the contents of /etc/systemd/system/farmhand.service.
func RenderSystemdUnit(layout Layout) ([]byte, error) {
	tmpl, err := template.ParseFS(templates.FS, "farmhand.service.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parse systemd template: %w", err)
	}
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, systemdRenderContext{
		User:       daemonUser,
		InstallDir: layout.InstallDir,
		BinaryPath: layout.BinaryPath,
		ConfigPath: layout.ConfigPath,
	})
	if err != nil {
		return nil, fmt.Errorf("render systemd unit: %w", err)
	}
	return buf.Bytes(), nil
}

// InstallSystemd writes the unit file, ensures the `farmhand` service user
// exists (creating it via `useradd -r` if not), chowns the install dirs to
// that user, then reloads systemd. It does NOT enable or start the unit —
// callers do that explicitly with StartSystemd so dry-runs are possible.
//
// Requires root.
func InstallSystemd(layout Layout) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("installing the systemd unit requires root (re-run with sudo)")
	}

	body, err := RenderSystemdUnit(layout)
	if err != nil {
		return err
	}
	if err := os.WriteFile(systemdUnitPath, body, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", systemdUnitPath, err)
	}

	if err := ensureSystemUser(daemonUser); err != nil {
		return err
	}
	for _, dir := range layout.RuntimeDirs() {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
		if err := chownRecursive(dir, daemonUser); err != nil {
			return err
		}
	}

	if out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// StartSystemd enables and starts the farmhand service.
func StartSystemd() error {
	out, err := exec.Command("systemctl", "enable", "--now", "farmhand").CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl enable --now farmhand: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RestartSystemd restarts the farmhand service. Safe to call after a binary
// swap during `farmhand update`.
func RestartSystemd() error {
	out, err := exec.Command("systemctl", "restart", "farmhand").CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl restart farmhand: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// StopSystemd stops the farmhand service. Used by `farmhand uninstall`.
func StopSystemd() error {
	// `systemctl stop` returns non-zero if the unit isn't loaded; treat that
	// as success since the caller's intent is "make sure it's stopped".
	out, err := exec.Command("systemctl", "stop", "farmhand").CombinedOutput()
	if err != nil && !strings.Contains(string(out), "not loaded") {
		return fmt.Errorf("systemctl stop farmhand: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RemoveSystemd disables the service and removes the unit file. Does NOT
// remove the daemonUser or the install dir.
func RemoveSystemd() error {
	_ = exec.Command("systemctl", "disable", "farmhand").Run()
	if err := os.Remove(systemdUnitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", systemdUnitPath, err)
	}
	if out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// SystemdStatus reports whether the unit is active. Used by `farmhand doctor`.
func SystemdStatus() (active bool, raw string, err error) {
	out, runErr := exec.Command("systemctl", "is-active", "farmhand").CombinedOutput()
	raw = strings.TrimSpace(string(out))
	// `is-active` exit code is non-zero when inactive — that's not an error
	// for our purposes, only "couldn't even run systemctl" is.
	if runErr != nil {
		if _, ok := runErr.(*exec.ExitError); !ok {
			return false, raw, fmt.Errorf("systemctl is-active: %w", runErr)
		}
	}
	return raw == "active", raw, nil
}

// ensureSystemUser creates a system user `name` if it doesn't exist already.
func ensureSystemUser(name string) error {
	if err := exec.Command("id", name).Run(); err == nil {
		return nil
	}
	out, err := exec.Command("useradd", "-r", "-s", "/usr/sbin/nologin", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("useradd -r %s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// chownRecursive sets owner:group on path and everything under it.
func chownRecursive(path, ownerGroup string) error {
	out, err := exec.Command("chown", "-R", ownerGroup+":"+ownerGroup, path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("chown %s: %w: %s", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}
