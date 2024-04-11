package main

import (
	"database/sql"
	"log"

	"github.com/davecgh/go-spew/spew"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/template/html/v2"
)

func runHTTPServer(db *sql.DB) {
	app := fiber.New(fiber.Config{
		Views: html.New("./views", ".html"),
	})

	app.Get("/", func(c *fiber.Ctx) error {
		log.Println("Returning list of flows")

		rows, err := db.Query("SELECT src_ip, dst_ip, ip_proto, port, SUM(in_bytes), SUM(in_packets), SUM(out_bytes), SUM(out_packets) FROM flows GROUP BY src_ip, dst_ip, ip_proto, port")
		if err != nil {
			log.Fatal(err)
		}
		defer rows.Close()

		log.Printf("Received flows")

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
			spew.Dump(flow)
			flows = append(flows, flow)
		}

		return c.Render("index", fiber.Map{
			"flows": flows,
		})
	})

	log.Fatal(app.Listen(":2137"))
}
