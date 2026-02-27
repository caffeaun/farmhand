package device

import (
	"os"
	"path/filepath"
	"testing"
)

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
