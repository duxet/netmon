package api

import (
	"testing"

	"github.com/duxet/netmon/version"
	"github.com/gofiber/fiber/v3"
)

func TestStatus(t *testing.T) {
	app, _ := newTestApp(t)

	var dto StatusDTO
	if status, _ := get(t, app, "/api/status", &dto); status != fiber.StatusOK {
		t.Fatalf("GET /api/status = %d", status)
	}
	if dto.Version != version.String() {
		t.Errorf("version = %q, want %q", dto.Version, version.String())
	}
}
