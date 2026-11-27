package api

import (
	"database/sql"
	"math"
	"sync"
	"time"

	"github.com/duxet/netmon/live"
	"github.com/duxet/netmon/storage"
	"github.com/gofiber/fiber/v3"
)

// parseSince reads the optional since query parameter: the time of the newest
// sample the client already has.
func parseSince(c fiber.Ctx) (time.Time, error) {
	value := c.Query("since")
	if value == "" {
		return time.Time{}, nil
	}

	since, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, badRequest("since must be an RFC 3339 timestamp, e.g. 2026-11-24T08:00:00.123Z: " + value)
	}

	return since, nil
}

// toLiveDTOs turns samples into what the API answers, with the traffic of
// MAC addresses merged into a device counted as the device's: deviceOf
// returns the device a MAC address belongs to.
func toLiveDTOs(samples []live.Sample, deviceOf func(string) string) []LiveSampleDTO {
	dtos := make([]LiveSampleDTO, 0, len(samples))

	for _, sample := range samples {
		devices := make([]LiveDeviceDTO, 0, len(sample.Devices))
		byMAC := map[string]int{}

		for _, device := range sample.Devices {
			download := uint64(math.Round(device.Download))
			upload := uint64(math.Round(device.Upload))

			// A trickle of less than half a byte per second is a quiet device.
			if download == 0 && upload == 0 {
				continue
			}

			mac := deviceOf(device.MAC.String())
			if i, ok := byMAC[mac]; ok {
				devices[i].Download += download
				devices[i].Upload += upload
				continue
			}

			dto := LiveDeviceDTO{
				MACAddress: mac,
				Download:   download,
				Upload:     upload,
			}
			if device.IP.IsValid() {
				dto.IPAddress = device.IP.String()
			}

			byMAC[mac] = len(devices)
			devices = append(devices, dto)
		}

		dtos = append(dtos, LiveSampleDTO{Time: sample.Time, Devices: devices})
	}

	return dtos
}

// aliasRefresh is how long the merged MAC addresses are kept, as the live
// view asks every second.
const aliasRefresh = 10 * time.Second

// deviceAliases knows which device each merged MAC address belongs to.
type deviceAliases struct {
	db *sql.DB

	mu      sync.Mutex
	devices map[string]string
	read    time.Time
}

// deviceOf returns the device mac was merged into, or mac.
func (a *deviceAliases) deviceOf(now time.Time) func(string) string {
	a.mu.Lock()
	if a.devices == nil || now.Sub(a.read) >= aliasRefresh {
		if aliases, err := storage.GetAliases(a.db); err == nil {
			a.devices = map[string]string{}
			for device, macs := range aliases {
				for _, mac := range macs {
					a.devices[mac] = device
				}
			}
			a.read = now
		}
	}
	devices := a.devices
	a.mu.Unlock()

	return func(mac string) string {
		if device, ok := devices[mac]; ok {
			return device
		}
		return mac
	}
}

// registerLiveRoutes adds the endpoint for the current bandwidth of devices.
func registerLiveRoutes(app *fiber.App, db *sql.DB, meter *live.Meter) {
	aliases := &deviceAliases{db: db}

	// The samples of the last couple of minutes, or with since only those
	// newer than it. The web UI asks every second, so unlike the other
	// endpoints this one does not log its requests.
	app.Get("/api/live", func(c fiber.Ctx) error {
		since, err := parseSince(c)
		if err != nil {
			return err
		}

		// Collectors that are costly to sample only do so while someone asks.
		meter.Watch(time.Now())

		c.Set(fiber.HeaderCacheControl, "no-store")

		return c.JSON(toLiveDTOs(meter.Samples(since), aliases.deviceOf(time.Now())))
	})
}
