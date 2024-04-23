package api

import (
	"context"
	"database/sql"
	"github.com/duxet/netmon/common"
	"github.com/duxet/netmon/storage"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/template/html/v2"
	"log"
	"net"
)

type Pagination struct {
	Limit  uint32
	Offset uint32
}

func getHostname(ipAddress common.IPAddress) string {
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{}
			return d.DialContext(ctx, "udp", "8.8.8.8:53")
		},
	}

	var hostname string

	if ipAddress.IsPrivate() {
		hostname = "missing"
	} else {
		hostnames, _ := r.LookupAddr(context.Background(), ipAddress.String())

		if len(hostnames) > 0 {
			hostname = hostnames[0]
		}
	}

	return hostname
}

func CreateHTTPApp(db *sql.DB) *fiber.App {
	app := fiber.New(fiber.Config{
		Views: html.New("./views", ".html"),
	})

	app.Get("/api/flows", func(c *fiber.Ctx) error {
		log.Println("Returning list of flows")
		records := storage.GetFlows(db)

		var flows []FlowDTO

		for _, record := range records {
			flow := FlowDTO{
				Source: EndpointDTO{
					MACAddress: record.SourceMACAddress,
					IPAddress:  record.SourceIPAddress,
					Hostname:   nil,
				},
				Destination: EndpointDTO{
					MACAddress: record.DestinationMACAddress,
					IPAddress:  record.DestinationIPAddress,
					Hostname:   nil,
				},
				IPProto: record.IPProto,
				Port:    record.Port,
				Traffic: TrafficDTO{
					InBytes:    record.InBytes,
					InPackets:  record.InPackets,
					OutBytes:   record.OutBytes,
					OutPackets: record.OutPackets,
				},
			}
			flows = append(flows, flow)
		}

		return c.JSON(flows)
	})

	app.Get("/api/stats", func(c *fiber.Ctx) error {
		log.Println("Returning stats")
		record := storage.GetStats(db)
		stats := StatsDTO{
			TotalClients: record.TotalClients,
			TotalBytes:   record.TotalBytes,
			TotalPackets: record.TotalPackets,
		}

		return c.JSON(stats)
	})

	app.Get("/api/clients", func(c *fiber.Ctx) error {
		log.Println("Returning list of clients")
		records := storage.GetClients(db)

		var clients []ClientDTO

		for _, record := range records {
			client := ClientDTO{
				Endpoint: EndpointDTO{
					MACAddress: record.SourceMACAddress,
					IPAddress:  record.SourceIPAddress,
					Hostname:   nil,
				},
				Traffic: TrafficDTO{
					InBytes:    record.InBytes,
					InPackets:  record.InPackets,
					OutBytes:   record.OutBytes,
					OutPackets: record.OutPackets,
				},
			}
			clients = append(clients, client)
		}

		return c.JSON(clients)
	})

	app.Get("/api/clients/:clientId/flows", func(c *fiber.Ctx) error {
		log.Println("Returning list of client flows")
		clientId := c.Params("clientId")
		records := storage.GetClientFlows(db, clientId)

		var flows []FlowDTO
		for _, record := range records {
			sourceHostname := getHostname(record.SourceIPAddress)
			destinationHostname := getHostname(record.DestinationIPAddress)
			flow := FlowDTO{
				Source: EndpointDTO{
					MACAddress: record.SourceMACAddress,
					IPAddress:  record.SourceIPAddress,
					Hostname:   &sourceHostname,
				},
				Destination: EndpointDTO{
					MACAddress: record.DestinationMACAddress,
					IPAddress:  record.DestinationIPAddress,
					Hostname:   &destinationHostname,
				},
				IPProto: record.IPProto,
				Port:    record.Port,
				Traffic: TrafficDTO{
					InBytes:    record.InBytes,
					InPackets:  record.InPackets,
					OutBytes:   record.OutBytes,
					OutPackets: record.OutPackets,
				},
			}
			flows = append(flows, flow)
		}

		return c.JSON(flows)
	})

	return app
}
