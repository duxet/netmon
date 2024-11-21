package api

import (
	"database/sql"
	"embed"
	"github.com/duxet/netmon/common"
	"github.com/duxet/netmon/storage"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
	"log"
	"net/http"
)

type Pagination struct {
	Limit  uint32
	Offset uint32
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

		var clientID *common.ClientID

		if c.Query("clientID") != "" {
			parsedID, _ := common.ParseClientID(c.Query("clientID"))
			clientID = &parsedID
		}

		var ip *common.IPAddress

		if c.Query("ip") != "" {
			ip, _ = common.ParseIPAddress(c.Query("ip"))
		}

		records := storage.GetFlows(db, storage.FlowsFilter{
			ClientID: clientID,
			IP:       ip,
		})

		var flows []FlowDTO

		for _, record := range records {
			flow := FlowDTO{
				ClientID: record.ClientID,
				LocalIP:  record.LocalIP,
				RemoteIP: record.RemoteIP,
				Country:  common.GetCountryCode(record.RemoteIP),
				IPProto:  record.IPProto,
				Port:     record.Port,
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
				ID:          record.ID,
				Hostname:    record.Hostname,
				MACAddress:  record.MACAddress,
				IPAddresses: record.IPAddresses,
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
		clientId, _ := common.ParseClientID(c.Params("clientId"))
		filter := storage.FlowsFilter{ClientID: &clientId}
		records := storage.GetFlows(db, filter)

		var flows []FlowDTO
		for _, record := range records {
			flow := FlowDTO{
				ClientID: record.ClientID,
				LocalIP:  record.LocalIP,
				RemoteIP: record.RemoteIP,
				Country:  common.GetCountryCode(record.RemoteIP),
				IPProto:  record.IPProto,
				Port:     record.Port,
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

	type HostnamesBody struct {
		IPAddresses []string `json:"ip_addresses"`
	}

	app.Post("/api/hostnames", func(c *fiber.Ctx) error {
		log.Println("Returning hostnames")

		r := new(HostnamesBody)
		if err := c.BodyParser(r); err != nil {
			return err
		}

		var hostnames []HostnameDTO

		for _, ipAddressString := range r.IPAddresses {
			if ipAddress, err := common.ParseIPAddress(ipAddressString); err == nil {
				hostnames = append(hostnames, HostnameDTO{
					IPAddress: *ipAddress,
					Hostname:  common.GetHostname(*ipAddress),
				})
			}
		}

		return c.JSON(hostnames)
	})

	return app
}
