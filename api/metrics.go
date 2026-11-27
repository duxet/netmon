package api

import (
	"database/sql"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/duxet/netmon/collector"
	"github.com/duxet/netmon/storage"
	"github.com/duxet/netmon/version"
	"github.com/gofiber/fiber/v3"
)

// HealthDTO tells whether netmon works: whether it can read its database and
// records traffic.
type HealthDTO struct {
	// Status is "ok", or "unhealthy" with the Problems.
	Status   string
	Problems []string
}

// registerHealthRoutes adds /healthz, which answers 200 when netmon works and
// 503 when it doesn't, for container health checks and uptime monitors. It
// needs no session, so it has to be added before the routes that do.
func registerHealthRoutes(app *fiber.App, db *sql.DB, health *collector.Health) {
	app.Get("/healthz", func(c fiber.Ctx) error {
		var problems []string

		var one int
		if err := db.QueryRowContext(c.Context(), "SELECT 1").Scan(&one); err != nil {
			problems = append(problems, "the database can't be read: "+err.Error())
		}

		if health != nil {
			status := newCollectorStatusDTO(health.Status(), time.Now())
			switch {
			case !status.Running:
				problems = append(problems, "the collector isn't running: "+deref(status.Error))
			case status.Error != nil:
				problems = append(problems, "recording traffic failed: "+*status.Error)
			case status.Stale:
				problems = append(problems, "recording traffic seems stuck: nothing recorded since "+status.LastRecorded.Format(time.RFC3339))
			}
		}

		if len(problems) > 0 {
			return c.Status(fiber.StatusServiceUnavailable).JSON(HealthDTO{Status: "unhealthy", Problems: problems})
		}
		return c.JSON(HealthDTO{Status: "ok", Problems: []string{}})
	})
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// metrics is what /metrics reports about, besides the database.
type metrics struct {
	health       *collector.Health
	totals       *collector.Totals
	deliveries   func() map[string]uint64
	databasePath string
}

// registerMetricsRoutes adds /metrics, for Prometheus.
func registerMetricsRoutes(app *fiber.App, db *sql.DB, m metrics) {
	app.Get("/metrics", func(c fiber.Ctx) error {
		var w metricsWriter

		w.family("netmon_info", "gauge", "The version of netmon that is running, as a label.")
		w.sample("netmon_info", labels{"version", version.String()}, 1)

		if m.health != nil {
			s := m.health.Status()
			w.gauge("netmon_collector_running", "Whether the collector started.", boolValue(s.Running))
			w.counter("netmon_collector_rounds_total", "Rounds of recording traffic, about one a minute.", s.Rounds)
			w.counter("netmon_collector_failed_rounds_total", "Rounds of recording traffic that failed.", s.FailedRounds)
			w.counter("netmon_collector_flows_written_total", "Flows written to the database.", s.Rows)
			w.counter("netmon_collector_event_overflows_total", "Times the kernel dropped events about connections closing.", s.EventOverflows)
			w.counter("netmon_collector_dropped_connections_total", "Closed connections left out while recording failed.", s.DroppedConnections)
			w.gauge("netmon_collector_warnings", "Problems that keep traffic from being recorded in full.", float64(len(s.Warnings)))
			if !s.LastRecorded.IsZero() {
				w.gauge("netmon_collector_last_recorded_timestamp_seconds", "When traffic was last written to the database.",
					float64(s.LastRecorded.UnixMilli())/1000)
			}
		}

		if m.databasePath != "" {
			w.gauge("netmon_database_size_bytes", "Size of the database file, with its write-ahead log.", float64(databaseSize(m.databasePath)))
		}

		if _, firing, err := storage.ListAlerts(db, storage.AlertsFilter{State: "firing", Limit: 1}); err == nil {
			w.gauge("netmon_alerts_firing", "Alerts that are firing.", float64(firing))
		} else {
			return err
		}

		if m.deliveries != nil {
			counts := m.deliveries()
			w.family("netmon_notification_deliveries_total", "counter", "Notifications about alerts, one per alert and channel, by whether they were sent.")
			for _, status := range []string{"sent", "failed", "suppressed"} {
				w.sample("netmon_notification_deliveries_total", labels{"status", status}, float64(counts[status]))
			}
		}

		if m.totals != nil {
			names, err := storage.GetDeviceNames(db)
			if err != nil {
				return err
			}
			aliases, err := storage.GetAliases(db)
			if err != nil {
				return err
			}
			devices := m.totals.Devices()
			// Merged MAC addresses count as their device.
			for device, merged := range aliases {
				for _, alias := range merged {
					if total, ok := devices[alias]; ok {
						sum := devices[device]
						sum.Received += total.Received
						sum.Sent += total.Sent
						devices[device] = sum
						delete(devices, alias)
					}
				}
			}
			macs := slices.Sorted(func(yield func(string) bool) {
				for mac := range devices {
					if !yield(mac) {
						return
					}
				}
			})

			w.family("netmon_device_received_bytes_total", "counter", "Bytes each device received, since netmon started.")
			for _, mac := range macs {
				w.sample("netmon_device_received_bytes_total", labels{"mac", mac, "name", names[mac]}, float64(devices[mac].Received))
			}
			w.family("netmon_device_sent_bytes_total", "counter", "Bytes each device sent, since netmon started.")
			for _, mac := range macs {
				w.sample("netmon_device_sent_bytes_total", labels{"mac", mac, "name", names[mac]}, float64(devices[mac].Sent))
			}
		}

		c.Set(fiber.HeaderContentType, "text/plain; version=0.0.4; charset=utf-8")
		return c.SendString(w.b.String())
	})
}

// databaseSize is the size of the database at path, with its write-ahead log.
func databaseSize(path string) int64 {
	var size int64
	for _, file := range []string{path, path + ".wal"} {
		if info, err := os.Stat(file); err == nil {
			size += info.Size()
		}
	}
	return size
}

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// labels are the names and values of a sample's labels, in pairs.
type labels []string

// metricsWriter writes metrics in Prometheus's text format.
type metricsWriter struct {
	b strings.Builder
}

func (w *metricsWriter) family(name, kind, help string) {
	w.b.WriteString("# HELP " + name + " " + help + "\n")
	w.b.WriteString("# TYPE " + name + " " + kind + "\n")
}

func (w *metricsWriter) sample(name string, l labels, value float64) {
	w.b.WriteString(name)
	if len(l) > 0 {
		w.b.WriteString("{")
		for i := 0; i+1 < len(l); i += 2 {
			if i > 0 {
				w.b.WriteString(",")
			}
			w.b.WriteString(l[i] + `="` + escapeLabel(l[i+1]) + `"`)
		}
		w.b.WriteString("}")
	}
	w.b.WriteString(" " + strconv.FormatFloat(value, 'g', -1, 64) + "\n")
}

func (w *metricsWriter) gauge(name, help string, value float64) {
	w.family(name, "gauge", help)
	w.sample(name, nil, value)
}

func (w *metricsWriter) counter(name, help string, value uint64) {
	w.family(name, "counter", help)
	w.sample(name, nil, float64(value))
}

var labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

func escapeLabel(s string) string {
	return labelEscaper.Replace(s)
}
