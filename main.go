package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"github.com/duxet/netmon/api"
	"github.com/duxet/netmon/collector"
	"github.com/duxet/netmon/live"
	"github.com/duxet/netmon/lookup"
	"github.com/duxet/netmon/storage"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/duckdb/duckdb-go/v2"
	migrate "github.com/rubenv/sql-migrate"
)

//go:embed migrations/*
var dbMigrations embed.FS

// deviceSyncWindow is how far back each collection round looks for devices.
const deviceSyncWindow = 5 * time.Minute

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

	retentionDays, err := strconv.Atoi(envOr("NETMON_RETENTION_DAYS", "0"))
	if err != nil || retentionDays < 0 {
		log.Fatalf("NETMON_RETENTION_DAYS must be a number of days (0 keeps all data): %q", os.Getenv("NETMON_RETENTION_DAYS"))
	}

	// Catch up on devices in flows written before a crash, before compaction
	// rolls those rows up.
	if err := storage.SyncDevices(db, time.Time{}); err != nil {
		log.Println("Failed to update devices:", err)
	}

	compactionDone := compactPeriodically(ctx, db, time.Duration(retentionDays)*24*time.Hour)

	// The collector reports the traffic it sees to the meter as it happens, and
	// the API serves what the meter measured to the live view.
	meter := live.NewMeter()

	coll, err := collector.CollectTraffic(db, meter, collector.Options{
		OnFlush: func(now time.Time) {
			// The window overlaps earlier rounds, so a failed sync is caught
			// up on by the next ones.
			if err := storage.SyncDevices(db, now.Add(-deviceSyncWindow)); err != nil {
				log.Println("Failed to update devices:", err)
			}
		},
	})
	if err != nil {
		log.Println("Collector failed to start:", err)
	}

	countries := lookup.OpenCountries("GeoLite2-Country.mmdb")
	defer countries.Close()
	hostnames := lookup.NewHostnames(envOr("NETMON_LOCAL_DNS", "192.168.1.1:54"), envOr("NETMON_PUBLIC_DNS", "8.8.8.8:53"))

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

		_ = app.ShutdownWithContext(context.Background())
	}()

	// Listen returns nil after a graceful shutdown; returning normally (rather
	// than log.Fatal) lets the deferred db.Close run.
	if err := app.Listen(":2137"); err != nil {
		log.Fatal(err)
	}

	<-compactionDone
}

// compactPeriodically rolls per-minute flows up into hourly rows and applies
// retention, at startup and then every hour, until ctx is done. The returned
// channel is closed once a compaction in progress has finished.
func compactPeriodically(ctx context.Context, db *sql.DB, retention time.Duration) <-chan struct{} {
	done := make(chan struct{})

	compact := func() {
		result, err := storage.Compact(db, time.Now(), retention)
		if err != nil {
			log.Println("Failed to compact flows:", err)
			return
		}
		if result != (storage.CompactResult{}) {
			log.Printf("Compacted flows: %d rows rolled up into %d hourly rows, %d expired",
				result.Removed, result.RolledUp, result.Expired)
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

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
