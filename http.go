package main

import (
	"database/sql"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/template/html/v2"
	"log"
)

type Pagination struct {
	Limit  uint32
	Offset uint32
}

func runHTTPServer(db *sql.DB) {
	app := fiber.New(fiber.Config{
		Views: html.New("./views", ".html"),
	})

	app.Get("/api/flows", func(c *fiber.Ctx) error {
		log.Println("Returning list of flows")

		rows, err := db.Query("SELECT src_ip, dst_ip, ip_proto, port, SUM(in_bytes), SUM(in_packets), SUM(out_bytes), SUM(out_packets) FROM flows GROUP BY src_ip, dst_ip, ip_proto, port ORDER BY created_at DESC")
		if err != nil {
			log.Fatal(err)
		}
		defer rows.Close()

		var flows []Flow

		for rows.Next() {
			var flow Flow
			if err := rows.Scan(
				&flow.SourceIPAddress,
				&flow.DestinationIPAddress,
				&flow.IPProto,
				&flow.Port,
				&flow.InBytes,
				&flow.InPackets,
				&flow.OutBytes,
				&flow.OutPackets,
			); err != nil {
				log.Fatal(err)
			}
			flows = append(flows, flow)
		}

		return c.JSON(flows)
	})

	app.Get("/api/stats", func(c *fiber.Ctx) error {
		log.Println("Returning stats")

		row := db.QueryRow("SELECT count(DISTINCT src_ip), sum(in_bytes + out_bytes), sum(in_packets + out_packets) FROM flows")

		var stats Stats
		if err := row.Scan(
			&stats.TotalClients,
			&stats.TotalBytes,
			&stats.TotalPackets,
		); err != nil {
			log.Fatal(err)
		}

		return c.JSON(stats)
	})

	/*
		app.Get("/api/clients", func(c *fiber.Ctx) error {
			log.Println("Returning list of clients")

			rows, err := db.Query("SELECT src_ip, SUM(in_bytes), SUM(in_packets), SUM(out_bytes), SUM(out_packets) FROM flows GROUP BY src_ip ORDER BY SUM(in_bytes + out_bytes) DESC")
			if err != nil {
				log.Fatal(err)
			}
			defer rows.Close()

		})


	*/

	log.Fatal(app.Listen(":2137"))
}
