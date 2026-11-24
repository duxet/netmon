package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"github.com/duxet/netmon/alerting"
	"github.com/duxet/netmon/api"
	"github.com/duxet/netmon/collector"
	"github.com/duxet/netmon/live"
	"github.com/duxet/netmon/lookup"
	"github.com/duxet/netmon/notify"
	"github.com/duxet/netmon/storage"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/duckdb/duckdb-go/v2"
	migrate "github.com/rubenv/sql-migrate"
)

//go:embed migrations/*
var dbMigrations embed.FS

// listenPort is where the API and web UI are served.
const listenPort = ":2137"

// notifyShutdownTimeout bounds how long shutting down waits for queued
// notifications to be sent.
const notifyShutdownTimeout = 5 * time.Second

//go:embed all:client/dist/*
var clientAssets embed.FS

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	connector, err := duckdb.NewConnector("flows.db", func(execer driver.ExecerContext) error {
		var bootQueries []string

		for _, query := range bootQueries {
			_, err := execer.ExecContext(context.Background(), query, nil)
			if err != nil {
				log.Fatal(err)
			}
		}
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	// db, err := sql.Open("duckdb", "flows.db")
	db := sql.OpenDB(connector)
	defer db.Close()

	migrations := migrate.EmbedFileSystemMigrationSource{
		FileSystem: dbMigrations,
		Root:       "migrations",
	}

	n, err := migrate.Exec(db, "sqlite3", migrations, migrate.Up)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Applied %d migrations", n)

	retention := daysFromEnv("NETMON_RETENTION_DAYS", 0)
	alertRetention := daysFromEnv("NETMON_ALERT_RETENTION_DAYS", 30)

	// Catch up on devices in flows written before a crash, before compaction
	// rolls those rows up.
	if err := storage.SyncDevices(db, time.Time{}); err != nil {
		log.Println("Failed to update devices:", err)
	}

	compactionDone := compactPeriodically(ctx, db, retention, alertRetention)

	countries := lookup.OpenCountries("GeoLite2-Country.mmdb")
	defer countries.Close()
	hostnames := lookup.NewHostnames(envOr("NETMON_LOCAL_DNS", "192.168.1.1:54"), envOr("NETMON_PUBLIC_DNS", "8.8.8.8:53"))

	// Alert rules are evaluated after every collection round, which also
	// brings the devices up to date, and the notifications they raise are
	// sent in the background.
	dispatcher := notify.NewDispatcher(db, notify.DispatcherOptions{})
	engine := alerting.NewEngine(db, dispatcher, alerting.Options{Hostnames: hostnames, PublicURL: publicURL()})
	engineDone := make(chan struct{})
	go func() {
		defer close(engineDone)
		engine.Run(ctx)
	}()

	// The collector reports the traffic it sees to the meter as it happens, and
	// the API serves what the meter measured to the live view.
	meter := live.NewMeter()

	coll, err := collector.CollectTraffic(db, meter, collector.Options{OnFlush: engine.Trigger})
	if err != nil {
		log.Println("Collector failed to start:", err)
	}

	assets, err := fs.Sub(clientAssets, "client/dist")
	if err != nil {
		log.Fatal(err)
	}

	app := api.CreateHTTPApp(db, assets, api.Options{Countries: countries, Hostnames: hostnames, Live: meter})

	go func() {
		<-ctx.Done()
		log.Println("Shutting down gracefully")

		if coll != nil {
			coll.Shutdown()
		}
		// Let a running evaluation finish, then send what it raised.
		<-engineDone
		dispatcher.Close(notifyShutdownTimeout)

		_ = app.ShutdownWithContext(context.Background())
	}()

	// Listen returns nil after a graceful shutdown; returning normally (rather
	// than log.Fatal) lets the deferred db.Close run.
	if err := app.Listen(listenPort); err != nil {
		log.Fatal(err)
	}

	<-compactionDone
}

// compactPeriodically rolls per-minute flows up into hourly rows and applies
// the retention of flows and alerts, at startup and then every hour, until
// ctx is done. The returned channel is closed once a compaction in progress
// has finished.
func compactPeriodically(ctx context.Context, db *sql.DB, retention, alertRetention time.Duration) <-chan struct{} {
	done := make(chan struct{})

	compact := func() {
		now := time.Now()
		result, err := storage.Compact(db, now, retention)
		if err != nil {
			log.Println("Failed to compact flows:", err)
		} else if result != (storage.CompactResult{}) {
			log.Printf("Compacted flows: %d rows rolled up into %d hourly rows, %d expired",
				result.Removed, result.RolledUp, result.Expired)
		}

		expired, err := storage.ExpireAlerts(db, now, alertRetention)
		if err != nil {
			log.Println("Failed to expire alerts:", err)
		} else if expired > 0 {
			log.Printf("Expired %d resolved alerts", expired)
		}
	}

	go func() {
		defer close(done)
		compact()

		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				compact()
			}
		}
	}()

	return done
}

// daysFromEnv reads a number of days from an environment variable, with 0
// for "keep everything", and stops netmon when it isn't one.
func daysFromEnv(name string, fallback int) time.Duration {
	days, err := strconv.Atoi(envOr(name, strconv.Itoa(fallback)))
	if err != nil || days < 0 {
		log.Fatalf("%s must be a number of days (0 keeps everything): %q", name, os.Getenv(name))
	}
	return time.Duration(days) * 24 * time.Hour
}

// publicURL is where people reach the web UI, for links in notifications:
// NETMON_PUBLIC_URL, or the machine's hostname on netmon's port.
func publicURL() string {
	if url := os.Getenv("NETMON_PUBLIC_URL"); url != "" {
		return strings.TrimSuffix(url, "/")
	}
	hostname, err := os.Hostname()
	if err != nil {
		return ""
	}
	return "http://" + hostname + listenPort
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
