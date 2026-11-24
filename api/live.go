package api

import (
	"math"
	"time"

	"github.com/duxet/netmon/live"
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

func toLiveDTOs(samples []live.Sample) []LiveSampleDTO {
	dtos := make([]LiveSampleDTO, 0, len(samples))

	for _, sample := range samples {
		devices := make([]LiveDeviceDTO, 0, len(sample.Devices))

		for _, device := range sample.Devices {
			download := uint64(math.Round(device.Download))
			upload := uint64(math.Round(device.Upload))

			// A trickle of less than half a byte per second is a quiet device.
			if download == 0 && upload == 0 {
				continue
			}

			dto := LiveDeviceDTO{
				MACAddress: device.MAC.String(),
				Download:   download,
				Upload:     upload,
			}
			if device.IP.IsValid() {
				dto.IPAddress = device.IP.String()
			}

			devices = append(devices, dto)
		}

		dtos = append(dtos, LiveSampleDTO{Time: sample.Time, Devices: devices})
	}

	return dtos
}

// registerLiveRoutes adds the endpoint for the current bandwidth of devices.
func registerLiveRoutes(app *fiber.App, meter *live.Meter) {
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

		return c.JSON(toLiveDTOs(meter.Samples(since)))
	})
}
