package api

import (
	"context"
	"database/sql"
	"embed"
	"github.com/duxet/netmon/common"
	"github.com/duxet/netmon/storage"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
	"github.com/oschwald/geoip2-golang"
	"log"
	"net"
	"net/http"
)

type Pagination struct {
	Limit  uint32
	Offset uint32
}

func getCountry(ipAddress common.IPAddress) *string {
	db, err := geoip2.Open("GeoLite2-Country.mmdb")
	if err != nil {
		log.Println("Failed to open GeoIP2-Country.mmdb", err)
		return nil
	}

	country, err := db.Country(ipAddress.AsSlice())
	if err != nil {
		log.Println("Failed to get country", err)
	}

	return &country.Country.IsoCode
}

func getHostname(ipAddress common.IPAddress) *string {
	localResolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{}
			return d.DialContext(ctx, "udp", "192.168.1.1:54")
		},
	}
	globalResolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{}
			return d.DialContext(ctx, "udp", "8.8.8.8:53")
		},
	}

	var hostnames []string

	if ipAddress.IsPrivate() {
		hostnames, _ = localResolver.LookupAddr(context.Background(), ipAddress.String())
	} else {
		hostnames, _ = globalResolver.LookupAddr(context.Background(), ipAddress.String())
	}

	if len(hostnames) > 0 {
		return &hostnames[0]
	}

	return nil
}

func CreateHTTPApp(db *sql.DB, clientAssets embed.FS) *fiber.App {
	app := fiber.New()

	app.Use(cors.New())

	app.Use("/", filesystem.New(filesystem.Config{
		Root:       http.FS(clientAssets),
		PathPrefix: "client/dist",
		Browse:     true,
	}))

	app.Get("/api/flows", func(c *fiber.Ctx) error {
		log.Println("Returning list of flows")

		var mac *common.MACAddress

		if c.Query("mac") != "" {
			mac, _ = common.ParseMACAddress(c.Query("mac"))
		}

		var ip *common.IPAddress

		if c.Query("ip") != "" {
			ip, _ = common.ParseIPAddress(c.Query("ip"))
		}

		records := storage.GetFlows(db, storage.FlowsFilter{
			MAC: mac,
			IP:  ip,
		})

		var flows []FlowDTO

		for _, record := range records {
			flow := FlowDTO{
				Source: EndpointDTO{
					MACAddress: record.SourceMACAddress,
					IPAddress:  record.SourceIPAddress,
					Hostname:   getHostname(record.SourceIPAddress),
					Country:    getCountry(record.SourceIPAddress),
				},
				Destination: EndpointDTO{
					MACAddress: record.DestinationMACAddress,
					IPAddress:  record.DestinationIPAddress,
					Hostname:   getHostname(record.DestinationIPAddress),
					Country:    getCountry(record.DestinationIPAddress),
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

	app.Get("/api/traffic-measurements", func(c *fiber.Ctx) error {
		log.Println("Returning traffic measurements")
		records := storage.GetTrafficMeasurements(db)

		var trafficMeasurements []TrafficMeasurementDTO

		for _, record := range records {
			trafficMeasurement := TrafficMeasurementDTO{
				InBytes:  record.InBytes,
				OutBytes: record.OutBytes,
				Date:     record.Date,
			}
			trafficMeasurements = append(trafficMeasurements, trafficMeasurement)
		}

		return c.JSON(trafficMeasurements)
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
					Hostname:   getHostname(record.SourceIPAddress),
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
			flow := FlowDTO{
				Source: EndpointDTO{
					MACAddress: record.SourceMACAddress,
					IPAddress:  record.SourceIPAddress,
					Hostname:   getHostname(record.SourceIPAddress),
				},
				Destination: EndpointDTO{
					MACAddress: record.DestinationMACAddress,
					IPAddress:  record.DestinationIPAddress,
					Hostname:   getHostname(record.DestinationIPAddress),
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
