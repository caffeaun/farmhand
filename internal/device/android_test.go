package device

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// makeRecordingADB writes a fake `adb` that appends its argument vector
// (one $* line per invocation) to a record file in dir. The script exits
// 0 for any input-style command, so the bridge code path runs normally.
// Returns (adbPath, recordPath).
func makeRecordingADB(t *testing.T, dir string) (string, string) {
	t.Helper()
	recordPath := filepath.Join(dir, "adb-record.log")
	script := `#!/bin/sh
echo "$*" >> "` + recordPath + `"
exit 0
`
	adbPath := filepath.Join(dir, "adb")
	if err := os.WriteFile(adbPath, []byte(script), 0700); err != nil {
		t.Fatalf("write recording adb: %v", err)
	}
	return adbPath, recordPath
}

// readRecording returns the recorded argument vectors as a slice of strings,
// one per fake-adb invocation. Trailing newline removed.
func readRecording(t *testing.T, recordPath string) []string {
	t.Helper()
	data, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("read recording: %v", err)
	}
	trimmed := strings.TrimRight(string(data), "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

// makeFakeADB writes a shell script that acts as a fake `adb` binary into dir
// and returns its path. The script uses its arguments to decide what to output.
func makeFakeADB(t *testing.T, dir string) string {
	t.Helper()
	script := `#!/bin/sh
case "$*" in
  "devices -l")
    cat <<'EOF'
List of devices attached
RFCXXXXXXXXX           device usb:1-1 product:starqltesq model:SM_S911B transport_id:1
ABCDEF123456           offline usb:1-2 product:raven model:Pixel_6_Pro transport_id:2
UNAUTHORIZED1          unauthorized usb:1-3 transport_id:3
EOF
    ;;
  "-s RFCXXXXXXXXX shell getprop ro.product.model")
    printf "SM-S911B\n"
    ;;
  "-s RFCXXXXXXXXX shell getprop ro.build.version.release")
    printf "13\n"
    ;;
  *)
    exit 0
    ;;
esac
`
	adbPath := filepath.Join(dir, "adb")
	if err := os.WriteFile(adbPath, []byte(script), 0700); err != nil {
		t.Fatalf("write fake adb: %v", err)
	}
	return adbPath
}

// makeEmptyADB writes a fake `adb` that returns only the header (no devices).
func makeEmptyADB(t *testing.T, dir string) string {
	t.Helper()
	script := `#!/bin/sh
case "$*" in
  "devices -l")
    echo "List of devices attached"
    ;;
esac
`
	adbPath := filepath.Join(dir, "adb")
	if err := os.WriteFile(adbPath, []byte(script), 0700); err != nil {
		t.Fatalf("write empty adb: %v", err)
	}
	return adbPath
}

func TestNewADBBridge_NotFound(t *testing.T) {
	_, err := NewADBBridge("/nonexistent/path/adb")
	if err == nil {
		t.Fatal("expected error for nonexistent adb binary, got nil")
	}
}

func TestNewADBBridge_Found(t *testing.T) {
	dir := t.TempDir()
	adbPath := makeFakeADB(t, dir)

	bridge, err := NewADBBridge(adbPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bridge == nil {
		t.Fatal("expected non-nil bridge")
	}
}

func TestDevices_MultiDevice(t *testing.T) {
	dir := t.TempDir()
	adbPath := makeFakeADB(t, dir)

	bridge, err := NewADBBridge(adbPath)
	if err != nil {
		t.Fatalf("NewADBBridge: %v", err)
	}

	devices, err := bridge.Devices()
	if err != nil {
		t.Fatalf("Devices(): %v", err)
	}

	if len(devices) != 3 {
		t.Fatalf("expected 3 devices, got %d", len(devices))
	}

	tests := []struct {
		serial   string
		model    string
		status   string
		platform string
	}{
		{"RFCXXXXXXXXX", "SM_S911B", "online", PlatformAndroid},
		{"ABCDEF123456", "Pixel_6_Pro", "offline", PlatformAndroid},
		{"UNAUTHORIZED1", "", "offline", PlatformAndroid},
	}

	for i, tt := range tests {
		d := devices[i]
		if d.ID != tt.serial {
			t.Errorf("device[%d].ID = %q, want %q", i, d.ID, tt.serial)
		}
		if d.Status != tt.status {
			t.Errorf("device[%d].Status = %q, want %q", i, d.Status, tt.status)
		}
		if d.Platform != tt.platform {
			t.Errorf("device[%d].Platform = %q, want %q", i, d.Platform, tt.platform)
		}
		if d.Model != tt.model {
			t.Errorf("device[%d].Model = %q, want %q", i, d.Model, tt.model)
		}
		if d.BatteryLevel != -1 {
			t.Errorf("device[%d].BatteryLevel = %d, want -1", i, d.BatteryLevel)
		}
		if d.Tags == nil {
			t.Errorf("device[%d].Tags is nil, want empty slice", i)
		}
	}
}

func TestDevices_NoDevices(t *testing.T) {
	dir := t.TempDir()
	adbPath := makeEmptyADB(t, dir)

	bridge, err := NewADBBridge(adbPath)
	if err != nil {
		t.Fatalf("NewADBBridge: %v", err)
	}

	devices, err := bridge.Devices()
	if err != nil {
		t.Fatalf("Devices(): %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("expected 0 devices, got %d: %v", len(devices), devices)
	}
}

func TestGetProperty_TrimmedOutput(t *testing.T) {
	dir := t.TempDir()
	adbPath := makeFakeADB(t, dir)

	bridge, err := NewADBBridge(adbPath)
	if err != nil {
		t.Fatalf("NewADBBridge: %v", err)
	}

	// ro.product.model returns "SM-S911B\n" — must be trimmed to "SM-S911B"
	val, err := bridge.GetProperty("RFCXXXXXXXXX", "ro.product.model")
	if err != nil {
		t.Fatalf("GetProperty: %v", err)
	}
	if val != "SM-S911B" {
		t.Errorf("GetProperty = %q, want %q", val, "SM-S911B")
	}
}

func TestIsOnline_True(t *testing.T) {
	dir := t.TempDir()
	adbPath := makeFakeADB(t, dir)

	bridge, err := NewADBBridge(adbPath)
	if err != nil {
		t.Fatalf("NewADBBridge: %v", err)
	}

	if !bridge.IsOnline("RFCXXXXXXXXX") {
		t.Error("IsOnline(RFCXXXXXXXXX) = false, want true")
	}
}

func TestIsOnline_False_Offline(t *testing.T) {
	dir := t.TempDir()
	adbPath := makeFakeADB(t, dir)

	bridge, err := NewADBBridge(adbPath)
	if err != nil {
		t.Fatalf("NewADBBridge: %v", err)
	}

	if bridge.IsOnline("ABCDEF123456") {
		t.Error("IsOnline(ABCDEF123456) = true, want false")
	}
}

func TestIsOnline_False_Unknown(t *testing.T) {
	dir := t.TempDir()
	adbPath := makeFakeADB(t, dir)

	bridge, err := NewADBBridge(adbPath)
	if err != nil {
		t.Fatalf("NewADBBridge: %v", err)
	}

	if bridge.IsOnline("NOTADEVICE") {
		t.Error("IsOnline(NOTADEVICE) = true, want false")
	}
}

func TestIsOnline_False_Unauthorized(t *testing.T) {
	dir := t.TempDir()
	adbPath := makeFakeADB(t, dir)

	bridge, err := NewADBBridge(adbPath)
	if err != nil {
		t.Fatalf("NewADBBridge: %v", err)
	}

	if bridge.IsOnline("UNAUTHORIZED1") {
		t.Error("IsOnline(UNAUTHORIZED1) = true, want false")
	}
}

// --------------------------------------------------------------------------
// Tap / Swipe / KeyEvent / InputText
// --------------------------------------------------------------------------

func TestTap_RecordsCorrectCommand(t *testing.T) {
	dir := t.TempDir()
	adbPath, recordPath := makeRecordingADB(t, dir)
	bridge, err := NewADBBridge(adbPath)
	if err != nil {
		t.Fatalf("NewADBBridge: %v", err)
	}

	if err := bridge.Tap("R58W2193TXP", 540, 960); err != nil {
		t.Fatalf("Tap: %v", err)
	}

	got := readRecording(t, recordPath)
	want := []string{"-s R58W2193TXP shell input tap 540 960"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Errorf("recording = %v, want %v", got, want)
	}
}

func TestTap_NegativeCoordinatesRejected(t *testing.T) {
	dir := t.TempDir()
	adbPath, _ := makeRecordingADB(t, dir)
	bridge, _ := NewADBBridge(adbPath)

	if err := bridge.Tap("X", -1, 5); err == nil {
		t.Error("expected error for negative X, got nil")
	}
	if err := bridge.Tap("X", 5, -1); err == nil {
		t.Error("expected error for negative Y, got nil")
	}
}

func TestSwipe_WithDuration(t *testing.T) {
	dir := t.TempDir()
	adbPath, recordPath := makeRecordingADB(t, dir)
	bridge, _ := NewADBBridge(adbPath)

	if err := bridge.Swipe("X", 100, 200, 300, 400, 250); err != nil {
		t.Fatalf("Swipe: %v", err)
	}

	got := readRecording(t, recordPath)
	want := "-s X shell input swipe 100 200 300 400 250"
	if len(got) != 1 || got[0] != want {
		t.Errorf("recording = %v, want [%q]", got, want)
	}
}

func TestSwipe_OmitsZeroDuration(t *testing.T) {
	dir := t.TempDir()
	adbPath, recordPath := makeRecordingADB(t, dir)
	bridge, _ := NewADBBridge(adbPath)

	if err := bridge.Swipe("X", 1, 2, 3, 4, 0); err != nil {
		t.Fatalf("Swipe: %v", err)
	}

	got := readRecording(t, recordPath)
	want := "-s X shell input swipe 1 2 3 4"
	if len(got) != 1 || got[0] != want {
		t.Errorf("recording = %v, want [%q]", got, want)
	}
}

func TestSwipe_NegativeArgsRejected(t *testing.T) {
	dir := t.TempDir()
	adbPath, _ := makeRecordingADB(t, dir)
	bridge, _ := NewADBBridge(adbPath)

	if err := bridge.Swipe("X", -1, 0, 0, 0, 0); err == nil {
		t.Error("expected error for negative x1")
	}
	if err := bridge.Swipe("X", 0, 0, 0, 0, -1); err == nil {
		t.Error("expected error for negative duration")
	}
}

func TestKeyEvent_IntegerKeycode(t *testing.T) {
	dir := t.TempDir()
	adbPath, recordPath := makeRecordingADB(t, dir)
	bridge, _ := NewADBBridge(adbPath)

	if err := bridge.KeyEvent("X", "4"); err != nil {
		t.Fatalf("KeyEvent: %v", err)
	}

	got := readRecording(t, recordPath)
	want := "-s X shell input keyevent 4"
	if len(got) != 1 || got[0] != want {
		t.Errorf("recording = %v, want [%q]", got, want)
	}
}

func TestKeyEvent_SymbolicKeycode(t *testing.T) {
	dir := t.TempDir()
	adbPath, recordPath := makeRecordingADB(t, dir)
	bridge, _ := NewADBBridge(adbPath)

	if err := bridge.KeyEvent("X", "KEYCODE_BACK"); err != nil {
		t.Fatalf("KeyEvent: %v", err)
	}

	got := readRecording(t, recordPath)
	want := "-s X shell input keyevent KEYCODE_BACK"
	if len(got) != 1 || got[0] != want {
		t.Errorf("recording = %v, want [%q]", got, want)
	}
}

func TestKeyEvent_RejectsInvalidKeycode(t *testing.T) {
	dir := t.TempDir()
	adbPath, _ := makeRecordingADB(t, dir)
	bridge, _ := NewADBBridge(adbPath)

	cases := []string{
		"",                // empty
		"-1",              // negative
		"hello",           // not KEYCODE_*
		"KEYCODE_back",    // lowercase
		"KEYCODE_BACK; rm /tmp/x", // injection attempt
	}
	for _, kc := range cases {
		if err := bridge.KeyEvent("X", kc); err == nil {
			t.Errorf("expected error for keycode %q, got nil", kc)
		}
	}
}

func TestInputText_Simple(t *testing.T) {
	dir := t.TempDir()
	adbPath, recordPath := makeRecordingADB(t, dir)
	bridge, _ := NewADBBridge(adbPath)

	if err := bridge.InputText("X", "hello"); err != nil {
		t.Fatalf("InputText: %v", err)
	}

	got := readRecording(t, recordPath)
	want := "-s X shell input text 'hello'"
	if len(got) != 1 || got[0] != want {
		t.Errorf("recording = %v, want [%q]", got, want)
	}
}

// TestInputText_InjectionStaysLiteral is the security-critical test: shell
// metacharacters inside the text must not be interpreted by the device shell.
// Verifies that `a'; reboot` is sent as one safely-quoted argument.
func TestInputText_InjectionStaysLiteral(t *testing.T) {
	dir := t.TempDir()
	adbPath, recordPath := makeRecordingADB(t, dir)
	bridge, _ := NewADBBridge(adbPath)

	// Note: the apostrophe and the trailing `; reboot` would let the device
	// shell run `reboot` if naively concatenated. quoteForDeviceShell must
	// escape the apostrophe so the device shell sees one literal string.
	if err := bridge.InputText("X", "a'; reboot"); err != nil {
		t.Fatalf("InputText: %v", err)
	}

	got := readRecording(t, recordPath)
	if len(got) != 1 {
		t.Fatalf("recording = %v, want 1 invocation", got)
	}
	// The exact host-side $* (after the host shell already consumed one
	// level of quoting because the fake-adb script uses "$*") is:
	//   -s X shell input text 'a'\''; reboot'
	wantSuffix := `input text 'a'\''; reboot'`
	if !strings.HasSuffix(got[0], wantSuffix) {
		t.Errorf("recording = %q, want suffix %q (must escape embedded apostrophe so the trailing `; reboot` stays inside the quoted string)", got[0], wantSuffix)
	}
}

// --------------------------------------------------------------------------
// Screenshot
// --------------------------------------------------------------------------

// makeFixtureADB writes a fake adb that emits the contents of `fixtureBytes`
// when its argument vector contains matchToken; for any other invocation it
// just exits 0. The recorded argument vectors are appended to a log file.
// Returns (adbPath, recordPath).
func makeFixtureADB(t *testing.T, dir, matchToken string, fixtureBytes []byte) (string, string) {
	t.Helper()
	fixturePath := filepath.Join(dir, "fixture.bin")
	if err := os.WriteFile(fixturePath, fixtureBytes, 0600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	recordPath := filepath.Join(dir, "adb-record.log")
	script := `#!/bin/sh
echo "$*" >> "` + recordPath + `"
case "$*" in
  *` + matchToken + `*)
    cat "` + fixturePath + `"
    ;;
esac
exit 0
`
	adbPath := filepath.Join(dir, "adb")
	if err := os.WriteFile(adbPath, []byte(script), 0700); err != nil {
		t.Fatalf("write fixture adb: %v", err)
	}
	return adbPath, recordPath
}

func TestScreenshot_ReturnsBytesAndCallsExecOut(t *testing.T) {
	dir := t.TempDir()
	// Minimal byte sequence — the test only verifies round-trip; the bytes
	// themselves do not need to be a valid PNG, just non-trivial so we
	// catch any truncation / encoding bug.
	want := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 'X', 'Y', 'Z'}
	adbPath, recordPath := makeFixtureADB(t, dir, "screencap", want)
	bridge, _ := NewADBBridge(adbPath)

	got, err := bridge.Screenshot("R58W2193TXP")
	if err != nil {
		t.Fatalf("Screenshot: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("Screenshot bytes:\n got: %q\nwant: %q", got, want)
	}

	rec := readRecording(t, recordPath)
	wantCmd := "-s R58W2193TXP exec-out screencap -p"
	if len(rec) != 1 || rec[0] != wantCmd {
		t.Errorf("recording = %v, want [%q]", rec, wantCmd)
	}
}

// --------------------------------------------------------------------------
// Logcat
// --------------------------------------------------------------------------

func TestLogcat_DumpsBufferAndPassesArgs(t *testing.T) {
	dir := t.TempDir()
	want := []byte("01-01 12:00:00.000  1234  5678 I MainActivity: hello\n")
	adbPath, recordPath := makeFixtureADB(t, dir, "logcat", want)
	bridge, _ := NewADBBridge(adbPath)

	got, err := bridge.Logcat("X", LogcatOptions{Since: 30 * time.Second, Filter: "E"})
	if err != nil {
		t.Fatalf("Logcat: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("Logcat bytes:\n got: %q\nwant: %q", got, want)
	}

	rec := readRecording(t, recordPath)
	if len(rec) != 1 {
		t.Fatalf("recording = %v, want 1 invocation", rec)
	}
	// Verify the command line contains the expected pieces; this avoids
	// being too brittle about positional ordering of -t / *:E.
	for _, piece := range []string{"-s X", "logcat", "-d", "-t 30s", "*:E"} {
		if !strings.Contains(rec[0], piece) {
			t.Errorf("recording %q missing %q", rec[0], piece)
		}
	}
}

func TestLogcat_NoOptsOmitsArgs(t *testing.T) {
	dir := t.TempDir()
	adbPath, recordPath := makeFixtureADB(t, dir, "logcat", []byte("empty\n"))
	bridge, _ := NewADBBridge(adbPath)

	if _, err := bridge.Logcat("X", LogcatOptions{}); err != nil {
		t.Fatalf("Logcat: %v", err)
	}
	rec := readRecording(t, recordPath)
	want := "-s X logcat -d"
	if len(rec) != 1 || rec[0] != want {
		t.Errorf("recording = %v, want [%q]", rec, want)
	}
}

func TestLogcat_MinuteRoundingForSubMinuteSince(t *testing.T) {
	dir := t.TempDir()
	adbPath, recordPath := makeFixtureADB(t, dir, "logcat", []byte("x\n"))
	bridge, _ := NewADBBridge(adbPath)

	// 2 minutes 30 seconds → minutes branch, rounded down to 2m.
	if _, err := bridge.Logcat("X", LogcatOptions{Since: 2*time.Minute + 30*time.Second}); err != nil {
		t.Fatalf("Logcat: %v", err)
	}
	// 800 ms → seconds branch, rounded up to 1s.
	if _, err := bridge.Logcat("X", LogcatOptions{Since: 800 * time.Millisecond}); err != nil {
		t.Fatalf("Logcat: %v", err)
	}

	rec := readRecording(t, recordPath)
	if len(rec) != 2 {
		t.Fatalf("recording = %v, want 2 invocations", rec)
	}
	if !strings.Contains(rec[0], "-t 2m") {
		t.Errorf("first recording = %q, want -t 2m", rec[0])
	}
	if !strings.Contains(rec[1], "-t 1s") {
		t.Errorf("second recording = %q, want -t 1s", rec[1])
	}
}

func TestLogcat_RejectsInvalidFilter(t *testing.T) {
	dir := t.TempDir()
	adbPath, _ := makeFixtureADB(t, dir, "logcat", []byte("x\n"))
	bridge, _ := NewADBBridge(adbPath)

	for _, f := range []string{"verbose", "e", "*", "; rm /tmp/x"} {
		if _, err := bridge.Logcat("X", LogcatOptions{Filter: f}); err == nil {
			t.Errorf("expected error for filter %q, got nil", f)
		}
	}
}

func TestKillAllApps_RecordsCorrectCommand(t *testing.T) {
	dir := t.TempDir()
	adbPath, recordPath := makeRecordingADB(t, dir)
	bridge, _ := NewADBBridge(adbPath)

	if err := bridge.KillAllApps("R58W2193TXP"); err != nil {
		t.Fatalf("KillAllApps: %v", err)
	}

	got := readRecording(t, recordPath)
	want := "-s R58W2193TXP shell am kill-all"
	if len(got) != 1 || got[0] != want {
		t.Errorf("recording = %v, want [%q]", got, want)
	}
}

func TestLaunch_RecordsCorrectCommand(t *testing.T) {
	dir := t.TempDir()
	adbPath, recordPath := makeRecordingADB(t, dir)
	bridge, _ := NewADBBridge(adbPath)

	if err := bridge.Launch("R58W2193TXP", "com.example.app"); err != nil {
		t.Fatalf("Launch: %v", err)
	}

	got := readRecording(t, recordPath)
	want := "-s R58W2193TXP shell am start --pn com.example.app"
	if len(got) != 1 || got[0] != want {
		t.Errorf("recording = %v, want [%q]", got, want)
	}
}

func TestLaunch_RejectsInvalidPackageID(t *testing.T) {
	dir := t.TempDir()
	adbPath, _ := makeRecordingADB(t, dir)
	bridge, _ := NewADBBridge(adbPath)

	invalid := []string{
		"",                        // empty
		"NoDots",                  // single segment
		"Com.Example.App",         // uppercase
		"com.example app",         // whitespace
		"com.example.app;reboot",  // injection attempt
		"com.example.-bad",        // segment starts with dash
		".com.example",            // leading dot
		"com..example",            // empty segment
	}
	for _, pkg := range invalid {
		if err := bridge.Launch("X", pkg); err == nil {
			t.Errorf("Launch(%q) accepted, expected rejection", pkg)
		}
	}
}

func TestLaunch_SurfacesAMErrorOutput(t *testing.T) {
	dir := t.TempDir()
	// Fake adb whose `am start` exits 0 but prints an error line, matching
	// the actual behavior of older Android `am start` against an unknown
	// package.
	script := `#!/bin/sh
case "$*" in
  *"am start "*)
    echo "Error: Activity class {com.unknown/.Main} does not exist."
    exit 0
    ;;
  *)
    exit 0
    ;;
esac
`
	adbPath := filepath.Join(dir, "adb")
	if err := os.WriteFile(adbPath, []byte(script), 0700); err != nil {
		t.Fatalf("write fake adb: %v", err)
	}
	bridge, _ := NewADBBridge(adbPath)

	err := bridge.Launch("X", "com.unknown.app")
	if err == nil {
		t.Fatal("expected error when am start prints an Error: line, got nil")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error did not surface the am output: %v", err)
	}
}

func TestQuoteForDeviceShell(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "''"},
		{"hello", "'hello'"},
		{"a b", "'a b'"},
		{"a'b", `'a'\''b'`},
		{"a';rm /x", `'a'\'';rm /x'`},
		{"'", `''\'''`},
	}
	for _, c := range cases {
		got := quoteForDeviceShell(c.in)
		if got != c.want {
			t.Errorf("quoteForDeviceShell(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

