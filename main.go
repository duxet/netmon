package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"errors"
	"flag"
	"fmt"
	"github.com/duxet/netmon/alerting"
	"github.com/duxet/netmon/api"
	"github.com/duxet/netmon/apps"
	"github.com/duxet/netmon/auth"
	"github.com/duxet/netmon/blocklists"
	"github.com/duxet/netmon/collector"
	"github.com/duxet/netmon/config"
	"github.com/duxet/netmon/domains"
	"github.com/duxet/netmon/homeassistant"
	"github.com/duxet/netmon/live"
	"github.com/duxet/netmon/localnames"
	"github.com/duxet/netmon/logging"
	"github.com/duxet/netmon/lookup"
	"github.com/duxet/netmon/networks"
	"github.com/duxet/netmon/notify"
	"github.com/duxet/netmon/storage"
	"github.com/duxet/netmon/unifi"
	"github.com/duxet/netmon/version"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
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
	showVersion := flag.Bool("version", false, "print the version and exit")
	healthcheck := flag.Bool("healthcheck", false, "check that the netmon serving at the configured address is healthy, and exit with 0 if it is")
	backupTo := flag.String("backup", "", "write a copy of the database to this file, and exit; works while netmon runs")
	flag.Parse()

	if *showVersion {
		fmt.Println("netmon", version.String())
		return
	}

	cfg, err := config.Load(*configFile)
	if err != nil {
		fatal("Can't start", err)
	}
	if *healthcheck {
		os.Exit(checkHealth(cfg.Listen))
	}
	if *backupTo != "" {
		if err := backup(cfg, *backupTo); err != nil {
			fmt.Fprintln(os.Stderr, "Backing up failed:", err)
			os.Exit(1)
		}
		fmt.Println("Backed up", cfg.Database, "to", *backupTo)
		return
	}
	logging.Setup(cfg.LogLevel)
	slog.Info("Starting netmon", "version", version.String())
	if cfg.File != "" {
		slog.Info("Read configuration", "file", cfg.File)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The limits apply to the whole database, but the configuration page can
	// change them, so new connections set the current ones.
	var limits atomic.Pointer[storage.Limits]
	limits.Store(&storage.Limits{Memory: cfg.MemoryLimit, Threads: cfg.Threads})
	connector, err := duckdb.NewConnector(cfg.Database, func(execer driver.ExecerContext) error {
		for _, query := range limits.Load().Statements() {
			if _, err := execer.ExecContext(context.Background(), query, nil); err != nil {
				return fmt.Errorf("%s: %w", query, err)
			}
		}
		return nil
	})
	if err != nil {
		fatal("Can't open the database", err)
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
		fatal("Can't update the database", err)
	}
	if n > 0 {
		slog.Info("Updated the database", "migrations", n)
	}

	// Catch up on devices in flows written before a crash, before compaction
	// rolls those rows up.
	if err := storage.SyncDevices(db, time.Time{}); err != nil {
		slog.Error("Failed to update devices", "err", err)
	}

	countries := lookup.OpenCountries(cfg.GeoIPCountry)
	defer countries.Close()
	// One ASN database serves both the app classifier and the autonomous
	// systems the web UI shows.
	asns := lookup.OpenASNs(cfg.GeoIPASN)
	defer asns.Close()
	classifier := apps.NewClassifierWithASNs(apps.Known, asns)

	// The names clients look up tell what the addresses they then talk to
	// are for, from dnsmasq's log or, on macOS, the packets captured.
	names := domains.New()
	if err := names.Load(db, time.Now().Add(-domains.Retention)); err != nil {
		slog.Error("Failed to load the names of addresses", "err", err)
	}
	classifier.SetNames(names.Names)
	if cfg.DNSLog != "" {
		go domains.Follow(ctx, cfg.DNSLog, names)
	}
	hostnames := lookup.NewHostnames(cfg.LocalDNS, cfg.PublicDNS)

	// Local devices are named by the names they asked the DHCP server for,
	// or announce over mDNS, before reverse DNS is asked. They are kept per
	// MAC address, so a device keeps its name when its lease is gone.
	localNames := localnames.New()
	if err := localNames.Load(db); err != nil {
		slog.Error("Failed to load the names of devices", "err", err)
	}
	hostnames.SetLocalNames(localNames.ByIP)
	if paths := localnames.LeaseFiles(cfg.DHCPLeases); len(paths) > 0 {
		go localnames.WatchLeases(ctx, paths, strings.TrimSpace(cfg.DHCPLeases) == localnames.Auto, localNames)
	}
	if cfg.MDNS {
		go func() {
			if err := localnames.ListenMDNS(ctx, localNames, true); err != nil {
				slog.Warn("Can't listen for mDNS, so devices aren't named by the names they announce", "err", err)
			}
		}()
	}

	// Alert rules are evaluated after every collection round, which also
	// brings the devices up to date, and the notifications they raise are
	// sent in the background.
	// The blocklists of blocklist rules are kept next to the database.
	lists := blocklists.New(filepath.Join(filepath.Dir(cfg.Database), "blocklists"))
	dispatcher := notify.NewDispatcher(db, notify.DispatcherOptions{})
	engine := alerting.NewEngine(db, dispatcher, alerting.Options{
		Hostnames:  hostnames,
		Countries:  countries,
		ASNs:       asns,
		Blocklists: lists,
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
			slog.Error("Failed to read networks", "err", err)
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
		logging.SetLevel(next.LogLevel)
		hostnames.SetServers(next.LocalDNS, next.PublicDNS)
		engine.SetPublicURL(next.BaseURL())
		localTraffic.SetIgnored(next.IgnoreLocalTraffic)
		if next.Retention() != applied.Retention() || next.AlertRetention() != applied.AlertRetention() ||
			next.HourlyRetention() != applied.HourlyRetention() {
			compactNow()
		}
		if next.MemoryLimit != applied.MemoryLimit || next.Threads != applied.Threads {
			l := storage.Limits{Memory: next.MemoryLimit, Threads: next.Threads}
			limits.Store(&l)
			for _, query := range l.Statements() {
				if _, err := db.Exec(query); err != nil {
					slog.Error("Failed to change the database's limits", "query", query, "err", err)
				}
			}
		}
		applied = next
	})

	compactionDone, compactNow := compactPeriodically(ctx, db, editor.Running)

	// The collector reports the traffic it sees to the meter as it happens, and
	// the API serves what the meter measured to the live view.
	meter := live.NewMeter()

	health := collector.NewHealth()
	totals := collector.NewTotals()
	// With a broker, devices are published to Home Assistant after every
	// round.
	var publisher *homeassistant.Publisher
	if cfg.MQTTBroker != "" {
		publisher = homeassistant.Connect(homeassistant.Config{
			Broker: cfg.MQTTBroker, Username: cfg.MQTTUsername, Password: cfg.MQTTPassword,
			Topic: cfg.MQTTTopic, DiscoveryPrefix: cfg.MQTTDiscoveryPrefix, AllDevices: cfg.MQTTAllDevices,
		}, db, totals)
	}

	coll, err := collector.CollectTraffic(db, meter, collector.Options{
		Health: health,
		Totals: totals,
		DNS:    names.ObserveDNS,
		OnFlush: func(at time.Time) {
			readNetworks()
			engine.Trigger(at)
			go func() {
				if err := names.Flush(db, at); err != nil {
					slog.Error("Failed to save the names of addresses", "err", err)
				}
				if err := localNames.Flush(db); err != nil {
					slog.Error("Failed to save the names of devices", "err", err)
				}
			}()
			if publisher != nil {
				go publisher.Publish(at)
			}
		},
		Interface: cfg.Interface,
		Ignore:    localTraffic.Ignored,
	})
	if err != nil {
		slog.Error("Collector failed to start", "err", err)
		health.Failed(err)
	}

	assets, err := fs.Sub(clientAssets, "client/dist")
	if err != nil {
		fatal("Can't serve the web UI", err)
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
			fatal("Can't set up single sign-on", err)
		}
		slog.Info("Signing in with OpenID Connect", "issuer", cfg.OIDCIssuer, "callback", cfg.BaseURL()+"/auth/callback")

		// Not having reached the provider is no reason not to start: flows are
		// recorded all the same, and people can sign in once it is there. Say so
		// now, though, instead of when someone tries.
		go func() {
			if err := authn.Check(); err != nil {
				slog.Warn("Can't reach the OpenID Connect provider, so nobody can sign in until it is", "err", err)
			}
		}()
	}

	// Clients are read from UniFi in the background; a controller that can't be
	// reached is logged, and devices go without UniFi details meanwhile.
	var unifiSource *unifi.Source
	if cfg.UniFiEnabled() {
		unifiSource, err = unifi.New(unifi.Options{
			URL:         cfg.UniFiURL,
			APIKey:      cfg.UniFiAPIKey,
			Username:    cfg.UniFiUsername,
			Password:    cfg.UniFiPassword,
			Site:        cfg.UniFiSite,
			Fingerprint: cfg.UniFiFingerprint,
		})
		if err != nil {
			fatal("Failed to set up UniFi", err)
		}
		slog.Info("Reading clients from UniFi", "url", cfg.UniFiURL, "site", cfg.UniFiSite)
		go unifiSource.Run(ctx)
	}

	app := api.CreateHTTPApp(db, assets, api.Options{
		Countries: countries, ASNs: asns, Classifier: classifier, Hostnames: hostnames, Live: meter, Alerts: engine, Auth: authn, Config: editor,
		Collector: health, Totals: totals, Deliveries: dispatcher.Deliveries, DatabasePath: cfg.Database, Blocklists: lists,
		Domains:    names,
		LocalNames: localNames,
		Hosts: func() api.HostRules {
			running := editor.Running()
			return api.HostRules{PublicURL: running.BaseURL(), Allowed: running.AllowedHostList()}
		},
		UniFi: unifiSource,
	})

	go func() {
		<-ctx.Done()
		slog.Info("Shutting down gracefully")

		if coll != nil {
			coll.Shutdown()
		}
		// Let a running evaluation finish, then send what it raised.
		<-engineDone
		dispatcher.Close(notifyShutdownTimeout)
		if publisher != nil {
			publisher.Close()
		}
		if err := names.Flush(db, time.Now()); err != nil {
			slog.Error("Failed to save the names of addresses", "err", err)
		}
		if err := localNames.Flush(db); err != nil {
			slog.Error("Failed to save the names of devices", "err", err)
		}

		_ = app.ShutdownWithContext(context.Background())
	}()

	// Listen returns nil after a graceful shutdown; returning normally (rather
	// than exiting) lets the deferred db.Close run.
	if err := app.Listen(cfg.Listen); err != nil {
		fatal("Can't serve the API and web UI", err)
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
		result, err := storage.Compact(db, now, retention, cfg.HourlyRetention())
		if err != nil {
			slog.Error("Failed to compact flows", "err", err)
		} else if result != (storage.CompactResult{}) {
			slog.Info("Compacted flows", "rows", result.Removed, "hourly_rows", result.RolledUp,
				"hourly_rows_removed", result.RemovedHourly, "daily_rows", result.RolledUpDaily, "expired", result.Expired)
		}

		expired, err := storage.ExpireAlerts(db, now, alertRetention)
		if err != nil {
			slog.Error("Failed to expire alerts", "err", err)
		} else if expired > 0 {
			slog.Info("Expired resolved alerts", "alerts", expired)
		}

		// Expired sessions don't work anymore either way; this only tidies up.
		if _, err := storage.ExpireSessions(db, now); err != nil {
			slog.Error("Failed to expire sessions", "err", err)
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

// localURL returns the URL of path at the netmon listening on listen, on
// this machine.
func localURL(listen, path string) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", err
	}
	if addr, err := netip.ParseAddr(host); host == "" || (err == nil && addr.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + path, nil
}

// localClient makes requests to netmon on this machine, never through a
// proxy.
func localClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: &http.Transport{Proxy: nil}}
}

// backup writes a copy of the database to path. When netmon runs, the
// database is locked, so the running netmon makes the copy.
func backup(cfg *config.Config, path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s exists already", path)
	}
	if _, err := os.Stat(cfg.Database); err != nil {
		return fmt.Errorf("no database: %w", err)
	}

	db, err := sql.Open("duckdb", cfg.Database)
	if err == nil {
		err = db.Ping()
	}
	if err == nil {
		defer db.Close()
		return storage.Backup(context.Background(), db, path)
	}
	if !strings.Contains(err.Error(), "lock") {
		return err
	}

	url, err := localURL(cfg.Listen, "/api/backup")
	if err != nil {
		return err
	}
	resp, err := localClient(0).Get(url)
	if err != nil {
		return fmt.Errorf("the database is in use, and netmon isn't answering at %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return errors.New("the database is in use, and netmon asks people to sign in, so ask it for a backup in the web UI")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("netmon answered %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	part := path + ".part"
	file, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, resp.Body); err != nil {
		_ = file.Close()
		_ = os.Remove(part)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(part)
		return err
	}
	return os.Rename(part, path)
}

// checkHealth asks the netmon serving at listen whether it is healthy, as a
// container's health check does, and returns the exit status: 0 when it is.
func checkHealth(listen string) int {
	url, err := localURL(listen, "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	resp, err := localClient(5 * time.Second).Get(url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "netmon isn't answering:", err)
		return 1
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		fmt.Fprintf(os.Stderr, "netmon is unhealthy: %s\n", strings.TrimSpace(string(body)))
		return 1
	}
	return 0
}

// fatal logs why netmon can't go on, and exits.
func fatal(msg string, err error) {
	slog.Error(msg, "err", err)
	os.Exit(1)
}
