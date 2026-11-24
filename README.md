# netmon

Easy to set up tool for monitoring network traffic flows in your home network.

## Installing

Every [release](https://github.com/duxet/netmon/releases) has an archive per
platform, which holds the `netmon` binary (the web UI is built in), and a
`SHA256SUMS` file to check them against:

| Platform              | File                                 |
|-----------------------|--------------------------------------|
| Linux, x86-64         | `netmon_<version>_linux_amd64.tar.gz`  |
| Linux, arm64          | `netmon_<version>_linux_arm64.tar.gz`  |
| macOS, Intel          | `netmon_<version>_darwin_amd64.tar.gz` |
| macOS, Apple silicon  | `netmon_<version>_darwin_arm64.tar.gz` |
| OpenWrt               | a `.ipk` or `.apk` package, see [below](#running-on-openwrt) |

netmon keeps its database (`flows.db`) in the directory it is started from.
Windows is not supported, as there is no collector for it yet.

The Linux binaries are statically linked, so they don't depend on the C library
of your distribution. The macOS binaries aren't signed, so Gatekeeper blocks
them when they were downloaded with a browser; allow them with
`xattr -d com.apple.quarantine netmon`.

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

## Running on Linux

On Linux traffic is observed through the kernel's connection tracking, which
netmon reads over netlink. Because the router sees all the traffic of your
network, run it on the router for the whole network, or on any other Linux
machine for its own traffic. It needs:

- root (or `CAP_NET_ADMIN`),
- the `nf_conntrack` and `nf_conntrack_netlink` kernel modules,
- connection tracking accounting, as the kernel only keeps byte and packet
  counters when it is on:

```sh
sudo sysctl -w net.netfilter.nf_conntrack_acct=1
sudo ./netmon
```

## Running on OpenWrt

Packages for 64-bit targets (`x86_64` and `aarch64`) are attached to each
release, as `netmon_<version>_openwrt-<series>_<architecture>.<ipk|apk>`.
OpenWrt 24.10 uses `.ipk` packages and 25.12 `.apk` ones. Find your
architecture with `grep DISTRIB_ARCH /etc/openwrt_release`, copy the matching
package to the router and install it:

```sh
opkg install ./netmon_<version>_openwrt-24.10_<architecture>.ipk              # OpenWrt 24.10
apk add --allow-untrusted ./netmon_<version>_openwrt-25.12_<architecture>.apk  # OpenWrt 25.12
```

This installs the `kmod-nf-conntrack-netlink` kernel module package, enables
connection tracking accounting and sets up the `netmon` service, which starts
at boot (start it now with `service netmon start`). The UI is then at
`http://<router>:2137`.

It is configured in `/etc/config/netmon`:

| Option           | Default          |                                                       |
|------------------|------------------|-------------------------------------------------------|
| `enabled`        | `1`              | Start the service                                     |
| `data_dir`       | `/srv/netmon`    | Where `flows.db` and the optional GeoIP databases live |
| `local_dns`      | `127.0.0.1:53`   | `NETMON_LOCAL_DNS`, dnsmasq knows your DHCP names     |
| `public_dns`     | `8.8.8.8:53`     | `NETMON_PUBLIC_DNS`                                   |
| `retention_days` | `0` (keep all)   | `NETMON_RETENTION_DAYS`                               |

```sh
uci set netmon.main.data_dir=/mnt/sda1/netmon
uci commit netmon
service netmon restart
```

Things to know:

- netmon embeds the DuckDB database, which makes the binary about 90 MB
  (the package about 30 MB), and it used around 70 MB of RAM when idle with an
  empty database on x86-64. That doesn't fit the flash of most routers. Use
  an x86 box, or a device with extroot or enough free storage, and keep
  `data_dir` on external storage, as the database is written every minute.
- There are no packages for 32-bit targets (ARM, MIPS), as the DuckDB libraries
  netmon uses are only built for 64-bit platforms.
- netmon has no authentication. The default firewall keeps the WAN away from
  port 2137; don't open it there.

## Traffic by app

The dashboard and client pages show how much traffic went to popular services
(YouTube/Google, Telegram, X, Meta, Netflix, Apple, TikTok, Spotify, Twitch, Steam).
Apps are recognized by the remote IP address, using a built-in list of networks
each service operates. For better coverage, put `GeoLite2-ASN.mmdb` (from MaxMind)
next to the binary: addresses will then also be matched by the service's ASN.

Services that share infrastructure can't be told apart by IP (YouTube and other
Google services; Facebook, Instagram and WhatsApp), and traffic served from shared
CDNs such as Cloudflare or Akamai is reported as "Other".

## Live view

The Live page (`/live`) shows what each device is using right now: download and upload
for every device and for the whole network, updated every second, next to the last two
minutes. Rates are in bits per second, seen from each device's own side. As elsewhere,
only devices on the local network with a known MAC address are listed.

How the rates are measured depends on the platform:

- **Linux** reads the byte counters of Netfilter's connection tracking every second, but
  only while someone is looking: dumping every connection that often is not free on a
  small router, so measuring starts with the first request and stops about 10 seconds after
  the last one. The history therefore begins when the page is opened. The kernel only
  counts bytes with accounting enabled (`sysctl net.netfilter.nf_conntrack_acct=1`), which
  the recording of flows needs as well.
- **macOS** counts packets as they are captured, all the time, so the history is already
  there when the page is opened.

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
database grows with the number of flows rather than with uptime. The
previous hour is kept per minute along with the current one, so the last 60
minutes can always be read at full detail. With `NETMON_RETENTION_DAYS` set,
hourly data older than that many days is deleted.

## API

The web UI is built on a JSON API at `/api`. Times are RFC 3339 timestamps
(e.g. `2026-11-24T08:00:00Z`); `from` is inclusive and `to` exclusive. Data
from before the previous hour is stored per hour, so ranges are effectively
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
| `GET /api/live` | `since` |
| `POST /api/hostnames` | body `{"ip_addresses": [...]}`, at most 256 |
| `GET /api/devices` | |
| `PUT /api/devices/:mac/name` | body `{"name": "..."}`, 1 to 64 characters |
| `DELETE /api/devices/:mac/name` | |

- With `mac` or `ip`, traffic is counted from that client's side: what it
  received is download (`InBytes`), whichever side opened the connection.
- `app` is an app name from `/api/apps`, or `Other`.
- Flows: `q` matches part of an IP address, a port or a protocol; `sort` is
  `total` (default), `download`, `upload` or `packets`; `order` is `desc`
  (default) or `asc`; `limit` is at most 1000 (all flows without it). The
  number of matching flows is in the `X-Total-Count` header.
- The summary has totals and the `top` (default 8) remote hosts, services
  (`"protocol/port"`) and countries, plus the sum of the rest.
- Device names are keyed by MAC address and returned by `/api/clients` as
  `Name`, next to the reverse-DNS `Hostname` and the `Vendor` of the MAC
  address (null when unknown or randomized).
- `/api/live` returns the current bandwidth of devices, one sample per second for the last two
  minutes, oldest first: `[{"Time": "...", "Devices": [{"MACAddress", "IPAddress", "Download",
  "Upload"}]}]` with rates in bytes per second. Quiet devices are left out, so a second without
  traffic has no devices. Pass the `Time` of the newest sample you already have as `since`,
  exactly as it was returned, to get only newer ones. Asking is what makes the Linux collector
  measure (see [Live view](#live-view)), so don't poll it while nobody is looking.
- `/api/devices` lists every MAC address ever seen on either side of a flow,
  most recently seen first, with the `IPAddress` it used last, `FirstSeen`,
  `LastSeen`, and `Name`, `Hostname` and `Vendor` as for clients. Unlike
  `/api/clients` it has no time range and includes devices that only
  received connections, such as a NAS.

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
- [x] Let users rename devices, stored in the database
- [x] Show the device manufacturer from an offline MAC vendor (OUI) database
- [ ] Get local hostnames from the DNS server, DHCP leases or mDNS, and persist resolved names

### Features
- [x] Live view of current bandwidth per device
- [ ] Alerts (new device joined, usage over a threshold) via webhook, ntfy or Telegram
- [ ] Monthly usage per device, for ISP data caps
- [ ] CSV export

### Security and operations
- [ ] Add optional authentication and restrict CORS (currently any origin is allowed)
- [x] Add CI: Go tests and vet; frontend lint, typecheck and build
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

Device vendors come from the IEEE Registration Authority's public MA-L, MA-M
and MA-S listings, embedded in the binary. Refresh them with:

```sh
go generate ./vendors
```

## Releasing

[`.github/workflows/build.yml`](.github/workflows/build.yml) lints and tests
every push and pull request, and builds netmon for each platform (Linux and
macOS on amd64 and arm64) and the OpenWrt packages. Pushing a version tag also
publishes a release with all of them, and their checksums:

```sh
git tag v1.2.3
git push origin v1.2.3
```

The OpenWrt package (`openwrt/netmon`) installs the static Linux binary
instead of compiling it: DuckDB's prebuilt libraries are built for glibc, while
OpenWrt uses musl. To build it yourself, put the static
`linux_amd64` or `linux_arm64` binary at `openwrt/netmon/src/netmon`, add the
`openwrt` directory as a feed (`src-link netmon /path/to/netmon/openwrt` in
`feeds.conf`) and run `make package/netmon/compile` in the SDK.
