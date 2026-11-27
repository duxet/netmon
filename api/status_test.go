package api

import (
	"errors"
	"testing"
	"time"

	"github.com/duxet/netmon/collector"
	"github.com/duxet/netmon/version"
	"github.com/gofiber/fiber/v3"
)

func TestStatus(t *testing.T) {
	app, _ := newTestApp(t)

	var dto StatusDTO
	if status, _ := get(t, app, "/api/status", &dto); status != fiber.StatusOK {
		t.Fatalf("GET /api/status = %d", status)
	}
	if dto.Version != version.String() || dto.Collector != nil {
		t.Errorf("status = %+v, want the version and no collector", dto)
	}
}

func TestStatusOfTheCollector(t *testing.T) {
	health := collector.NewHealth()
	health.Failed(errors.New("reading connection tracking needs root or CAP_NET_ADMIN"))
	app, _ := newTestAppWith(t, Options{Collector: health})

	var dto StatusDTO
	get(t, app, "/api/status", &dto)
	if c := dto.Collector; c == nil || c.Running || c.Error == nil || c.LastRecorded != nil || c.Warnings == nil {
		t.Errorf("collector = %+v, want it not running, with why", c)
	}
}

func TestCollectorStatusDTO(t *testing.T) {
	now := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)

	fresh := newCollectorStatusDTO(collector.HealthStatus{Running: true, LastRecorded: now.Add(-time.Minute)}, now)
	if fresh.Stale || fresh.Error != nil || fresh.LastRecorded == nil {
		t.Errorf("recorded a minute ago: %+v", fresh)
	}

	stale := newCollectorStatusDTO(collector.HealthStatus{Running: true, LastRecorded: now.Add(-10 * time.Minute)}, now)
	if !stale.Stale {
		t.Errorf("recorded 10 minutes ago: %+v, want it stale", stale)
	}
}
