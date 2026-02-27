package device

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureXCTraceOutput is a representative snapshot of `xcrun xctrace list devices`.
const fixtureXCTraceOutput = `== Devices ==
Mac mini (00000000-0000-0000-0000-000000000000)
iPhone 15 Pro (17.2) (00008101-001A34561234001E)
iPad Air (5th generation) (16.7.2) (00008027-XXXXXXXXXXXX)

== Simulators ==
iPhone 15 Simulator (17.2) (A1B2C3D4-E5F6-7890-ABCD-EF1234567890)
iPad Pro Simulator (17.2) (F1E2D3C4-B5A6-7890-DCBA-FE0987654321)
`

func TestNewIOSBridge_NonDarwin(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("skipping non-darwin test on macOS")
	}
	_, err := NewIOSBridge()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "iOS device support requires macOS")
	assert.Contains(t, err.Error(), runtime.GOOS)
}

func TestNewIOSBridge_DarwinRequiresXcrun(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("NewIOSBridge macOS checks only run on darwin")
	}
	// On a standard macOS CI / dev machine xcrun should be present.
	// If it's not, the error must mention xcrun and the install hint.
	bridge, err := NewIOSBridge()
	if err != nil {
		assert.Contains(t, err.Error(), "xcrun not found")
		assert.Contains(t, err.Error(), "xcode-select --install")
		return
	}
	require.NotNil(t, bridge)
	assert.NotEmpty(t, bridge.xcrunPath)
}

// --- parseXCTraceOutput tests (no real device / macOS required) ---

func TestParseXCTraceOutput_PhysicalDevicesOnly(t *testing.T) {
	devices := parseXCTraceOutput(fixtureXCTraceOutput)

	require.Len(t, devices, 2, "should parse exactly 2 physical devices (Mac host excluded, simulators skipped)")

	iphone := devices[0]
	assert.Equal(t, "00008101-001A34561234001E", iphone.ID)
	assert.Equal(t, PlatformIOS, iphone.Platform)
	assert.Equal(t, "iPhone 15 Pro", iphone.Model)
	assert.Equal(t, "17.2", iphone.OSVersion)
	assert.Equal(t, "online", iphone.Status)

	ipad := devices[1]
	assert.Equal(t, "00008027-XXXXXXXXXXXX", ipad.ID)
	assert.Equal(t, PlatformIOS, ipad.Platform)
	assert.Equal(t, "iPad Air (5th generation)", ipad.Model)
	assert.Equal(t, "16.7.2", ipad.OSVersion)
	assert.Equal(t, "online", ipad.Status)
}

func TestParseXCTraceOutput_SkipsSimulators(t *testing.T) {
	devices := parseXCTraceOutput(fixtureXCTraceOutput)

	for _, d := range devices {
		assert.NotContains(t, d.Model, "Simulator", "simulator entries must be excluded")
	}
}

func TestParseXCTraceOutput_EmptyOutput(t *testing.T) {
	devices := parseXCTraceOutput("")
	assert.Empty(t, devices)
}

func TestParseXCTraceOutput_EmptyDevicesSection(t *testing.T) {
	output := `== Devices ==

== Simulators ==
iPhone 15 Simulator (17.2) (A1B2C3D4-E5F6-7890-ABCD-EF1234567890)
`
	devices := parseXCTraceOutput(output)
	assert.Empty(t, devices, "no physical devices should be returned")
}

func TestParseXCTraceOutput_MultipleDevices(t *testing.T) {
	output := `== Devices ==
Mac mini (00000000-0000-0000-0000-000000000000)
iPhone 14 (16.5) (AAAAAA-111111-222222)
iPhone 15 Pro (17.2) (BBBBBB-333333-444444)
iPad mini (6th generation) (15.8) (CCCCCC-555555-666666)

== Simulators ==
`
	devices := parseXCTraceOutput(output)
	require.Len(t, devices, 3)
	assert.Equal(t, "AAAAAA-111111-222222", devices[0].ID)
	assert.Equal(t, "iPhone 14", devices[0].Model)
	assert.Equal(t, "16.5", devices[0].OSVersion)

	assert.Equal(t, "BBBBBB-333333-444444", devices[1].ID)
	assert.Equal(t, "iPhone 15 Pro", devices[1].Model)

	assert.Equal(t, "CCCCCC-555555-666666", devices[2].ID)
	assert.Equal(t, "iPad mini (6th generation)", devices[2].Model)
	assert.Equal(t, "15.8", devices[2].OSVersion)
}

func TestParseXCTraceOutput_NoSimulatorSection(t *testing.T) {
	output := `== Devices ==
iPhone 15 Pro (17.2) (00008101-001A34561234001E)
`
	devices := parseXCTraceOutput(output)
	require.Len(t, devices, 1)
	assert.Equal(t, "iPhone 15 Pro", devices[0].Model)
}

func TestParseXCTraceOutput_MacHostLineSkipped(t *testing.T) {
	output := `== Devices ==
Mac mini (00000000-0000-0000-0000-000000000000)
`
	devices := parseXCTraceOutput(output)
	assert.Empty(t, devices, "Mac host line must be skipped")
}
