package device

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const adbTimeout = 5 * time.Second

// ADBBridge wraps adb CLI commands via os/exec.CommandContext.
type ADBBridge struct {
	adbPath string
}

// NewADBBridge creates an ADB bridge, locating the adb binary from the given
// path or PATH. If adbPath is empty, "adb" is used and resolved via PATH.
func NewADBBridge(adbPath string) (*ADBBridge, error) {
	if adbPath == "" {
		adbPath = "adb"
	}
	resolved, err := exec.LookPath(adbPath)
	if err != nil {
		return nil, fmt.Errorf("adb binary not found at %q: %w", adbPath, err)
	}
	return &ADBBridge{adbPath: resolved}, nil
}

// Devices parses `adb devices -l` output into []Device.
// Each device gets Platform="android".
// Status mapping: "device" -> "online", "offline" -> "offline",
// "unauthorized" -> "offline".
func (b *ADBBridge) Devices() ([]Device, error) {
	ctx, cancel := context.WithTimeout(context.Background(), adbTimeout)
	defer cancel()

	out, err := b.run(ctx, "devices", "-l")
	if err != nil {
		return nil, fmt.Errorf("adb devices: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	var devices []Device
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Skip the header line and blank lines.
		if line == "" || strings.HasPrefix(line, "List of devices") {
			continue
		}
		d, ok := parseDeviceLine(line)
		if ok {
			devices = append(devices, d)
		}
	}
	return devices, nil
}

// parseDeviceLine parses a single line from `adb devices -l`.
// Format: <serial>\t<state> [key:value ...]
func parseDeviceLine(line string) (Device, bool) {
	// Split on whitespace: first field is serial, second is state, rest are key:value pairs.
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return Device{}, false
	}

	serial := fields[0]
	rawState := fields[1]

	var status string
	switch rawState {
	case "device":
		status = "online"
	case "offline", "unauthorized":
		status = "offline"
	default:
		status = "offline"
	}

	model := ""
	for _, kv := range fields[2:] {
		if strings.HasPrefix(kv, "model:") {
			model = strings.TrimPrefix(kv, "model:")
			break
		}
	}

	now := time.Now()
	return Device{
		ID:           serial,
		Platform:     PlatformAndroid,
		Model:        model,
		OSVersion:    "",
		Status:       status,
		BatteryLevel: -1,
		Tags:         []string{},
		LastSeen:     now,
		CreatedAt:    now,
	}, true
}

// Connect runs `adb connect <serial>` to re-establish a wireless connection.
// adb connect can exit 0 even on failure, so stdout is checked for "failed".
func (b *ADBBridge) Connect(serial string) error {
	ctx, cancel := context.WithTimeout(context.Background(), adbTimeout)
	defer cancel()

	out, err := b.run(ctx, "connect", serial)
	if err != nil {
		return fmt.Errorf("adb connect %s: %w", serial, err)
	}
	lower := strings.ToLower(out)
	if strings.Contains(lower, "failed") || strings.Contains(lower, "cannot") || strings.Contains(lower, "unable") {
		return fmt.Errorf("adb connect %s: %s", serial, strings.TrimSpace(out))
	}
	return nil
}

// GetProperty calls `adb -s <serial> shell getprop <prop>`.
//
// Security note: prop MUST be a hardcoded property name supplied by internal
// callers only — never pass user-supplied input as prop, as the value is
// passed directly to the shell command.
func (b *ADBBridge) GetProperty(serial, prop string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), adbTimeout)
	defer cancel()

	out, err := b.runDevice(ctx, serial, "shell", "getprop", prop)
	if err != nil {
		return "", fmt.Errorf("adb getprop %s on %s: %w", prop, serial, err)
	}
	return strings.TrimSpace(out), nil
}

// IsOnline checks if the device with the given serial is currently connected
// and in the "online" state.
func (b *ADBBridge) IsOnline(serial string) bool {
	devices, err := b.Devices()
	if err != nil {
		return false
	}
	for _, d := range devices {
		if d.ID == serial && d.Status == "online" {
			return true
		}
	}
	return false
}

// WakeDevice sends a wakeup keyevent to the device via adb.
func (b *ADBBridge) WakeDevice(serial string) error {
	ctx, cancel := context.WithTimeout(context.Background(), adbTimeout)
	defer cancel()

	_, err := b.runDevice(ctx, serial, "shell", "input", "keyevent", "KEYCODE_WAKEUP")
	if err != nil {
		return fmt.Errorf("adb wake %s: %w", serial, err)
	}
	return nil
}

// RebootDevice sends the adb reboot command to the device.
func (b *ADBBridge) RebootDevice(serial string) error {
	ctx, cancel := context.WithTimeout(context.Background(), adbTimeout)
	defer cancel()

	_, err := b.runDevice(ctx, serial, "reboot")
	if err != nil {
		return fmt.Errorf("adb reboot %s: %w", serial, err)
	}
	return nil
}

// run executes an adb command without a device selector and returns stdout.
func (b *ADBBridge) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, b.adbPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// runDevice executes an adb command with -s <serial> device selector.
func (b *ADBBridge) runDevice(ctx context.Context, serial string, args ...string) (string, error) {
	fullArgs := append([]string{"-s", serial}, args...)
	return b.run(ctx, fullArgs...)
}
