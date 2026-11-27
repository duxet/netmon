package api

import (
	"time"

	"github.com/duxet/netmon/collector"
	"github.com/duxet/netmon/version"
	"github.com/gofiber/fiber/v3"
)

// staleRecording is how long after the last round of recording the
// collector counts as stuck, as rounds come every minute.
const staleRecording = 3 * time.Minute

// registerStatusRoutes adds the endpoint that tells how netmon is doing,
// with health telling how its collector is, if it has one.
func registerStatusRoutes(app *fiber.App, health *collector.Health) {
	app.Get("/api/status", func(c fiber.Ctx) error {
		dto := StatusDTO{Version: version.String()}
		if health != nil {
			dto.Collector = newCollectorStatusDTO(health.Status(), time.Now())
		}
		return c.JSON(dto)
	})
}

func newCollectorStatusDTO(status collector.HealthStatus, now time.Time) *CollectorStatusDTO {
	dto := &CollectorStatusDTO{
		Running:  status.Running,
		Error:    nonEmpty(status.Error),
		Warnings: status.Warnings,
	}
	if dto.Warnings == nil {
		dto.Warnings = []string{}
	}
	if !status.LastRecorded.IsZero() {
		at := status.LastRecorded
		dto.LastRecorded = &at
		dto.Stale = now.Sub(at) > staleRecording
	}
	return dto
}
