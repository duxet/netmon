package api

import (
	"database/sql"
	"io/fs"
	"log"
	"net/netip"
	"path"
	"strconv"
	"strings"

	"github.com/duxet/netmon/apps"
	"github.com/duxet/netmon/common"
	"github.com/duxet/netmon/storage"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/static"
)

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

// totalCountHeader carries the number of flows matching a paged request.
const totalCountHeader = "X-Total-Count"

func toFlowDTOs(flows []orientedFlow, countries *countryLookup) []FlowDTO {
	var dtos []FlowDTO

	for _, flow := range flows {
		record := flow.record
		dtos = append(dtos, FlowDTO{
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

	return dtos
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

	app.Use(cors.New(cors.Config{ExposeHeaders: []string{totalCountHeader}}))

	// serveFlows answers with one page of the flows matching filter, and
	// their total number in the X-Total-Count header.
	serveFlows := func(c fiber.Ctx, filter storage.FlowsFilter) error {
		page, err := parseFlowsPage(c)
		if err != nil {
			return err
		}

		records, err := storage.GetFlows(db, filter)
		if err != nil {
			return err
		}

		hasClient := filter.MAC != nil || filter.IP != nil
		flows, total := pageFlows(orientFlows(records, filter), page, hasClient)
		c.Set(totalCountHeader, strconv.Itoa(total))

		return c.JSON(toFlowDTOs(flows, countries))
	}

	app.Get("/api/flows", func(c fiber.Ctx) error {
		log.Println("Returning list of flows")

		filter, err := parseFlowsFilter(c)
		if err != nil {
			return err
		}

		return serveFlows(c, filter)
	})

	// Totals and the largest remote hosts, services and countries of the
	// flows matching the filter, seen from the filtered client.
	app.Get("/api/flows/summary", func(c fiber.Ctx) error {
		log.Println("Returning flow summary")

		filter, err := parseFlowsFilter(c)
		if err != nil {
			return err
		}

		top, err := parseInt(c, "top", defaultTopGroups, 1, maxTopGroups)
		if err != nil {
			return err
		}

		records, err := storage.GetFlows(db, filter)
		if err != nil {
			return err
		}

		return c.JSON(summarizeFlows(orientFlows(records, filter), top, countries))
	})

	app.Get("/api/stats", func(c fiber.Ctx) error {
		log.Println("Returning stats")

		filter, err := parseFlowsFilter(c)
		if err != nil {
			return err
		}

		record, err := storage.GetStats(db, filter)
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

	// Traffic per hour. With mac or ip it is counted from that client's
	// side; with app, only traffic to that app is counted.
	app.Get("/api/traffic-measurements", func(c fiber.Ctx) error {
		log.Println("Returning traffic measurements")

		filter, err := parseFlowsFilter(c)
		if err != nil {
			return err
		}

		if app := c.Query("app"); app != "" {
			if !isAppName(app) {
				return badRequest("unknown app: " + app)
			}

			records, err := storage.GetEndpointPairTrafficByHour(db, filter)
			if err != nil {
				return err
			}

			return c.JSON(appTrafficByHour(apps.Default(), records, app))
		}

		records, err := storage.GetTrafficMeasurements(db, filter)
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

		filter, err := parseFlowsFilter(c)
		if err != nil {
			return err
		}

		records, err := storage.GetClients(db, filter)
		if err != nil {
			return err
		}

		deviceNames, err := storage.GetDeviceNames(db)
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

			var name *string
			if n, ok := deviceNames[record.MACAddress.String()]; ok {
				name = &n
			}

			clients = append(clients, ClientDTO{
				Name:     name,
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

		filter, err := parseClientFilter(c, c.Params("clientId"))
		if err != nil {
			return err
		}

		return serveFlows(c, filter)
	})

	registerDeviceRoutes(app, db)

	type HostnamesBody struct {
		IPAddresses []string `json:"ip_addresses"`
	}

	app.Post("/api/hostnames", func(c fiber.Ctx) error {
		log.Println("Returning hostnames")

		r := new(HostnamesBody)
		if err := c.Bind().Body(r); err != nil {
			return badRequest("invalid request body: " + err.Error())
		}

		if len(r.IPAddresses) > maxHostnamesPerRequest {
			return badRequest("too many ip addresses in one request")
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
