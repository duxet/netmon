# netmon

Easy to set up tool for monitoring network traffic flows in your home network.

## Running on macOS

On macOS traffic is observed by capturing packets with libpcap (which ships
with macOS) and aggregating them into flows. Capturing requires access to the
BPF devices, so start netmon as root:

```sh
sudo ./netmon
```

It captures on the interface the default route goes through. Set
`NETMON_INTERFACE` to capture on another one:

```sh
sudo NETMON_INTERFACE=en5 ./netmon
```

Things to know:

- Only traffic that reaches the chosen interface is recorded. For most Macs
  that is their own traffic; the traffic of other hosts shows up when the Mac
  routes or bridges it (e.g. Internet Sharing), or is plugged into a mirrored
  switch port.
- MAC addresses are recorded for hosts on the interface's local network only.
  Interfaces without Ethernet frames, such as VPN tunnels, give no MAC
  addresses, so there will be no clients listed for them.
- Building needs cgo and the Xcode command line tools (`xcode-select --install`).
- `flows.db` is created in the working directory, so it will be owned by root
  when started with `sudo`.

## Traffic by app

The dashboard and client pages show how much traffic went to popular services
(YouTube/Google, Telegram, X, Meta, Netflix, Apple, TikTok, Spotify, Twitch, Steam).
Apps are recognized by the remote IP address, using a built-in list of networks
each service operates. For better coverage, put `GeoLite2-ASN.mmdb` (from MaxMind)
next to the binary: addresses will then also be matched by the service's ASN.

Services that share infrastructure can't be told apart by IP (YouTube and other
Google services; Facebook, Instagram and WhatsApp), and traffic served from shared
CDNs such as Cloudflare or Akamai is reported as "Other".

## Configuration

Hostnames are looked up with reverse DNS: local addresses on a server that
knows your DHCP names, public addresses on a public resolver. Set either to
`host`, `host:port` or `system` (the OS resolver):

| Variable                | Default          |
|-------------------------|------------------|
| `NETMON_LOCAL_DNS`      | `192.168.1.1:54` |
| `NETMON_PUBLIC_DNS`     | `8.8.8.8:53`     |
| `NETMON_RETENTION_DAYS` | `0` (keep all)   |

Lookups time out after 2 seconds and are cached (names for an hour, failures
for a minute). Countries need `GeoLite2-Country.mmdb` next to the binary when
it starts.

Flows are recorded every minute. At startup and then every hour, the minutes
of complete hours are rolled up into one row per flow and hour, so the
database grows with the number of flows rather than with uptime. With
`NETMON_RETENTION_DAYS` set, hourly data older than that many days is deleted.

## API

The web UI is built on a JSON API at `/api`. Times are RFC 3339 timestamps
(e.g. `2026-11-24T08:00:00Z`); `from` is inclusive and `to` exclusive. Data
older than the current hour is stored per hour, so ranges are effectively
rounded to whole hours.

| Endpoint | Parameters |
|----------|------------|
| `GET /api/stats` | `from`, `to` |
| `GET /api/clients` | `from`, `to` |
| `GET /api/apps` | `mac` or `ip`, `from`, `to` |
| `GET /api/traffic-measurements` | `mac` or `ip`, `app`, `from`, `to` |
| `GET /api/flows` | `mac` or `ip`, `from`, `to`, `q`, `sort`, `order`, `limit`, `offset` |
| `GET /api/clients/:mac-or-ip/flows` | as `/api/flows` |
| `GET /api/flows/summary` | `mac` or `ip`, `from`, `to`, `top` |
| `POST /api/hostnames` | body `{"ip_addresses": [...]}`, at most 256 |

- With `mac` or `ip`, traffic is counted from that client's side: what it
  received is download (`InBytes`), whichever side opened the connection.
- `app` is an app name from `/api/apps`, or `Other`.
- Flows: `q` matches part of an IP address, a port or a protocol; `sort` is
  `total` (default), `download`, `upload` or `packets`; `order` is `desc`
  (default) or `asc`; `limit` is at most 1000 (all flows without it). The
  number of matching flows is in the `X-Total-Count` header.
- The summary has totals and the `top` (default 8) remote hosts, services
  (`"protocol/port"`) and countries, plus the sum of the rest.

Invalid parameters are answered with a 400 and `{"error": "..."}`.

## TODO

### Stability
- [x] Return errors from storage queries as HTTP 500s instead of calling `log.Fatal`, which stops the server
- [x] Fix or remove `/api/clients/:clientId/flows`: it scans 8 of the 10 selected columns, so every call fails
- [x] Open the GeoIP database once at startup: `getCountry` opens it for every endpoint, never closes it, and crashes when a lookup fails
- [x] Make reverse DNS configurable (`NETMON_LOCAL_DNS`, `NETMON_PUBLIC_DNS`), cached, parallel and with short timeouts

### API
- [x] Add `from`/`to` time ranges to stats, clients, flows and apps, so the dashboard range filter scopes the whole page
- [x] Add `mac`/`ip` filters to `/api/traffic-measurements` for traffic over time per client and per app
- [x] Add data retention and hourly rollups so the flows table doesn't grow forever
- [x] Use server-side pagination for flows (the `Pagination` type is unused)

### Devices
- [ ] Let users rename devices, stored in the database
- [ ] Show the device manufacturer from an offline MAC vendor (OUI) database when there is no hostname
- [ ] Get local hostnames from the DNS server, DHCP leases or mDNS, and persist resolved names

### Features
- [ ] Live view of current bandwidth per device
- [ ] Alerts (new device joined, usage over a threshold) via webhook, ntfy or Telegram
- [ ] Monthly usage per device, for ISP data caps
- [ ] CSV export

### Security and operations
- [ ] Add optional authentication and restrict CORS (currently any origin is allowed)
- [ ] Add CI: Go tests and vet; frontend lint, typecheck and build
- [ ] Add frontend unit tests for flow direction and time-series code
- [ ] Build the frontend in the Dockerfile (a fresh checkout has no `client/dist` to embed) and fill in `arm64.Dockerfile`
- [ ] Make port, database path, GeoIP paths and DNS servers configurable via flags or environment variables
- [ ] Add collectors for other platforms (Windows, BSD)

## Development

The web UI lives in `client/` (React 19, Vite, Tailwind CSS v4, [shadcn/ui](https://ui.shadcn.com)
with shadcn charts on Recharts, TanStack Router/Query/Table). It is embedded into the Go binary
from `client/dist`, so build it before the server:

```sh
just build              # npm run build in client/, then go build
```

For UI work, run the API (`./netmon`, port 2137) and the Vite dev server, which proxies `/api`:

```sh
cd client && npm install && npm run dev
```

Add shadcn components with `npx shadcn@latest add <component>` from `client/`.
