package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"flag"
	"github.com/duxet/netmon/alerting"
	"github.com/duxet/netmon/api"
	"github.com/duxet/netmon/apps"
	"github.com/duxet/netmon/auth"
	"github.com/duxet/netmon/collector"
	"github.com/duxet/netmon/config"
	"github.com/duxet/netmon/live"
	"github.com/duxet/netmon/lookup"
	"github.com/duxet/netmon/networks"
	"github.com/duxet/netmon/notify"
	"github.com/duxet/netmon/storage"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/duckdb/duckdb-go/v2"
	migrate "github.com/rubenv/sql-migrate"
)

//go:embed migrations/*
var dbMigrations embed.FS

// notifyShutdownTimeout bounds how long shutting down waits for queued
// notifications to be sent.
const notifyShutdownTimeout = 5 * time.Second

//go:embed all:client/dist/*
var clientAssets embed.FS

func main() {
	configFile := flag.String("config", "", "YAML configuration file (default: $"+config.FileEnv+", or "+config.DefaultFile+" if it exists)")
	flag.Parse()

	cfg, err := config.Load(*configFile)
	if err != nil {
		log.Fatal(err)
	}
	if cfg.File != "" {
		log.Printf("Read configuration from %s", cfg.File)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	connector, err := duckdb.NewConnector(cfg.Database, func(execer driver.ExecerContext) error {
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

	// db, err := sql.Open("duckdb", cfg.Database)
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

	// Catch up on devices in flows written before a crash, before compaction
	// rolls those rows up.
	if err := storage.SyncDevices(db, time.Time{}); err != nil {
		log.Println("Failed to update devices:", err)
	}

	countries := lookup.OpenCountries(cfg.GeoIPCountry)
	defer countries.Close()
	// One ASN database serves both the app classifier and the autonomous
	// systems the web UI shows.
	asns := lookup.OpenASNs(cfg.GeoIPASN)
	defer asns.Close()
	classifier := apps.NewClassifierWithASNs(apps.Known, asns)
	hostnames := lookup.NewHostnames(cfg.LocalDNS, cfg.PublicDNS)

	// Alert rules are evaluated after every collection round, which also
	// brings the devices up to date, and the notifications they raise are
	// sent in the background.
	dispatcher := notify.NewDispatcher(db, notify.DispatcherOptions{})
	engine := alerting.NewEngine(db, dispatcher, alerting.Options{
		Hostnames:  hostnames,
		Countries:  countries,
		Classifier: classifier,
		PublicURL:  cfg.BaseURL(),
	})
	engineDone := make(chan struct{})
	go func() {
		defer close(engineDone)
		engine.Run(ctx)
	}()

	// Traffic between local addresses is left out when so configured. The
	// blocks of the networks set up in the web UI count as local too, and are
	// read again every round, so that changes to them apply within a minute.
	var localTraffic networks.LocalTraffic
	localTraffic.SetIgnored(cfg.IgnoreLocalTraffic)
	readNetworks := func() {
		matcher, err := storage.NetworkMatcher(db)
		if err != nil {
			log.Println("Failed to read networks:", err)
			return
		}
		localTraffic.SetMatcher(matcher)
	}
	readNetworks()

	// The configuration page saves settings to the YAML file, and the ones
	// that can apply right away do.
	var compactNow func()
	applied := *cfg
	editor := config.NewEditor(*configFile, *cfg, func(next config.Config) {
		hostnames.SetServers(next.LocalDNS, next.PublicDNS)
		engine.SetPublicURL(next.BaseURL())
		localTraffic.SetIgnored(next.IgnoreLocalTraffic)
		if next.Retention() != applied.Retention() || next.AlertRetention() != applied.AlertRetention() {
			compactNow()
		}
		applied = next
	})

	compactionDone, compactNow := compactPeriodically(ctx, db, editor.Running)

	// The collector reports the traffic it sees to the meter as it happens, and
	// the API serves what the meter measured to the live view.
	meter := live.NewMeter()

	coll, err := collector.CollectTraffic(db, meter, collector.Options{
		OnFlush: func(at time.Time) {
			readNetworks()
			engine.Trigger(at)
		},
		Interface: cfg.Interface,
		Ignore:    localTraffic.Ignored,
	})
	if err != nil {
		log.Println("Collector failed to start:", err)
	}

	assets, err := fs.Sub(clientAssets, "client/dist")
	if err != nil {
		log.Fatal(err)
	}

	// With an OpenID Connect provider configured, the API and web UI need a
	// session. Without one, anyone who can reach netmon can use all of it.
	var authn *auth.Authenticator
	if cfg.OIDCEnabled() {
		authn, err = auth.New(db, auth.Options{
			Issuer:       cfg.OIDCIssuer,
			ClientID:     cfg.OIDCClientID,
			ClientSecret: cfg.OIDCClientSecret,
			BaseURL:      cfg.BaseURL(),
		})
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("Signing in with %s; the provider sends people back to %s/auth/callback", cfg.OIDCIssuer, cfg.BaseURL())

		// Not having reached the provider is no reason not to start: flows are
		// recorded all the same, and people can sign in once it is there. Say so
		// now, though, instead of when someone tries.
		go func() {
			if err := authn.Check(); err != nil {
				log.Printf("Can't reach the OpenID Connect provider, so nobody can sign in until it is: %v", err)
			}
		}()
	}

	app := api.CreateHTTPApp(db, assets, api.Options{
		Countries: countries, ASNs: asns, Classifier: classifier, Hostnames: hostnames, Live: meter, Alerts: engine, Auth: authn, Config: editor,
	})

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
	if err := app.Listen(cfg.Listen); err != nil {
		log.Fatal(err)
	}

	<-compactionDone
}

// compactPeriodically rolls per-minute flows up into hourly rows and applies
// the retention of flows and alerts, and removes expired sessions, at startup
// and then every hour, until ctx is done. It reads the retention from current
// each time. The returned channel is closed once a compaction in progress has
// finished, and the function asks for a compaction now, such as after the
// retention changed; it never blocks.
func compactPeriodically(ctx context.Context, db *sql.DB, current func() config.Config) (<-chan struct{}, func()) {
	done := make(chan struct{})
	trigger := make(chan struct{}, 1)

	compact := func() {
		cfg := current()
		retention, alertRetention := cfg.Retention(), cfg.AlertRetention()
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

		// Expired sessions don't work anymore either way; this only tidies up.
		if _, err := storage.ExpireSessions(db, now); err != nil {
			log.Println("Failed to expire sessions:", err)
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
			case <-trigger:
				compact()
			}
		}
	}()

	return done, func() {
		select {
		case trigger <- struct{}{}:
		default:
		}
	}
}
