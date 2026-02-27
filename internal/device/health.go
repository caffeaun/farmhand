package device

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// GetBatteryInfo retrieves battery information from an Android device
// by parsing `adb shell dumpsys battery` output.
func (b *ADBBridge) GetBatteryInfo(serial string) (level int, charging bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := b.runDevice(ctx, serial, "shell", "dumpsys", "battery")
	if err != nil {
		return 0, false, fmt.Errorf("adb dumpsys battery %s: %w", serial, err)
	}
	return ParseBatteryOutput(out)
}

// ParseBatteryOutput parses `dumpsys battery` output into level and charging state.
// Exported for testing with fixture strings.
//
// Recognised charging indicators:
//   - status: 2  → BATTERY_STATUS_CHARGING
//   - status: 5  → BATTERY_STATUS_FULL (treated as charging)
//   - AC powered: true
//   - USB powered: true
//   - Wireless powered: true
func ParseBatteryOutput(output string) (level int, charging bool, err error) {
	var levelFound bool

	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		// Split on the first colon to get key and value.
		idx := strings.IndexByte(line, ':')
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])

		switch key {
		case "level":
			n, parseErr := strconv.Atoi(val)
			if parseErr != nil {
				return 0, false, fmt.Errorf("parse battery level %q: %w", val, parseErr)
			}
			level = n
			levelFound = true

		case "status":
			// 2 = BATTERY_STATUS_CHARGING, 5 = BATTERY_STATUS_FULL
			if val == "2" || val == "5" {
				charging = true
			}

		case "AC powered", "USB powered", "Wireless powered":
			if val == "true" {
				charging = true
			}
		}
	}

	if !levelFound {
		return 0, false, fmt.Errorf("battery level not found in dumpsys output")
	}
	return level, charging, nil
}
