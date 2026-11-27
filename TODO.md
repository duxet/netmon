# TODO

Improvements that aren't in the TODO list of the [README](README.md#todo).

## Collector

- [x] Bound what the Linux collector keeps while recording fails. The connections
  closed since the last round stay in memory until a round is written, so a
  database that keeps failing makes them grow without limit.
- [ ] Try the Linux collector on a busy router: watch the log for "Lost Netfilter
  events", and compare netmon's totals with the router's interface counters.

## Performance and resources

- [x] Page and sort flows in SQL. `/api/flows` and `/api/flows/summary` load every
  flow in the range, then filter, sort, page and look up countries and ASNs in Go
  (`pageFlows` and `summarizeFlows` in `api/flows.go`), on every refresh of every
  open tab. Move `ORDER BY`, `LIMIT`/`OFFSET` and the top-N groups into the query.
- [x] Limit DuckDB's memory and threads. `bootQueries` in `main.go` is empty, so
  DuckDB takes its defaults, about 80% of RAM and every core, which is a lot on a
  router. Add `memory_limit` and `threads` settings, with low defaults in the
  OpenWrt package.
- [x] Roll old hours up into days. With the default `retention_days: 0`,
  `flows_hourly` grows forever. Rolling hours older than some days up into one row
  per flow and day would keep the database small, and make "Monthly usage per
  device" cheap.

## Operations

- [x] Show the collector's health in the web UI. When the collector can't start
  (no `CAP_NET_ADMIN`, say) or `nf_conntrack_acct` is 0, netmon only logs it and
  shows an empty UI. Show a banner with the reason and the time of the last round
  that was recorded.
- [x] Log less. There are about 80 `log.Print` calls, among them one per API
  request ("Returning list of flows") and one per failed reverse DNS lookup
  (`lookup/hostnames.go`), which flood `logd` on OpenWrt. Switch to `log/slog`,
  with a `log_level` setting.
- [x] Add `/healthz`, with a `HEALTHCHECK` in the Dockerfile, and a Prometheus
  `/metrics` endpoint: collector rounds, rows written, lost Netfilter events,
  database size, failed notifications, and optionally the traffic per device.
- [x] Tell the binary its version. CI's `VERSION` only names the archives: set it
  with `-ldflags -X`, add `netmon -version`, and show it in the web UI.
- [x] Add backups. The database is locked while netmon runs, so add a
  `netmon backup` command (DuckDB's `EXPORT DATABASE`), and an export and import of
  the settings alone: networks, device names and icons, channels and rules.
- [ ] Publish a container image for amd64 and arm64 to GHCR, with a compose example
  (`network_mode: host`, `cap_add: NET_ADMIN`).

## Security

- [x] Protect against DNS rebinding and CSRF. Without single sign-on, any web page
  that someone on the network opens can call `PUT /api/config` and the notification
  channel endpoints, which can make netmon send requests to any URL. Restricting
  CORS doesn't stop that. Check the `Host` header against `public_url` and a list of
  allowed hosts, and require `application/json` or a custom header on changes.
- [x] Add API tokens, read-only and revocable, so that Home Assistant, scripts and
  Grafana can use the API while single sign-on is on.

## Features

- [ ] Record the domain names clients look up, as reverse DNS knows nothing useful
  about most CDN and cloud addresses, which is why Cloudflare and Akamai traffic is
  "Other". Read dnsmasq's query log on OpenWrt, or the DNS answers and TLS SNI where
  packets are captured (macOS). Flows would then show `netflix.com`, and apps would
  be recognized far more often. This is about remote hosts, unlike the README's
  item about local hostnames.
- [ ] Add an alert rule for traffic with addresses on public blocklists, such as
  abuse.ch's Feodo Tracker or Spamhaus DROP, refreshed daily.
- [x] Make alerts easier to live with: acknowledge or snooze a firing alert, quiet
  hours, and rules for a device that comes back online and for a device's first
  traffic with a new country or ASN.
- [x] Manage devices: forget a device (there is no `DELETE /api/devices/:mac`), and
  merge several MAC addresses into one device, for phones that use a random one per
  network.
- [ ] Publish devices to Home Assistant over MQTT: whether each is online, for
  presence detection, and its current download and upload.
- [x] Add time ranges: the last hour (its data is kept per minute), a custom range,
  and zooming into a chart by dragging across it. Only the last 24 hours, 7 days and
  30 days can be picked now.
- [x] Search flows by hostname, country, ASN and app. `q` only matches IP
  addresses, ports and protocols (`matchesQuery` in `api/flows.go`).
