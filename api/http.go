package api

import (
	"database/sql"
	"io/fs"
	"log/slog"
	"net/netip"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/duxet/netmon/alerting"
	"github.com/duxet/netmon/apps"
	"github.com/duxet/netmon/auth"
	"github.com/duxet/netmon/collector"
	"github.com/duxet/netmon/common"
	"github.com/duxet/netmon/config"
	"github.com/duxet/netmon/live"
	"github.com/duxet/netmon/lookup"
	"github.com/duxet/netmon/storage"
	"github.com/duxet/netmon/vendors"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/static"
)

// Options configures the HTTP API's lookups and live view. The caller owns
// them, and closes the lookups after the app has shut down.
type Options struct {
	// Countries resolves addresses to countries. Without it, flows have no
	// countries.
	Countries *lookup.Countries
	// ASNs resolves addresses to autonomous systems. Without it, flows have
	// no autonomous systems.
	ASNs *lookup.ASNs
	// Classifier recognizes the apps remote hosts belong to. Without it, the
	// default one is used.
	Classifier *apps.Classifier
	// Hostnames does reverse DNS lookups. Without it, the system resolver
	// is used for all addresses.
	Hostnames *lookup.Hostnames
	// Live is the meter the collectors report the current bandwidth of
	// devices to, for /api/live. Without one the live view stays empty.
	Live *live.Meter
	// Alerts is the alert engine, which previews rules. Without one, the
	// API makes its own for previews.
	Alerts *alerting.Engine
	// Auth signs people in. With it, the API and the web UI need a session;
	// without it, anyone who can reach netmon can use all of it.
	Auth *auth.Authenticator
	// Config saves settings to the YAML file. Without it, /api/config
	// isn't served.
	Config *config.Editor
	// Collector tells how recording traffic goes. Without it, /api/status
	// has no collector and /healthz doesn't check it.
	Collector *collector.Health
	// Totals adds up the traffic of each device, for /metrics. Without it,
	// /metrics has no devices.
	Totals *collector.Totals
	// Deliveries counts the notifications sent, failed and suppressed, for
	// /metrics. Without it, /metrics has no notifications.
	Deliveries func() map[string]uint64
	// DatabasePath is the database file, whose size /metrics reports.
	DatabasePath string
}

// maxHostnamesPerRequest bounds the work a single /api/hostnames call can cause.
const maxHostnamesPerRequest = 256

// totalCountHeader carries the number of flows matching a paged request.
const totalCountHeader = "X-Total-Count"

func toFlowDTOs(flows []orientedFlow, countries *lookup.Countries, asns *lookup.ASNs) []FlowDTO {
	var dtos []FlowDTO

	for _, flow := range flows {
		record := flow.record
		dtos = append(dtos, FlowDTO{
			Source: EndpointDTO{
				MACAddress: record.SourceMACAddress,
				IPAddress:  record.SourceIPAddress,
				Country:    countries.Country(record.SourceIPAddress),
				ASN:        asnOf(asns, record.SourceIPAddress),
			},
			Destination: EndpointDTO{
				MACAddress: record.DestinationMACAddress,
				IPAddress:  record.DestinationIPAddress,
				Country:    countries.Country(record.DestinationIPAddress),
				ASN:        asnOf(asns, record.DestinationIPAddress),
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
		slog.Warn("Web UI not available (build it with `npm run build` in client/)", "err", err)
	}

	return func(c fiber.Ctx) error {
		if index == nil || path.Ext(c.Path()) != "" {
			return fiber.ErrNotFound
		}
		c.Type("html")
		return c.Status(fiber.StatusOK).Send(index)
	}
}

// logRequests logs every request, at the debug level.
func logRequests(c fiber.Ctx) error {
	if !slog.Default().Enabled(c.Context(), slog.LevelDebug) {
		return c.Next()
	}

	start := time.Now()
	err := c.Next()
	attrs := []any{"method", c.Method(), "url", c.OriginalURL(), "status", c.Response().StatusCode(), "duration", time.Since(start)}
	if err != nil {
		attrs = append(attrs, "err", err)
	}
	slog.Debug("Request", attrs...)
	return err
}

// CreateHTTPApp serves the API and the web UI, whose built files assets holds
// at its root.
func CreateHTTPApp(db *sql.DB, assets fs.FS, options Options) *fiber.App {
	countries := options.Countries
	asns := options.ASNs
	classifier := options.Classifier
	if classifier == nil {
		classifier = apps.Default()
	}
	hostnames := options.Hostnames
	if hostnames == nil {
		hostnames = lookup.NewHostnames("system", "system")
	}

	app := fiber.New(fiber.Config{ErrorHandler: errorHandler})

	app.Use(cors.New(cors.Config{ExposeHeaders: []string{totalCountHeader}}))
	app.Use(logRequests)

	registerHealthRoutes(app, db, options.Collector)

	// Everything registered after this needs a session.
	if options.Auth != nil {
		options.Auth.Mount(app)
	}

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

		flows, total := pageFlows(orientFlows(records, filter), page, hasSubject(filter))
		c.Set(totalCountHeader, strconv.Itoa(total))

		return c.JSON(toFlowDTOs(flows, countries, asns))
	}

	app.Get("/api/flows", func(c fiber.Ctx) error {
		filter, err := parseFlowsFilter(c, db)
		if err != nil {
			return err
		}

		return serveFlows(c, filter)
	})

	// Totals and the largest remote hosts, services and countries of the
	// flows matching the filter, seen from the filtered client.
	app.Get("/api/flows/summary", func(c fiber.Ctx) error {
		filter, err := parseFlowsFilter(c, db)
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

		return c.JSON(summarizeFlows(orientFlows(records, filter), top, countries, asns))
	})

	app.Get("/api/stats", func(c fiber.Ctx) error {
		filter, err := parseFlowsFilter(c, db)
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
		filter, err := parseFlowsFilter(c, db)
		if err != nil {
			return err
		}

		records, err := storage.GetEndpointPairTraffic(db, filter)
		if err != nil {
			return err
		}

		return c.JSON(aggregateAppTraffic(classifier, records))
	})

	// Traffic per hour. With mac or ip it is counted from that client's
	// side; with app, only traffic to that app is counted.
	app.Get("/api/traffic-measurements", func(c fiber.Ctx) error {
		filter, err := parseFlowsFilter(c, db)
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

			return c.JSON(appTrafficByHour(classifier, records, app))
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
		filter, err := parseFlowsFilter(c, db)
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
		deviceIcons, err := storage.GetDeviceIcons(db)
		if err != nil {
			return err
		}

		matcher, err := storage.NetworkMatcher(db)
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

			var name, icon *string
			if n, ok := deviceNames[record.MACAddress.String()]; ok {
				name = &n
			}
			if i, ok := deviceIcons[record.MACAddress.String()]; ok {
				icon = &i
			}

			var vendor *string
			if v := vendors.Lookup(record.MACAddress.HardwareAddr); v != "" {
				vendor = &v
			}

			clients = append(clients, ClientDTO{
				Name:      name,
				Icon:      icon,
				Hostname:  hostname,
				Vendor:    vendor,
				NetworkID: networkIDOf(matcher, record.IPAddress),
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
		filter, err := parseClientFilter(c, c.Params("clientId"))
		if err != nil {
			return err
		}

		return serveFlows(c, filter)
	})

	registerSessionRoutes(app, options.Auth != nil)
	registerStatusRoutes(app, options.Collector)
	registerMetricsRoutes(app, db, metrics{
		health:       options.Collector,
		totals:       options.Totals,
		deliveries:   options.Deliveries,
		databasePath: options.DatabasePath,
	})
	registerDeviceRoutes(app, db, hostnames)
	registerNetworkRoutes(app, db)
	if options.Config != nil {
		registerConfigRoutes(app, options.Config)
	}
	registerNotificationRoutes(app, db)
	alerts := options.Alerts
	if alerts == nil {
		alerts = alerting.NewEngine(db, nil, alerting.Options{Hostnames: hostnames, Countries: countries})
	}
	registerAlertRoutes(app, db, alerts, countries)

	meter := options.Live
	if meter == nil {
		meter = live.NewMeter()
	}
	registerLiveRoutes(app, meter)

	type HostnamesBody struct {
		IPAddresses []string `json:"ip_addresses"`
	}

	app.Post("/api/hostnames", func(c fiber.Ctx) error {
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
