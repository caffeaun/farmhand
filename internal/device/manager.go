package device

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/caffeaun/farmhand/internal/db"
	"github.com/caffeaun/farmhand/internal/events"
	"github.com/rs/zerolog"
)

// adbDriver is the subset of *ADBBridge methods used by Manager.
// Defined here (consumer side) so tests can inject a fake without touching android.go.
type adbDriver interface {
	Devices() ([]Device, error)
	GetProperty(serial, prop string) (string, error)
	Connect(serial string) error
	WakeDevice(serial string) error
	RebootDevice(serial string) error
	GetBatteryInfo(serial string) (level int, charging bool, err error)
}

// isWirelessSerial returns true when id looks like an IP:port ADB serial
// (e.g. "192.168.1.50:5555"), as opposed to a USB serial like "ZX1G226B4T".
func isWirelessSerial(id string) bool {
	host, port, err := net.SplitHostPort(id)
	if err != nil || port == "" {
		return false
	}
	return net.ParseIP(host) != nil
}

// iosDriver is the subset of *IOSBridge methods used by Manager.
// Defined here (consumer side) so tests can inject a fake without touching ios.go.
type iosDriver interface {
	Devices() ([]Device, error)
}

// Manager handles device discovery, lifecycle, and operations.
// It polls the ADB and iOS bridges at a configurable interval, upserts
// discovered devices into the repository, marks stale devices as offline,
// and publishes DeviceOnline / DeviceOffline events when status changes.
type Manager struct {
	adb          adbDriver
	ios          iosDriver // nil on Linux (iOS not supported)
	repo         *db.DeviceRepository
	bus          *events.Bus
	logger       zerolog.Logger
	pollInterval time.Duration
	mu           sync.Mutex // prevents concurrent poll ticks from overlapping
}

// NewManager creates a Manager.
// ios may be nil on platforms where iOS is not supported.
func NewManager(
	adb *ADBBridge,
	ios *IOSBridge,
	repo *db.DeviceRepository,
	bus *events.Bus,
	pollInterval time.Duration,
	logger zerolog.Logger,
) *Manager {
	var iosI iosDriver
	if ios != nil {
		iosI = ios
	}
	return &Manager{
		adb:          adb,
		ios:          iosI,
		repo:         repo,
		bus:          bus,
		logger:       logger,
		pollInterval: pollInterval,
	}
}

// Start launches the background polling goroutine and returns immediately.
// The goroutine runs an immediate poll, then polls on each ticker tick.
// It stops cleanly when ctx is cancelled.
func (m *Manager) Start(ctx context.Context) {
	go func() {
		// Run an immediate poll so callers do not have to wait for the first tick.
		m.poll(ctx)

		ticker := time.NewTicker(m.pollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				m.poll(ctx)
			case <-ctx.Done():
				m.logger.Info().Msg("device manager: stopping poll loop")
				return
			}
		}
	}()
}

// poll runs a single discovery cycle:
//  1. Query ADB bridge for Android devices.
//  2. Query iOS bridge for iOS devices (if available).
//  3. Upsert each discovered device; detect online↔offline transitions.
//  4. Mark devices not seen in 2×pollInterval as 'offline'.
//  5. Publish DeviceOnline / DeviceOffline events for status changes.
func (m *Manager) poll(ctx context.Context) {
	// Serialise concurrent ticks (e.g. first immediate poll vs. first ticker fire).
	m.mu.Lock()
	defer m.mu.Unlock()

	// Collect devices from all bridges.
	var discovered []Device
	adbErr := false

	adbDevices, err := m.adb.Devices()
	if err != nil {
		adbErr = true
		m.logger.Error().Err(err).Msg("device manager: ADB bridge error")
	} else {
		discovered = append(discovered, adbDevices...)
	}

	if m.ios != nil {
		iosDevices, err := m.ios.Devices()
		if err != nil {
			m.logger.Error().Err(err).Msg("device manager: iOS bridge error")
		} else {
			discovered = append(discovered, iosDevices...)
		}
	}

	// Fetch stable hardware IDs for online Android devices.
	for i, d := range discovered {
		if d.Platform == PlatformAndroid && d.Status == "online" {
			hwID, err := m.adb.GetProperty(d.ID, "ro.serialno")
			if err != nil {
				m.logger.Debug().Err(err).Str("device_id", d.ID).Msg("device manager: failed to fetch ro.serialno")
			} else {
				discovered[i].HardwareID = strings.TrimSpace(hwID)
			}
		}
		// iOS devices use UDID as their ID, which is already stable.
		if d.Platform == PlatformIOS {
			discovered[i].HardwareID = d.ID
		}
	}

	// Upsert discovered devices and emit DeviceOnline events for new/recovered ones.
	for _, d := range discovered {
		// Check previous status before upserting so we can detect transitions.
		prev, prevErr := m.repo.FindByID(d.ID)

		// Merge logic: when hardware_id matches an existing record with a
		// different serial (e.g. wireless port changed), delete the old record
		// and carry its tags forward to the new one.
		if d.HardwareID != "" {
			existing, findErr := m.repo.FindByHardwareID(d.HardwareID)
			if findErr == nil && existing.ID != d.ID {
				m.logger.Info().
					Str("hardware_id", d.HardwareID).
					Str("old_serial", existing.ID).
					Str("new_serial", d.ID).
					Msg("device manager: merging device record (serial changed)")
				// Preserve user-assigned tags from the old record.
				if len(existing.Tags) > 0 && len(d.Tags) == 0 {
					d.Tags = existing.Tags
				}
				_ = m.repo.Delete(existing.ID)
			}
		}

		dbDev := bridgeDeviceToDB(d)
		if err := m.repo.Upsert(dbDev); err != nil {
			m.logger.Error().Err(err).Str("device_id", d.ID).Msg("device manager: upsert failed")
			continue
		}

		// Fire DeviceOnline when: device was previously offline or not in DB.
		wasOffline := prevErr != nil || prev.Status == "offline"
		if wasOffline && d.Status == "online" {
			m.bus.Publish(events.Event{
				Type:      events.DeviceOnline,
				Payload:   dbDev,
				Timestamp: time.Now().UTC(),
			})
		}
	}

	// Mark stale devices as offline.
	// "Stale" means last_seen < now - 2*pollInterval AND status != 'offline'.
	staleThreshold := time.Now().UTC().Add(-2 * m.pollInterval)
	all, err := m.repo.FindAll(db.DeviceFilter{})
	if err != nil {
		m.logger.Error().Err(err).Msg("device manager: could not list devices for staleness check")
		return
	}

	for _, d := range all {
		if d.Status != "offline" && d.LastSeen.Before(staleThreshold) {
			if err := m.repo.UpdateStatus(d.ID, "offline"); err != nil {
				m.logger.Error().Err(err).Str("device_id", d.ID).Msg("device manager: mark offline failed")
				continue
			}
			d.Status = "offline"
			m.bus.Publish(events.Event{
				Type:      events.DeviceOffline,
				Payload:   d,
				Timestamp: time.Now().UTC(),
			})
			m.logger.Info().Str("device_id", d.ID).Msg("device manager: marked offline (stale)")
		}
	}

	// Attempt to reconnect offline wireless Android devices.
	// Skip entirely when ADB bridge is down to avoid spamming connect.
	if !adbErr {
		for _, d := range all {
			if d.Status == "offline" && d.Platform == PlatformAndroid && isWirelessSerial(d.ID) {
				if err := m.adb.Connect(d.ID); err != nil {
					m.logger.Debug().Err(err).Str("device_id", d.ID).Msg("device manager: wireless reconnect failed")
				} else {
					m.logger.Debug().Str("device_id", d.ID).Msg("device manager: wireless reconnect attempted")
				}
			}
		}
	}

	_ = ctx // ctx reserved for future cancellation within poll steps
}

// List returns devices matching the filter.
func (m *Manager) List(filter db.DeviceFilter) ([]db.Device, error) {
	return m.repo.FindAll(filter)
}

// GetByID returns a device by ID.
func (m *Manager) GetByID(id string) (db.Device, error) {
	return m.repo.FindByID(id)
}

// Wake sends a wakeup command to the device.
// Returns an error when the device is not found or is currently offline.
func (m *Manager) Wake(id string) error {
	device, err := m.repo.FindByID(id)
	if err != nil {
		return err
	}
	if device.Status == "offline" {
		return fmt.Errorf("device %s is offline", id)
	}
	if device.Platform == PlatformAndroid {
		return m.adb.WakeDevice(id)
	}
	return fmt.Errorf("wake not supported for platform %s", device.Platform)
}

// Reboot sends a reboot command to the device.
func (m *Manager) Reboot(id string) error {
	device, err := m.repo.FindByID(id)
	if err != nil {
		return err
	}
	if device.Platform == PlatformAndroid {
		return m.adb.RebootDevice(id)
	}
	return fmt.Errorf("reboot not supported for platform %s", device.Platform)
}

// HealthCheck returns health metrics for a device.
// For Android: full health data including battery from `dumpsys battery`.
// For iOS: limited data (IsOnline, LastSeen only, BatteryLevel=-1).
func (m *Manager) HealthCheck(id string) (DeviceHealth, error) {
	device, err := m.repo.FindByID(id)
	if err != nil {
		return DeviceHealth{}, err
	}

	health := DeviceHealth{
		DeviceID:     device.ID,
		BatteryLevel: device.BatteryLevel,
		IsOnline:     device.Status == "online" || device.Status == "busy",
		LastSeen:     device.LastSeen,
	}

	if device.Platform == PlatformAndroid && health.IsOnline {
		level, charging, battErr := m.adb.GetBatteryInfo(device.ID)
		if battErr == nil {
			health.BatteryLevel = level
			health.BatteryCharging = charging
			// Best-effort update of battery level in DB; ignore error.
			_ = m.repo.UpdateBatteryLevel(device.ID, level)
		}
	} else if device.Platform == PlatformIOS {
		health.BatteryLevel = -1 // iOS battery not available in MVP
	}

	return health, nil
}

// bridgeDeviceToDB converts a Device returned by a Bridge into a db.Device
// suitable for the repository layer.
func bridgeDeviceToDB(d Device) db.Device {
	return db.Device{
		ID:           d.ID,
		Platform:     d.Platform,
		Model:        d.Model,
		OSVersion:    d.OSVersion,
		Status:       d.Status,
		BatteryLevel: d.BatteryLevel,
		HardwareID:   d.HardwareID,
		Tags:         d.Tags,
		LastSeen:     d.LastSeen,
		CreatedAt:    d.CreatedAt,
	}
}
