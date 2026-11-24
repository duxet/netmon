package api

import (
	"database/sql"
	"io/fs"
	"log"
	"net/netip"
	"path"
	"strings"

	"github.com/duxet/netmon/apps"
	"github.com/duxet/netmon/common"
	"github.com/duxet/netmon/storage"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/static"
)

type Pagination struct {
	Limit  uint32
	Offset uint32
}

// Options configures the HTTP API's lookups.
type Options struct {
	// GeoIPDatabase is the path to a GeoLite2-Country database. Without it,
	// flows have no countries.
	GeoIPDatabase string
	// LocalDNS and PublicDNS are the servers used for reverse lookups of
	// local and public addresses: "host", "host:port" or "system".
	LocalDNS  string
	PublicDNS string
}

// maxHostnamesPerRequest bounds the work a single /api/hostnames call can cause.
const maxHostnamesPerRequest = 256

func toFlowDTOs(records []storage.FlowRecord, countries *countryLookup) []FlowDTO {
	var flows []FlowDTO

	for _, record := range records {
		flows = append(flows, FlowDTO{
			Source: EndpointDTO{
				MACAddress: record.SourceMACAddress,
				IPAddress:  record.SourceIPAddress,
				Country:    countries.Country(record.SourceIPAddress),
			},
			Destination: EndpointDTO{
				MACAddress: record.DestinationMACAddress,
				IPAddress:  record.DestinationIPAddress,
				Country:    countries.Country(record.DestinationIPAddress),
			},
			IPProto: record.IPProto,
			Port:    record.Port,
			Traffic: TrafficDTO{
				InBytes:    record.InBytes,
				InPackets:  record.InPackets,
				OutBytes:   record.OutBytes,
				OutPackets: record.OutPackets,
			},
		})
	}

	return flows
}

// spaFallback serves the app shell for client-side routes such as
// /clients/:id, which have no file of their own. Missing files (paths with
// an extension, e.g. a stale /assets/index-1234.js) stay 404s rather than
// getting HTML a browser can't use.
func spaFallback(assets fs.FS) fiber.Handler {
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		log.Printf("Web UI not available (build it with `npm run build` in client/): %v", err)
	}

	return func(c fiber.Ctx) error {
		if index == nil || path.Ext(c.Path()) != "" {
			return fiber.ErrNotFound
		}
		c.Type("html")
		return c.Status(fiber.StatusOK).Send(index)
	}
}

// CreateHTTPApp serves the API and the web UI, whose built files assets holds
// at its root.
func CreateHTTPApp(db *sql.DB, assets fs.FS, options Options) *fiber.App {
	countries := newCountryLookup(options.GeoIPDatabase)
	hostnames := newHostnameResolver(options.LocalDNS, options.PublicDNS)

	app := fiber.New(fiber.Config{ErrorHandler: errorHandler})
	app.Hooks().OnPostShutdown(func(error) error { return countries.Close() })

	app.Use(cors.New())

	app.Get("/api/flows", func(c fiber.Ctx) error {
		log.Println("Returning list of flows")

		filter, err := parseFlowsFilter(c)
		if err != nil {
			return err
		}

		records, err := storage.GetFlows(db, filter)
		if err != nil {
			return err
		}

		return c.JSON(toFlowDTOs(records, countries))
	})

	app.Get("/api/stats", func(c fiber.Ctx) error {
		log.Println("Returning stats")

		record, err := storage.GetStats(db)
		if err != nil {
			return err
		}

		return c.JSON(StatsDTO{
			TotalClients: record.TotalClients,
			TotalBytes:   record.TotalBytes,
			TotalPackets: record.TotalPackets,
		})
	})

	app.Get("/api/apps", func(c fiber.Ctx) error {
		log.Println("Returning traffic per app")

		filter, err := parseFlowsFilter(c)
		if err != nil {
			return err
		}

		records, err := storage.GetEndpointPairTraffic(db, filter)
		if err != nil {
			return err
		}

		return c.JSON(aggregateAppTraffic(apps.Default(), records))
	})

	app.Get("/api/traffic-measurements", func(c fiber.Ctx) error {
		log.Println("Returning traffic measurements")

		records, err := storage.GetTrafficMeasurements(db)
		if err != nil {
			return err
		}

		var trafficMeasurements []TrafficMeasurementDTO
		for _, record := range records {
			trafficMeasurements = append(trafficMeasurements, TrafficMeasurementDTO{
				InBytes:  record.InBytes,
				OutBytes: record.OutBytes,
				Date:     record.Date,
			})
		}

		return c.JSON(trafficMeasurements)
	})

	app.Get("/api/clients", func(c fiber.Ctx) error {
		log.Println("Returning list of clients")

		records, err := storage.GetClients(db)
		if err != nil {
			return err
		}

		addrs := make([]netip.Addr, 0, len(records))
		for _, record := range records {
			if record.IPAddress.Addr != nil {
				addrs = append(addrs, *record.IPAddress.Addr)
			}
		}
		names := hostnames.LookupAll(c.Context(), addrs)

		var clients []ClientDTO
		for _, record := range records {
			var hostname *string
			if record.IPAddress.Addr != nil {
				hostname = names[record.IPAddress.Unmap()]
			}

			clients = append(clients, ClientDTO{
				Hostname: hostname,
				Endpoint: EndpointDTO{
					MACAddress: record.MACAddress,
					IPAddress:  record.IPAddress,
				},
				Traffic: TrafficDTO{
					InBytes:    record.InBytes,
					InPackets:  record.InPackets,
					OutBytes:   record.OutBytes,
					OutPackets: record.OutPackets,
				},
			})
		}

		return c.JSON(clients)
	})

	// Same as /api/flows, for a client identified by MAC or IP address.
	app.Get("/api/clients/:clientId/flows", func(c fiber.Ctx) error {
		log.Println("Returning list of client flows")

		filter, err := parseClientID(c.Params("clientId"))
		if err != nil {
			return err
		}

		records, err := storage.GetFlows(db, filter)
		if err != nil {
			return err
		}

		return c.JSON(toFlowDTOs(records, countries))
	})

	type HostnamesBody struct {
		IPAddresses []string `json:"ip_addresses"`
	}

	app.Post("/api/hostnames", func(c fiber.Ctx) error {
		log.Println("Returning hostnames")

		r := new(HostnamesBody)
		if err := c.Bind().Body(r); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "invalid request body: "+err.Error())
		}

		if len(r.IPAddresses) > maxHostnamesPerRequest {
			return fiber.NewError(fiber.StatusBadRequest, "too many ip addresses in one request")
		}

		var addrs []common.IPAddress
		lookup := make([]netip.Addr, 0, len(r.IPAddresses))
		for _, ipAddressString := range r.IPAddresses {
			if ipAddress, err := common.ParseIPAddress(ipAddressString); err == nil {
				addrs = append(addrs, *ipAddress)
				lookup = append(lookup, *ipAddress.Addr)
			}
		}
		names := hostnames.LookupAll(c.Context(), lookup)

		var result []HostnameDTO
		for _, addr := range addrs {
			result = append(result, HostnameDTO{
				IPAddress: addr,
				Hostname:  names[addr.Unmap()],
			})
		}

		return c.JSON(result)
	})

	// The web UI, registered after the API so that API routes match first.
	// Unknown /api/ paths are left to the default 404.
	app.Get("/*", static.New("", static.Config{
		FS: assets,
		Next: func(c fiber.Ctx) bool {
			return strings.HasPrefix(c.Path(), "/api/")
		},
		NotFoundHandler: spaFallback(assets),
	}))

	return app
}
