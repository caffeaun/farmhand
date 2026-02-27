package device

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// deviceLineRe matches physical device lines from xcrun xctrace list devices output.
// It anchors to the last two parenthetical groups on the line:
//   - second-to-last group: OS version (e.g. "17.2", "16.7.2")
//   - last group: UDID (alphanumeric + hyphens, e.g. "00008101-001A34561234001E")
//
// The device name is everything before those two groups and may itself contain
// parentheticals, e.g. "iPad Air (5th generation) (16.7.2) (00008027-XXXX)".
// The UDID group uses [^\)]+ to capture any characters except a closing paren,
// keeping the pattern permissive for all real UDID formats.
var deviceLineRe = regexp.MustCompile(`^(.*\S)\s+\(([^)]+)\)\s+\(([^)]+)\)\s*$`)

// IOSBridge wraps xcrun CLI tooling for iOS device management.
type IOSBridge struct {
	xcrunPath string
}

// NewIOSBridge creates an iOS bridge. Returns an error on non-macOS hosts.
func NewIOSBridge() (*IOSBridge, error) {
	if runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("iOS device support requires macOS (current OS: %s)", runtime.GOOS)
	}

	path, err := exec.LookPath("xcrun")
	if err != nil {
		return nil, fmt.Errorf("xcrun not found in PATH: %w (install Xcode Command Line Tools: xcode-select --install)", err)
	}

	return &IOSBridge{xcrunPath: path}, nil
}

// Devices parses `xcrun xctrace list devices` output into []Device.
// Each device gets Platform="ios". Only physical devices are returned;
// simulator entries are skipped.
func (b *IOSBridge) Devices() ([]Device, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	//nolint:gosec // xcrunPath is resolved via exec.LookPath; args are static literals
	cmd := exec.CommandContext(ctx, b.xcrunPath, "xctrace", "list", "devices")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("xcrun xctrace list devices failed: %w", err)
	}

	return parseXCTraceOutput(string(out)), nil
}

// IsOnline checks if a device with the given UDID is currently visible.
func (b *IOSBridge) IsOnline(udid string) bool {
	devices, err := b.Devices()
	if err != nil {
		return false
	}
	for _, d := range devices {
		if d.ID == udid {
			return true
		}
	}
	return false
}

// parseXCTraceOutput parses the text output of `xcrun xctrace list devices`
// and returns a slice of Device for physical (non-simulator) iOS devices.
//
// Expected output structure:
//
//	== Devices ==
//	Mac mini (00000000-0000-0000-0000-000000000000)
//	iPhone 15 Pro (17.2) (00008101-001A34561234001E)
//
//	== Simulators ==
//	iPhone 15 Simulator (17.2) (A1B2C3D4-...)
func parseXCTraceOutput(output string) []Device {
	var devices []Device
	inDevices := false

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Section headers
		if line == "== Devices ==" {
			inDevices = true
			continue
		}
		if strings.HasPrefix(line, "== ") && strings.HasSuffix(line, " ==") {
			// Any other section header (e.g., "== Simulators ==") ends the device section
			inDevices = false
			continue
		}

		if !inDevices || line == "" {
			continue
		}

		m := deviceLineRe.FindStringSubmatch(line)
		if m == nil {
			// Line doesn't match "Name (Version) (UDID)" — skip (e.g. Mac host line)
			continue
		}

		name := strings.TrimSpace(m[1])
		version := strings.TrimSpace(m[2])
		udid := strings.TrimSpace(m[3])

		// Mac host entries have no OS version in the first parenthetical —
		// they use a UUID-style UDID directly: "Mac mini (00000000-0000-0000-0000-000000000000)"
		// Those are caught by the regex only if they match Name (UDID) with no version group,
		// but our regex requires two () groups. A Mac line has only one group and won't match.
		// Still, guard against entries where "version" looks like a bare UDID.
		if looksLikeUDID(version) {
			// This is a Name (UDID) line with no OS version — it's the Mac host, skip it.
			continue
		}

		devices = append(devices, Device{
			ID:        udid,
			Platform:  PlatformIOS,
			Model:     name,
			OSVersion: version,
			Status:    "online",
			LastSeen:  time.Now().UTC(),
		})
	}

	return devices
}

// looksLikeUDID returns true when s is a bare UDID (no dots → not an OS version string).
// OS version strings contain dots (e.g. "17.2", "16.7.2").
// UDIDs are hex strings with hyphens and no dots.
func looksLikeUDID(s string) bool {
	return !strings.Contains(s, ".")
}
