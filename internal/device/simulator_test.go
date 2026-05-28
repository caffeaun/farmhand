package device

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureSimctlJSON mirrors `xcrun simctl list devices available --json`:
// an iOS runtime with a booted + a shutdown device, plus a non-iOS (watchOS)
// runtime that must be filtered out.
const fixtureSimctlJSON = `{
  "devices": {
    "com.apple.CoreSimulator.SimRuntime.iOS-26-5": [
      {
        "udid": "EDA19CAC-8ABA-40FE-BA21-9D850FEA438C",
        "name": "iPhone 17 Pro",
        "state": "Booted",
        "isAvailable": true
      },
      {
        "udid": "B51B3170-23BC-474E-ACF9-7201959EE7CB",
        "name": "iPhone 17",
        "state": "Shutdown",
        "isAvailable": true
      },
      {
        "udid": "00000000-0000-0000-0000-000000000000",
        "name": "Unavailable Phone",
        "state": "Shutdown",
        "isAvailable": false
      }
    ],
    "com.apple.CoreSimulator.SimRuntime.watchOS-26-5": [
      {
        "udid": "11111111-1111-1111-1111-111111111111",
        "name": "Apple Watch Series 10",
        "state": "Booted",
        "isAvailable": true
      }
    ]
  }
}`

func TestParseSimctlList_iOSOnlyAndAvailable(t *testing.T) {
	devices, err := parseSimctlList(fixtureSimctlJSON)
	require.NoError(t, err)

	// 2 available iOS sims (watchOS filtered, unavailable filtered).
	require.Len(t, devices, 2)

	byUDID := map[string]simDevice{}
	for _, d := range devices {
		byUDID[d.UDID] = d
	}

	pro, ok := byUDID["EDA19CAC-8ABA-40FE-BA21-9D850FEA438C"]
	require.True(t, ok)
	assert.Equal(t, "iPhone 17 Pro", pro.Name)
	assert.Equal(t, "Booted", pro.State)
	assert.Equal(t, "26.5", pro.OSVersion)

	std, ok := byUDID["B51B3170-23BC-474E-ACF9-7201959EE7CB"]
	require.True(t, ok)
	assert.Equal(t, "Shutdown", std.State)
}

func TestParseSimctlList_InvalidJSON(t *testing.T) {
	_, err := parseSimctlList("not json")
	require.Error(t, err)
}

func TestParseSimctlList_Empty(t *testing.T) {
	devices, err := parseSimctlList(`{"devices": {}}`)
	require.NoError(t, err)
	assert.Empty(t, devices)
}

func TestIOSRuntimeVersion(t *testing.T) {
	tests := []struct {
		key    string
		want   string
		wantOK bool
	}{
		{"com.apple.CoreSimulator.SimRuntime.iOS-26-5", "26.5", true},
		{"com.apple.CoreSimulator.SimRuntime.iOS-18-3-1", "18.3.1", true},
		{"com.apple.CoreSimulator.SimRuntime.watchOS-26-5", "", false},
		{"com.apple.CoreSimulator.SimRuntime.tvOS-26-5", "", false},
		{"com.apple.CoreSimulator.SimRuntime.xrOS-26-5", "", false},
		{"garbage-key", "", false},
	}
	for _, tt := range tests {
		got, ok := iosRuntimeVersion(tt.key)
		assert.Equal(t, tt.wantOK, ok, tt.key)
		assert.Equal(t, tt.want, got, tt.key)
	}
}

func TestIsUDID(t *testing.T) {
	assert.True(t, isUDID("EDA19CAC-8ABA-40FE-BA21-9D850FEA438C"))
	assert.True(t, isUDID("eda19cac-8aba-40fe-ba21-9d850fea438c"))
	assert.False(t, isUDID("iPhone 17 Pro"))
	assert.False(t, isUDID("EDA19CAC8ABA40FEBA219D850FEA438C")) // no hyphens
	assert.False(t, isUDID(""))
}

func TestResolveTargets(t *testing.T) {
	all := []simDevice{
		{UDID: "EDA19CAC-8ABA-40FE-BA21-9D850FEA438C", Name: "iPhone 17 Pro", State: "Booted", OSVersion: "26.5"},
		{UDID: "B51B3170-23BC-474E-ACF9-7201959EE7CB", Name: "iPhone 17", State: "Shutdown", OSVersion: "26.5"},
		{UDID: "C0000000-0000-0000-0000-000000000001", Name: "iPhone 17", State: "Shutdown", OSVersion: "26.2"},
	}

	t.Run("by name (first match on duplicate)", func(t *testing.T) {
		resolved, unresolved := resolveTargets([]string{"iPhone 17 Pro", "iPhone 17"}, all)
		require.Empty(t, unresolved)
		require.Len(t, resolved, 2)
		assert.Equal(t, "iPhone 17 Pro", resolved[0].Name)
		assert.Equal(t, "B51B3170-23BC-474E-ACF9-7201959EE7CB", resolved[1].UDID) // first "iPhone 17"
	})

	t.Run("by UDID", func(t *testing.T) {
		resolved, unresolved := resolveTargets([]string{"C0000000-0000-0000-0000-000000000001"}, all)
		require.Empty(t, unresolved)
		require.Len(t, resolved, 1)
		assert.Equal(t, "26.2", resolved[0].OSVersion)
	})

	t.Run("unresolved name and udid", func(t *testing.T) {
		resolved, unresolved := resolveTargets([]string{"iPad Air", "FFFFFFFF-0000-0000-0000-000000000000"}, all)
		assert.Empty(t, resolved)
		assert.Equal(t, []string{"iPad Air", "FFFFFFFF-0000-0000-0000-000000000000"}, unresolved)
	})
}
