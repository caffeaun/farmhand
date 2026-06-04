package device

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const adbTimeout = 5 * time.Second

// inputTimeout is the default budget for `input tap|swipe|keyevent|text`
// commands; it is longer than adbTimeout because `input` can block while
// the device dispatches the event (notably swipe, which blocks for its
// full duration).
const inputTimeout = 15 * time.Second

// keycodePattern accepts symbolic keycodes that the Android `input` utility
// understands, e.g. KEYCODE_BACK, KEYCODE_VOLUME_UP, KEYCODE_DPAD_DOWN.
var keycodePattern = regexp.MustCompile(`^KEYCODE_[A-Z0-9_]+$`)

// packageIDPattern accepts Android package identifiers: a lowercase letter
// followed by alnum/underscore segments separated by dots, e.g.
// `com.example.app`. Validated before shelling out so the package id cannot
// inject extra adb-shell arguments.
var packageIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z0-9_]+)+$`)

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

// Tap dispatches a single tap event at (x, y) on the device.
func (b *ADBBridge) Tap(serial string, x, y int) error {
	if x < 0 || y < 0 {
		return fmt.Errorf("invalid tap coordinates (%d,%d): must be non-negative", x, y)
	}
	ctx, cancel := context.WithTimeout(context.Background(), inputTimeout)
	defer cancel()

	_, err := b.runDevice(ctx, serial, "shell", "input", "tap", strconv.Itoa(x), strconv.Itoa(y))
	if err != nil {
		return fmt.Errorf("adb tap %s (%d,%d): %w", serial, x, y, err)
	}
	return nil
}

// Swipe dispatches a swipe gesture from (x1, y1) to (x2, y2). When
// durationMs > 0 it is passed to `input swipe` as the gesture duration;
// 0 omits the argument so adb uses the device default (~150ms).
//
// The CommandContext deadline scales with durationMs because `input swipe`
// blocks for the full duration before exiting.
func (b *ADBBridge) Swipe(serial string, x1, y1, x2, y2, durationMs int) error {
	if x1 < 0 || y1 < 0 || x2 < 0 || y2 < 0 {
		return fmt.Errorf("invalid swipe coordinates: must be non-negative")
	}
	if durationMs < 0 {
		return fmt.Errorf("invalid swipe duration_ms %d: must be non-negative", durationMs)
	}

	timeout := inputTimeout
	if dur := time.Duration(durationMs) * time.Millisecond; dur > timeout {
		timeout = dur + 2*time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	args := []string{"shell", "input", "swipe",
		strconv.Itoa(x1), strconv.Itoa(y1),
		strconv.Itoa(x2), strconv.Itoa(y2),
	}
	if durationMs > 0 {
		args = append(args, strconv.Itoa(durationMs))
	}
	if _, err := b.runDevice(ctx, serial, args...); err != nil {
		return fmt.Errorf("adb swipe %s (%d,%d)->(%d,%d) dur=%dms: %w", serial, x1, y1, x2, y2, durationMs, err)
	}
	return nil
}

// KeyEvent dispatches a single keyevent to the device. The keycode must be
// either a non-negative integer or a symbolic KEYCODE_X name; arbitrary
// strings are rejected before they reach adb to prevent device-shell
// metacharacters from being interpreted.
func (b *ADBBridge) KeyEvent(serial, keycode string) error {
	if keycode == "" {
		return fmt.Errorf("invalid keycode: empty")
	}
	if _, err := strconv.ParseUint(keycode, 10, 32); err != nil {
		if !keycodePattern.MatchString(keycode) {
			return fmt.Errorf("invalid keycode %q: must be a non-negative integer or match ^KEYCODE_[A-Z0-9_]+$", keycode)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), inputTimeout)
	defer cancel()

	if _, err := b.runDevice(ctx, serial, "shell", "input", "keyevent", keycode); err != nil {
		return fmt.Errorf("adb keyevent %s %s: %w", serial, keycode, err)
	}
	return nil
}

// InputText types the given text on the device. `adb shell input text`
// is sensitive to device-shell metacharacters in text because adb
// concatenates extra args and runs them through the device shell;
// we instead pass one shell-quoted argument (`input text '<escaped>'`)
// so the device shell treats text strictly as data.
func (b *ADBBridge) InputText(serial, text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), inputTimeout)
	defer cancel()

	cmd := "input text " + quoteForDeviceShell(text)
	if _, err := b.runDevice(ctx, serial, "shell", cmd); err != nil {
		return fmt.Errorf("adb input text %s: %w", serial, err)
	}
	return nil
}

// KillAllApps closes every background app on the device. Implemented as
// `am kill-all`, which asks ActivityManager to terminate all user processes
// that are not currently foreground. Foreground activities (the launcher,
// any visible app) are not killed by this command; callers that want a
// strict "back to launcher" state should pair KillAllApps with a
// KEYCODE_HOME KeyEvent.
func (b *ADBBridge) KillAllApps(serial string) error {
	ctx, cancel := context.WithTimeout(context.Background(), inputTimeout)
	defer cancel()
	if _, err := b.runDevice(ctx, serial, "shell", "am", "kill-all"); err != nil {
		return fmt.Errorf("adb am kill-all %s: %w", serial, err)
	}
	return nil
}

// Launch starts the main launcher activity of the given Android package
// using `am start --pn <pkg>`. The package id is validated against
// packageIDPattern before reaching the device shell so it cannot smuggle
// extra args. Requires Android 10+ for the --pn flag.
func (b *ADBBridge) Launch(serial, pkg string) error {
	if !packageIDPattern.MatchString(pkg) {
		return fmt.Errorf("invalid package id %q: must match %s", pkg, packageIDPattern)
	}
	ctx, cancel := context.WithTimeout(context.Background(), inputTimeout)
	defer cancel()
	out, err := b.runDevice(ctx, serial, "shell", "am", "start", "--pn", pkg)
	if err != nil {
		return fmt.Errorf("adb am start --pn %s on %s: %w", pkg, serial, err)
	}
	// `am start` exits 0 even when the package is unknown; the failure is
	// in stdout/stderr. Surface those well-known failure markers as errors
	// so the CLI returns non-zero on "no main activity for the package".
	if strings.Contains(out, "Error:") || strings.Contains(out, "does not exist") {
		return fmt.Errorf("adb am start --pn %s on %s: %s", pkg, serial, strings.TrimSpace(out))
	}
	return nil
}

// quoteForDeviceShell wraps s in single quotes for safe inclusion in a
// device-shell command line. Embedded single quotes are escaped using the
// standard sh-portable sequence: close the quoted run, emit one escaped
// quote, then reopen the quoted run (`'\''`).
func quoteForDeviceShell(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// screenshotTimeout is the budget for `exec-out screencap -p`. Screencap on
// modern devices completes in well under a second; the buffer accounts for
// slow USB, congested wireless adb, or a device under load.
const screenshotTimeout = 15 * time.Second

// Screenshot returns a PNG of the device's current screen. Internally runs
// `adb -s <serial> exec-out screencap -p`, which streams a raw PNG to
// stdout.
func (b *ADBBridge) Screenshot(serial string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), screenshotTimeout)
	defer cancel()

	out, err := b.runDeviceRaw(ctx, serial, "exec-out", "screencap", "-p")
	if err != nil {
		return nil, fmt.Errorf("adb screencap %s: %w", serial, err)
	}
	return out, nil
}

// LogcatOptions selects which slice of the device's logcat ring buffer to
// dump. The zero value asks adb for the full buffer (which can be large —
// set Since to bound it).
type LogcatOptions struct {
	// Since, if non-zero, requests only entries newer than this many time
	// units ago. The unit is the most natural for adb: minutes for >= 1m,
	// seconds otherwise. Sub-second values are rounded up to 1s.
	Since time.Duration

	// Filter, if non-empty, becomes the priority filter passed as the
	// trailing positional arg to logcat. Accepts the standard adb prefixes
	// "V", "D", "I", "W", "E", "F", "S" (verbose…silent), e.g. "E" filters
	// to error and fatal only.
	Filter string
}

// logcatTimeout is the budget for a single non-streaming `logcat -d|-t N`
// invocation. Default buffer dumps are typically small (a few hundred KB)
// but a heavily-logging device can return more; keep the budget generous.
const logcatTimeout = 30 * time.Second

// Logcat dumps the device's logcat ring buffer as raw bytes (newline-
// terminated UTF-8 lines, exactly as adb writes them). Streaming logcat
// (`logcat -f` / `-T`) is intentionally out of scope; callers that want
// a live tail should poll on a cadence and diff.
//
// The filter is validated against a small allow-list so the value cannot
// inject device-shell tokens — important because logcat passes the trailing
// positional through to the device shell.
func (b *ADBBridge) Logcat(serial string, opts LogcatOptions) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), logcatTimeout)
	defer cancel()

	args := []string{"logcat", "-d"}
	if opts.Since > 0 {
		// adb's logcat -t accepts either a count of lines or a time of the
		// form "M:SS" / "HH:MM:SS.mmm" / "Nm"/"Ns". The simplest portable
		// form is `Nm` for minutes and `Ns` for seconds.
		var arg string
		if opts.Since >= time.Minute {
			minutes := int(opts.Since / time.Minute)
			if minutes < 1 {
				minutes = 1
			}
			arg = fmt.Sprintf("%dm", minutes)
		} else {
			seconds := int(opts.Since / time.Second)
			if seconds < 1 {
				seconds = 1
			}
			arg = fmt.Sprintf("%ds", seconds)
		}
		args = append(args, "-t", arg)
	}
	if opts.Filter != "" {
		switch opts.Filter {
		case "V", "D", "I", "W", "E", "F", "S":
			args = append(args, "*:"+opts.Filter)
		default:
			return nil, fmt.Errorf("invalid logcat filter %q: must be one of V D I W E F S", opts.Filter)
		}
	}

	out, err := b.runDeviceRaw(ctx, serial, args...)
	if err != nil {
		return nil, fmt.Errorf("adb logcat %s: %w", serial, err)
	}
	return out, nil
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

// runRaw is the binary-safe counterpart to run: it returns stdout as bytes,
// untrimmed, so callers receive the exact wire output (e.g. a PNG from
// `exec-out screencap -p`).
func (b *ADBBridge) runRaw(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, b.adbPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// runDeviceRaw is the binary-safe runDevice; the returned bytes are exactly
// what adb wrote to stdout, with no trimming or string conversion.
func (b *ADBBridge) runDeviceRaw(ctx context.Context, serial string, args ...string) ([]byte, error) {
	fullArgs := append([]string{"-s", serial}, args...)
	return b.runRaw(ctx, fullArgs...)
}
