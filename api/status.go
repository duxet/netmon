package api

import (
	"github.com/duxet/netmon/version"
	"github.com/gofiber/fiber/v3"
)

// registerStatusRoutes adds the endpoint that tells how netmon is doing.
func registerStatusRoutes(app *fiber.App) {
	app.Get("/api/status", func(c fiber.Ctx) error {
		return c.JSON(StatusDTO{Version: version.String()})
	})
}
