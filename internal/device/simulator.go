package device

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// simRuntimePrefix is stripped from a simctl runtime key to expose the
// "<platform>-<major>-<minor>" suffix (e.g. "iOS-26-5").
const simRuntimePrefix = "com.apple.CoreSimulator.SimRuntime."

// udidRe matches a simulator UDID (an uppercase or lowercase UUID). Used to
// decide whether a configured identifier is a UDID or a simulator name.
var udidRe = regexp.MustCompile(`^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)

// SimulatorBridge manages a configured set of iOS simulators via `xcrun simctl`.
// It boots them on startup, reports their live state for discovery, and shuts
// down (only) the ones it booted on exit.
type SimulatorBridge struct {
	xcrunPath string
	targets   []string // configured identifiers: UDIDs or simulator names
	logger    zerolog.Logger

	mu         sync.Mutex
	bootedByUs map[string]bool // UDIDs that this bridge booted (safe to shut down)
}

// NewSimulatorBridge creates a simulator bridge. Returns an error on non-macOS
// hosts or when xcrun is unavailable.
func NewSimulatorBridge(targets []string, logger zerolog.Logger) (*SimulatorBridge, error) {
	if runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("iOS simulator support requires macOS (current OS: %s)", runtime.GOOS)
	}
	path, err := exec.LookPath("xcrun")
	if err != nil {
		return nil, fmt.Errorf("xcrun not found in PATH: %w (install full Xcode)", err)
	}
	return &SimulatorBridge{
		xcrunPath:  path,
		targets:    targets,
		logger:     logger,
		bootedByUs: make(map[string]bool),
	}, nil
}

// simDevice is a normalised simulator entry derived from simctl output.
type simDevice struct {
	UDID      string
	Name      string
	State     string // "Booted", "Shutdown", ...
	OSVersion string // e.g. "26.5"
}

// simctlListOutput models `xcrun simctl list devices --json`.
type simctlListOutput struct {
	Devices map[string][]simctlDevice `json:"devices"`
}

type simctlDevice struct {
	UDID        string `json:"udid"`
	Name        string `json:"name"`
	State       string `json:"state"`
	IsAvailable bool   `json:"isAvailable"`
}

// Devices returns one Device per configured simulator, with status reflecting
// its live state (Booted -> "online", anything else -> "offline"). Each device
// is tagged "simulator". Implements the manager's simulatorDriver interface.
func (b *SimulatorBridge) Devices() ([]Device, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	all, err := b.listAll(ctx)
	if err != nil {
		return nil, err
	}

	resolved, unresolved := resolveTargets(b.targets, all)
	for _, u := range unresolved {
		b.logger.Warn().Str("identifier", u).Msg("simulator bridge: configured simulator not found")
	}

	now := time.Now().UTC()
	devices := make([]Device, 0, len(resolved))
	for _, s := range resolved {
		status := "offline"
		if s.State == "Booted" {
			status = "online"
		}
		devices = append(devices, Device{
			ID:           s.UDID,
			Platform:     PlatformIOS,
			Model:        s.Name,
			OSVersion:    s.OSVersion,
			Status:       status,
			BatteryLevel: -1,
			HardwareID:   s.UDID,
			Tags:         []string{"simulator"},
			LastSeen:     now,
			CreatedAt:    now,
		})
	}
	return devices, nil
}

// BootAll resolves the configured targets and boots each one. Per-simulator
// failures are logged and non-fatal. Safe to call once at startup.
func (b *SimulatorBridge) BootAll(ctx context.Context) {
	listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	all, err := b.listAll(listCtx)
	cancel()
	if err != nil {
		b.logger.Error().Err(err).Msg("simulator bridge: list failed; cannot boot simulators")
		return
	}

	resolved, unresolved := resolveTargets(b.targets, all)
	for _, u := range unresolved {
		b.logger.Warn().Str("identifier", u).Msg("simulator bridge: configured simulator not found; skipping boot")
	}

	for _, s := range resolved {
		bootCtx, cancelBoot := context.WithTimeout(ctx, 60*time.Second)
		bootedByUs, bootErr := b.Boot(bootCtx, s.UDID)
		cancelBoot()
		if bootErr != nil {
			b.logger.Error().Err(bootErr).Str("udid", s.UDID).Str("name", s.Name).Msg("simulator bridge: boot failed")
			continue
		}
		b.logger.Info().
			Str("udid", s.UDID).
			Str("name", s.Name).
			Bool("booted_by_us", bootedByUs).
			Msg("simulator bridge: simulator ready")
	}
}

// ShutdownAll shuts down only the simulators this bridge booted. Simulators
// that were already running (booted by the user) are left untouched.
func (b *SimulatorBridge) ShutdownAll(ctx context.Context) {
	b.mu.Lock()
	udids := make([]string, 0, len(b.bootedByUs))
	for u := range b.bootedByUs {
		udids = append(udids, u)
	}
	b.mu.Unlock()

	for _, u := range udids {
		sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := b.Shutdown(sctx, u)
		cancel()
		if err != nil {
			b.logger.Error().Err(err).Str("udid", u).Msg("simulator bridge: shutdown failed")
			continue
		}
		b.mu.Lock()
		delete(b.bootedByUs, u)
		b.mu.Unlock()
		b.logger.Info().Str("udid", u).Msg("simulator bridge: simulator shut down")
	}
}

// Boot boots the simulator with the given UDID. When the simulator is already
// booted (e.g. by the user) it is left as-is and bootedByUs is false, so it
// will not be shut down later. When this bridge boots it, bootedByUs is true.
func (b *SimulatorBridge) Boot(ctx context.Context, udid string) (bootedByUs bool, err error) {
	out, runErr := b.run(ctx, "boot", udid)
	if runErr != nil {
		if strings.Contains(strings.ToLower(out), "current state: booted") {
			return false, nil // already booted — not ours to shut down
		}
		return false, fmt.Errorf("simctl boot %s: %w: %s", udid, runErr, strings.TrimSpace(out))
	}
	b.mu.Lock()
	b.bootedByUs[udid] = true
	b.mu.Unlock()
	return true, nil
}

// Shutdown shuts down the simulator with the given UDID. A simulator that is
// already shut down is treated as success.
func (b *SimulatorBridge) Shutdown(ctx context.Context, udid string) error {
	out, err := b.run(ctx, "shutdown", udid)
	if err != nil {
		if strings.Contains(strings.ToLower(out), "current state: shutdown") {
			return nil
		}
		return fmt.Errorf("simctl shutdown %s: %w: %s", udid, err, strings.TrimSpace(out))
	}
	return nil
}

// listAll runs `simctl list devices available --json` and parses the result
// into iOS simulators only.
func (b *SimulatorBridge) listAll(ctx context.Context) ([]simDevice, error) {
	out, err := b.run(ctx, "list", "devices", "available", "--json")
	if err != nil {
		return nil, fmt.Errorf("simctl list devices: %w: %s", err, strings.TrimSpace(out))
	}
	return parseSimctlList(out)
}

// run executes `xcrun simctl <args...>` and returns combined stdout+stderr.
// Combined output is needed so callers can inspect simctl's state messages
// (e.g. "current state: Booted") which it writes to stderr.
func (b *SimulatorBridge) run(ctx context.Context, args ...string) (string, error) {
	full := append([]string{"simctl"}, args...)
	//nolint:gosec // xcrunPath is resolved via exec.LookPath; simctl args are not passed through a shell
	cmd := exec.CommandContext(ctx, b.xcrunPath, full...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// parseSimctlList parses `simctl list devices --json` output into iOS
// simulators. Non-iOS runtimes (tvOS/watchOS/visionOS) and unavailable
// devices are skipped.
func parseSimctlList(jsonOut string) ([]simDevice, error) {
	var out simctlListOutput
	if err := json.Unmarshal([]byte(jsonOut), &out); err != nil {
		return nil, fmt.Errorf("parse simctl json: %w", err)
	}

	var devices []simDevice
	for runtimeKey, devs := range out.Devices {
		version, ok := iosRuntimeVersion(runtimeKey)
		if !ok {
			continue // non-iOS runtime
		}
		for _, d := range devs {
			if !d.IsAvailable {
				continue
			}
			devices = append(devices, simDevice{
				UDID:      d.UDID,
				Name:      d.Name,
				State:     d.State,
				OSVersion: version,
			})
		}
	}
	return devices, nil
}

// iosRuntimeVersion extracts the iOS version from a simctl runtime key.
// "com.apple.CoreSimulator.SimRuntime.iOS-26-5" -> ("26.5", true).
// Returns ("", false) for non-iOS runtimes or unrecognised keys.
func iosRuntimeVersion(key string) (string, bool) {
	rest := strings.TrimPrefix(key, simRuntimePrefix)
	if rest == key {
		return "", false // prefix absent
	}
	platform, ver, found := strings.Cut(rest, "-")
	if !found || !strings.EqualFold(platform, "iOS") {
		return "", false
	}
	return strings.ReplaceAll(ver, "-", "."), true
}

// resolveTargets maps each configured identifier (UDID or simulator name) to a
// concrete simulator from all. UDID-shaped identifiers match by UDID; others
// match by name (first match wins on duplicates). Identifiers with no match are
// returned in unresolved for the caller to log.
func resolveTargets(targets []string, all []simDevice) (resolved []simDevice, unresolved []string) {
	byUDID := make(map[string]simDevice, len(all))
	byName := make(map[string][]simDevice, len(all))
	for _, d := range all {
		byUDID[d.UDID] = d
		byName[d.Name] = append(byName[d.Name], d)
	}

	for _, t := range targets {
		if isUDID(t) {
			if d, ok := byUDID[t]; ok {
				resolved = append(resolved, d)
			} else {
				unresolved = append(unresolved, t)
			}
			continue
		}
		if matches := byName[t]; len(matches) > 0 {
			resolved = append(resolved, matches[0])
		} else {
			unresolved = append(unresolved, t)
		}
	}
	return resolved, unresolved
}

// isUDID reports whether s is shaped like a simulator UDID (a UUID).
func isUDID(s string) bool {
	return udidRe.MatchString(s)
}
