package main

import (
	"database/sql"
	"github.com/duxet/netmon/storage"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/template/html/v2"
	"log"
)

type Pagination struct {
	Limit  uint32
	Offset uint32
}

func createHTTPServer(db *sql.DB) *fiber.App {
	app := fiber.New(fiber.Config{
		Views: html.New("./views", ".html"),
	})

	app.Get("/api/flows", func(c *fiber.Ctx) error {
		log.Println("Returning list of flows")
		flows := storage.GetFlows(db)

		return c.JSON(flows)
	})

	app.Get("/api/stats", func(c *fiber.Ctx) error {
		log.Println("Returning stats")
		stats := storage.GetStats(db)

		return c.JSON(stats)
	})

	app.Get("/api/clients", func(c *fiber.Ctx) error {
		log.Println("Returning list of clients")
		clients := storage.GetClients(db)

		return c.JSON(clients)
	})

	app.Get("/api/clients/:clientId/flows", func(c *fiber.Ctx) error {
		log.Println("Returning list of client flows")
		clientId := c.Params("clientId")
		flows := storage.GetClientFlows(db, clientId)

		return c.JSON(flows)
	})

	return app
}
