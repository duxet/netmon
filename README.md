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

By default netmon keeps its database (`flows.db`) in the directory it is started
from; see [Configuration](#configuration) to change that.
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

Connections to a port the router forwards to a device on your network count as that
device's traffic, though they were sent to the router's address.

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
- netmon has no authentication unless you turn on [single sign-on](#single-sign-on).
  The default firewall keeps the WAN away from port 2137; don't open it there.
  The service starts in `data_dir`, so put the single sign-on settings in a
  `netmon.yaml` there (see [Configuration](#configuration)).
- The service passes `local_dns`, `public_dns` and `retention_days` to netmon as
  environment variables, which override the YAML file, so **Settings → Configuration**
  shows them but leaves them to `/etc/config/netmon`. The other settings it changes in
  `netmon.yaml` in `data_dir`.

## Traffic by app

The dashboard and client pages show how much traffic went to popular services
(YouTube/Google, Telegram, X, Meta, Netflix, Apple, TikTok, Spotify, Twitch, Steam).
Apps are recognized by the remote IP address, using a built-in list of networks
each service operates. For better coverage, put `GeoLite2-ASN.mmdb` (from MaxMind)
next to the binary, or point `geoip_asn` at it: addresses will then also be matched
by the service's ASN.

The same database shows who runs each remote host: client and network pages list the
autonomous system (`AS15169 GOOGLE`) next to a host's country and rank the traffic by
network, which also names the organizations behind traffic that matches no known app.

Services that share infrastructure can't be told apart by IP (YouTube and other
Google services; Facebook, Instagram and WhatsApp), and traffic served from shared
CDNs such as Cloudflare or Akamai is reported as "Other".

## Networks

Group devices into networks, such as VLANs or subnets, under **Networks**: a network
is a name and the blocks of addresses it uses, in CIDR notation (`192.168.20.0/24`,
`fd00:20::/64`) or as single addresses. With no networks yet, the page lists the
blocks your devices use (the `/24` or `/64` of each one's last address), each a click
away from a network, and later the blocks of devices that are still in none.

Each device is then shown with its network's name and color: on the Clients page,
which can be filtered by network or to the devices in none, on its own page and in the
Live view. Every network has a page with its traffic over time, devices, destinations,
services, apps, countries and networks, the Networks page compares them, and the dashboard shows
the traffic of each.

- An address belongs to the network with the most specific block that holds it, as a
  router picks a route, so a network can sit inside another one's block: with
  `10.0.0.0/8` for Home and `10.0.20.0/24` for IoT, the IoT subnet's addresses belong
  to IoT and all others to Home. Two networks can't have the same block.
- A network's traffic is counted from its side: download is what its addresses
  received, from the internet or from other networks. Traffic between two networks
  counts for both, so the traffic of all networks can add up to more than the whole
  network's.
- Devices are matched by their IP addresses only. The VLAN ID is only shown for
  reference: netmon doesn't record VLAN tags. A device with
  addresses in two networks, such as an IPv4 address in one and an IPv6 address in
  another, is in both.
- Matching happens when traffic is read, not when it is recorded, so a new or changed
  network applies to past traffic too.
- IPv6 blocks your provider delegates can change; update the network when they do.

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

## Notifications

netmon can send notifications through [shoutrrr](https://shoutrrr.nickfedor.com), which
supports Telegram, ntfy, Discord, email (SMTP), Gotify, Pushover, Slack, Matrix, Home
Assistant, plain JSON webhooks and more. Add channels under **Settings → Notifications**:
a channel is a name and a shoutrrr service URL, such as
`telegram://TOKEN@telegram?chats=CHAT_ID` or `ntfy://ntfy.sh/TOPIC`. The page shows the
format of each service's URL and links to its documentation, which explains where to find
the values. Send a test message to check that a channel works, before or after saving it.

Then add **alert rules** under **Settings → Alert rules**, or with **Create alert** on a
device's page: what netmon should alert about, and which channels to send it to. Rules
are checked every minute, right after the traffic is recorded. **Preview** in the rule
form shows what a rule would find before you save it: what it measures right now, or
what it would have alerted about in the last 24 hours.

Rules about something that lasts fire while it does, resolve when it's over, and can be
sent again every hour, 6 hours or day while they last, and once more at the end:

- **Traffic goes over a threshold**: a device's download, upload or total over the last
  5 minutes to 24 hours, for all traffic or one app's, for every device or only the ones
  you pick. With **Whole network**, the internet traffic of all devices is added up
  instead, as an ISP data cap counts it (traffic between local devices doesn't count
  then). The alert resolves once the traffic falls under 90 % of the threshold. Windows
  longer than an hour are rounded to whole hours, as the stored data is (see
  [Configuration](#configuration)).
- **A device has no traffic for a while**: 15 minutes to a day, for the devices you
  pick. netmon can only tell for the time it was running, so after a restart a device
  counts as quiet only from then on. This suits devices that are always on, such as a
  NAS or a camera, rather than phones and laptops, which go quiet when they sleep.

Rules about events alert once per event, at most once a day for the same device and
port or country, and only about traffic after the rule was created:

- **A new device joins the network**: once for every device first seen after the rule
  was created. Phones join with a private, random Wi-Fi address per network, which
  counts as a new device; turn on "Ignore private Wi-Fi addresses" if that is too many.
- **A device connects to a port**: connections a device opens to some ports (TCP, UDP
  or either) of any address, such as telnet (23, 2323) from a smart plug.
- **A device exchanges traffic with a country**: with any of a list of countries, or
  with any country not on it. This needs `GeoLite2-Country.mmdb` next to the binary.

The **Alerts** page shows what is firing now and the history; each alert has a page of
its own, with what it was about and every message sent about it, or why it wasn't.
Messages link to that page, at `NETMON_PUBLIC_URL`. A channel gets at most 20 messages
in 5 minutes: more are held back, and the next message says how many. Several alerts
for one channel at once (new devices after a while offline, say) arrive as one message,
except on webhooks. Failed sends are tried three times, and only while netmon runs:
notifications still queued when it stops are not sent.

Things to know:

- A service URL holds the credentials needed to reach the service, such as a bot token
  or an SMTP password. netmon stores it in `flows.db` as is, and never shows or logs it
  again: the API returns it with its secrets replaced by `REDACTED`. To change a URL,
  paste the whole new one.
- Messages are plain text. Don't add `parsemode=HTML` or `Markdown` to a Telegram URL:
  device names with characters such as `_` or `<` would break the formatting.
- Webhook channels (`generic://HOST/PATH?template=json`, or `generic+https://...` in front
  of any URL) get a JSON object with `title`, `message`, `level` and, for alerts, details
  such as the device's address. Add headers as `@Header=value`, e.g.
  `@Authorization=Bearer+TOKEN`.
- Without [single sign-on](#single-sign-on), anyone who can reach netmon can see and change
  the channels, and make netmon send requests to any URL. Only run it on a network you
  trust, or turn single sign-on on.

## Single sign-on

By default netmon has no authentication: anyone who can reach it can see the traffic
of every device and change the notification channels. To require people to sign in,
point it at an [OpenID Connect](https://openid.net/connect/) provider, such as
[Pocket ID](https://pocket-id.org), Authelia, Authentik or Keycloak.

With [Pocket ID](https://pocket-id.org), add a client for netmon under **OIDC Clients**
in its admin settings, with the callback URL `<public_url>/auth/callback`, and copy its
client ID and secret (the secret is only shown once). Pocket ID's issuer is its own
address. Then configure netmon with the address people open it at, and the client:

```yaml
public_url: https://netmon.example.com
oidc_issuer: https://id.example.com
oidc_client_id: 3d1c1b5a-0f0e-4c8e-9d0b-3a8f7f6a2c11
```

Keep the secret out of the file, in the environment (`NETMON_OIDC_CLIENT_SECRET`), or in
a file only netmon can read. For a client that Pocket ID calls a **Public Client**, leave
the secret out: netmon then relies on PKCE alone, which it uses in any case.

- **`oidc_issuer`** is the provider's issuer URL, exactly as the provider writes it: ID
  tokens are matched against it, so a trailing slash matters. netmon finds the rest in
  `<issuer>/.well-known/openid-configuration`. It has to be `https://`, except for a
  provider on the same machine.
- **`public_url`** has to be the address people open netmon at, as the provider sends
  them back to `<public_url>/auth/callback`, which you register there. Open netmon at
  that address: a session cookie is only sent to the host that set it. When it starts
  with `https://`, cookies are marked `Secure`; over plain `http://` they can't be.
- **Who may sign in** is up to the provider: netmon lets in everyone it does. Limit the
  client to the people who should see your network, such as with **Allowed User Groups**
  on the client's Access tab in Pocket ID, and all the more with a provider that has
  other people's accounts, like Google.
- Without a session, requests to `/api` get a `401` and pages send the browser to
  sign in, and back to the page afterwards. The web UI's sidebar shows who is signed in,
  with **Sign out**, and the page signs in again by itself when a session ends while it is open.
- netmon signs people in with the authorization code flow, with PKCE, `state` and a
  `nonce`, and asks for the `openid`, `profile` and `email` scopes. It shows the name or
  username and email from the ID token, and uses nothing else to tell who is who.
- A session lasts 7 days, from signing in, however much it is used. Sessions live in
  `flows.db`, only as a hash of the cookie's value, so they survive restarts and a copy
  of the database can't be used to sign in. netmon doesn't ask the provider again
  after signing in, so taking away someone's access there only takes effect once their
  netmon session ends. Signing out of netmon doesn't sign them out of the provider.
- The provider is only contacted when someone signs in. If it can't be reached when
  netmon starts, netmon starts anyway (the flows are recorded all the same), logs it, and
  tries again when someone signs in. People who already have a session carry on.
- There are no API tokens: scripts can't sign in, so `/api` can't be used with `curl`
  while single sign-on is on.
- Setting `oidc_client_id` or `oidc_client_secret` without `oidc_issuer` is an error, so
  that a setting that has no effect can't leave netmon open without anyone noticing.

## Configuration

netmon is configured with a YAML file, environment variables, or both. A
setting is read from the first of these that has it:

1. the environment variable, such as `NETMON_RETENTION_DAYS`,
2. the YAML file,
3. the default.

The file is the one given with `-config path/to/netmon.yaml` or in
`NETMON_CONFIG`, which has to exist, or else `netmon.yaml` in the working
directory, if there is one. Every setting is a top-level key in the file, and
its environment variable is the key in upper case behind `NETMON_`. Empty
environment variables are ignored. A key that isn't a setting is an error, so
a typo doesn't go unnoticed.

| YAML key               | Environment variable          | Default                  |                                                                    |
|------------------------|-------------------------------|--------------------------|--------------------------------------------------------------------|
| `listen`               | `NETMON_LISTEN`               | `:2137`                  | Address of the API and web UI, as `host:port` or `:port`           |
| `database`             | `NETMON_DATABASE`             | `flows.db`               | Path of the database file                                          |
| `geoip_country`        | `NETMON_GEOIP_COUNTRY`        | `GeoLite2-Country.mmdb`  | Path of the GeoIP country database; empty in the file turns countries off |
| `geoip_asn`            | `NETMON_GEOIP_ASN`            | `GeoLite2-ASN.mmdb`      | Path of the GeoIP ASN database; empty in the file turns networks off, and apps go by their built-in prefixes |
| `local_dns`            | `NETMON_LOCAL_DNS`            | `192.168.1.1:54`         | DNS server for reverse lookups of local addresses                  |
| `public_dns`           | `NETMON_PUBLIC_DNS`           | `8.8.8.8:53`             | DNS server for reverse lookups of public addresses                 |
| `retention_days`       | `NETMON_RETENTION_DAYS`       | `0` (keep all)           | Days to keep hourly flow data                                      |
| `alert_retention_days` | `NETMON_ALERT_RETENTION_DAYS` | `30`                     | Days to keep resolved alerts, `0` keeps them                       |
| `public_url`           | `NETMON_PUBLIC_URL`           | `http://<hostname>:2137` | Where notifications link to; the port is the one of `listen`. Required with `oidc_issuer` |
| `oidc_issuer`          | `NETMON_OIDC_ISSUER`          | none                     | Issuer URL of an OpenID Connect provider; turns [single sign-on](#single-sign-on) on |
| `oidc_client_id`       | `NETMON_OIDC_CLIENT_ID`       | none                     | Client ID at the provider, required with `oidc_issuer`             |
| `oidc_client_secret`   | `NETMON_OIDC_CLIENT_SECRET`   | none                     | Client secret; leave out for a public client                       |
| `interface`            | `NETMON_INTERFACE`            | the default route's      | Interface to capture on, macOS only                                |
| `ignore_local_traffic` | `NETMON_IGNORE_LOCAL_TRAFFIC` | `true`                   | Leave out [traffic between local addresses](#local-traffic)        |
| `log_level`            | `NETMON_LOG_LEVEL`            | `info`                   | The least severe messages logged: `debug`, `info`, `warn` or `error` |

For example, in `netmon.yaml`:

```yaml
listen: 0.0.0.0:2137
database: /srv/netmon/flows.db
local_dns: 192.168.1.1:53
retention_days: 90
public_url: http://router.lan:2137
```

Hostnames are looked up with reverse DNS: local addresses on a server that
knows your DHCP names, public addresses on a public resolver. Set either to
`host`, `host:port` or `system` (the OS resolver). Lookups time out after 2
seconds and are cached (names for an hour, failures for a minute). Countries
need the GeoIP database at `geoip_country` (by default `GeoLite2-Country.mmdb`
in the working directory) when netmon starts, and networks (autonomous systems)
the one at `geoip_asn` (`GeoLite2-ASN.mmdb`).

Flows are recorded every minute. At startup and then every hour, the minutes
of complete hours are rolled up into one row per flow and hour, so the
database grows with the number of flows rather than with uptime. The
previous hour is kept per minute along with the current one, so the last 60
minutes can always be read at full detail. With `NETMON_RETENTION_DAYS` set,
hourly data older than that many days is deleted. Resolved alerts and the record of
their notifications are deleted after `NETMON_ALERT_RETENTION_DAYS` (0 keeps them);
alerts still firing are kept. `NETMON_PUBLIC_URL` is where notifications link to,
e.g. `http://router.lan:2137`.

netmon logs to standard error, one line per message with its details as `key=value`
pairs. `info` logs what netmon does, such as settings saved or devices renamed, and
problems; `debug` also logs every request, and lookups that failed.

### Local traffic

By default netmon only records traffic to and from the internet, and leaves out
traffic between two local addresses, such as backups to a NAS, casting to a TV or
DNS queries to the router. Local addresses are those in the private ranges
(`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16` and `fc00::/7`), loopback,
link-local, multicast, broadcast and unspecified ones, and those in the blocks of
the [networks](#networks) you set up, which is how public IPv6 prefixes of your
network count as local too. Set `ignore_local_traffic: false` (or
`NETMON_IGNORE_LOCAL_TRAFFIC=false`) to record all traffic. The setting applies to
the live view as well, and changes to it, or to the networks, apply to new traffic
within a minute; traffic recorded before stays.

### Changing settings in the web UI

**Settings → Configuration** shows every setting, where its value comes from (the
environment, the file or the default), and changes most of them. It saves them to the
YAML file: the one netmon read, or else `netmon.yaml` in the working directory, which it
creates, readable by netmon's user only. Only the lines of the settings you change are
touched, so comments, blank lines and the rest of the file stay as they are; a setting
taken back to its default is taken out of the file. A change is checked as netmon would
check the file when starting, and nothing is saved when it fails.

- The DNS servers, the retention of flows and alerts, the public URL, ignoring local
  traffic and the log level apply right away.
  Shortening a retention deletes the older data at once, after asking. The GeoIP
  database and the capture interface apply when netmon starts again, which the page
  reminds you of.
- A setting an environment variable sets can't be changed there, as the variable would
  override the file.
- `listen`, `database` and the single sign-on settings are only shown, with the client
  secret as set or not: a page served on the old address can't move netmon, a new
  database path would start an empty database, and single sign-on can't be turned off or
  pointed at another provider from the web UI. With single sign-on on, the same goes for
  `public_url`, which the provider sends people back to.
- Without [single sign-on](#single-sign-on), anyone who can reach netmon can change these
  settings, and delete history by shortening the retention.

## API

The web UI is built on a JSON API at `/api`. Times are RFC 3339 timestamps
(e.g. `2026-11-24T08:00:00Z`); `from` is inclusive and `to` exclusive. Data
from before the previous hour is stored per hour, so ranges are effectively
rounded to whole hours.

| Endpoint | Parameters |
|----------|------------|
| `GET /api/session` | |
| `GET /api/stats` | `network`, `from`, `to` |
| `GET /api/clients` | `network`, `from`, `to` |
| `GET /api/apps` | `mac`, `ip` or `network`, `from`, `to` |
| `GET /api/traffic-measurements` | `mac`, `ip` or `network`, `app`, `from`, `to` |
| `GET /api/flows` | `mac`, `ip` or `network`, `from`, `to`, `q`, `sort`, `order`, `limit`, `offset` |
| `GET /api/clients/:mac-or-ip/flows` | as `/api/flows` |
| `GET /api/flows/summary` | `mac`, `ip` or `network`, `from`, `to`, `top` |
| `GET /api/live` | `since` |
| `POST /api/hostnames` | body `{"ip_addresses": [...]}`, at most 256 |
| `GET /api/devices` | |
| `PUT /api/devices/:mac/name` | body `{"name": "..."}`, 1 to 64 characters |
| `DELETE /api/devices/:mac/name` | |
| `PUT /api/devices/:mac/icon` | body `{"icon": "laptop"}`, see below |
| `DELETE /api/devices/:mac/icon` | |
| `GET /api/networks` | |
| `POST /api/networks` | body `{"name": "...", "prefixes": ["192.168.20.0/24", ...], "vlan": 20, "color": "orange"}` |
| `GET /api/networks/summary` | `from`, `to` |
| `GET /api/networks/suggestions` | |
| `GET /api/networks/:id` | |
| `PUT /api/networks/:id` | body as for `POST` |
| `DELETE /api/networks/:id` | |
| `GET /api/notification-services` | |
| `GET /api/notification-channels` | |
| `POST /api/notification-channels` | body `{"name": "...", "url": "...", "enabled": true}` |
| `GET /api/notification-channels/:id` | |
| `PUT /api/notification-channels/:id` | body as for `POST`, every field optional |
| `DELETE /api/notification-channels/:id` | |
| `POST /api/notification-channels/:id/test` | |
| `POST /api/notification-channels/test` | body `{"url": "...", "name": "..."}` |
| `GET /api/alert-rule-options` | |
| `POST /api/alert-rules/preview` | body as for `POST /api/alert-rules`, `name` optional |
| `GET /api/alert-rules` | |
| `POST /api/alert-rules` | body, see below |
| `GET /api/alert-rules/:id` | |
| `PUT /api/alert-rules/:id` | body as for `POST` |
| `PATCH /api/alert-rules/:id` | body `{"enabled": false}` |
| `DELETE /api/alert-rules/:id` | |
| `GET /api/alerts` | `state` (`firing` or `resolved`), `rule_id`, `limit` (default 50, at most 500), `offset` |
| `GET /api/alerts/:id` | |
| `GET /api/config` | |
| `PUT /api/config` | body `{"local_dns": "192.168.1.1:53", "retention_days": 90, "public_url": null}` |

- With `mac` or `ip`, traffic is counted from that client's side: what it
  received is download (`InBytes`), whichever side opened the connection.
- With `network` (a network's `ID`), traffic is that of flows with either side in the
  network, counted from the network's side as for a client. `/api/clients` instead
  lists the clients whose own address is in the network, and `/api/stats` counts only
  those. An unknown network is answered with a 404.
- `app` is an app name from `/api/apps`, or `Other`.
- Flows: `q` matches part of an IP address, a port or a protocol; `sort` is
  `total` (default), `download`, `upload` or `packets`; `order` is `desc`
  (default) or `asc`; `limit` is at most 1000 (all flows without it). The
  number of matching flows is in the `X-Total-Count` header.
- The summary has totals and the `top` (default 8) remote hosts, services
  (`"protocol/port"`), countries and autonomous systems (`ASNs`, keyed by AS number
  with the organization as `Label`), plus the sum of the rest. `/api/flows` has the
  `ASN` (`Number` and `Organization`) of each endpoint next to its `Country`.
- Device names are keyed by MAC address and returned by `/api/clients` as
  `Name`, next to the reverse-DNS `Hostname` and the `Vendor` of the MAC
  address (null when unknown or randomized).
- A device's icon shows what kind of device it is. It is kept apart from its name, so
  a device can have either, and is returned by `/api/clients` and `/api/devices` as
  `Icon`, or null without one. It is one of `desktop`, `laptop`, `tablet`, `smartphone`,
  `watch`, `tv`, `media_player`, `game_console`, `speaker`, `router`, `access_point`,
  `switch`, `server`, `nas`, `printer`, `camera`, `light`, `plug`, `thermostat`,
  `appliance`, `iot`, `phone` or `car`.
- `/api/live` returns the current bandwidth of devices, one sample per second for the last two
  minutes, oldest first: `[{"Time": "...", "Devices": [{"MACAddress", "IPAddress", "Download",
  "Upload"}]}]` with rates in bytes per second. Quiet devices are left out, so a second without
  traffic has no devices. Pass the `Time` of the newest sample you already have as `since`,
  exactly as it was returned, to get only newer ones. Asking is what makes the Linux collector
  measure (see [Live view](#live-view)), so don't poll it while nobody is looking.
- `/api/devices` lists every MAC address ever seen on either side of a flow,
  most recently seen first, with the `IPAddress` it used last, `FirstSeen`,
  `LastSeen`, and `Name`, `Icon`, `Hostname` and `Vendor` as for clients. Unlike
  `/api/clients` it has no time range and includes devices that only
  received connections, such as a NAS.
- Clients and devices have the `NetworkID` of the network their IP address belongs to,
  or null.
- A network has an `ID`, `Name`, `VLAN` (null without one), `Color`, `Prefixes`,
  `CreatedAt` and `UpdatedAt`. `prefixes` are 1 to 32 blocks in CIDR notation or single
  addresses, with host bits cleared (`192.168.20.1/24` is `192.168.20.0/24`); `vlan` is
  1 to 4094, or null; `color` is `blue`, `orange`, `aqua`, `yellow`, `magenta`,
  `green`, `violet` or `red`. A new network without a color gets one no other network
  has, and an update without one keeps it; otherwise an update replaces everything.
  A name (in any case) or a block another network has already is answered with a 409.
- `/api/networks/summary` answers `{"Networks": [{"NetworkID", "Devices", "Download",
  "Upload", "Packets"}], "UnassignedDevices"}`, every network's traffic from its side and
  its number of devices with traffic, and the number of devices with no address in any
  network.
- `/api/networks/suggestions` lists up to 10 blocks of addresses no network has, as
  `[{"Prefix", "Devices"}]`: the `/24` (IPv4) or `/64` (IPv6) of each known device's last
  address, most devices first.
- `/api/notification-services` lists the services channels can use, the common ones
  first with an `ExampleURL`, each with its `DocsURL`.
- Notification channels have an `ID`, `Name`, `Service` (the URL's scheme), `Enabled`,
  `CreatedAt` and `UpdatedAt`. Their URL is write-only: responses have `MaskedURL`, with
  the secrets replaced by `REDACTED`. An update keeps the fields it leaves out, and an
  empty `url` keeps the URL too. `enabled` defaults to true.
- The test endpoints send a test message, to a saved channel or to a URL that isn't saved
  yet, and answer 204. When the service fails, they answer 502 with its reason in `error`.
- An alert rule is `{"name", "type", "params", "severity", "repeat_interval_s",
  "notify_on_resolve", "enabled", "channel_ids"}`. `severity` is `info`, `warning`
  (default) or `critical`; `repeat_interval_s` is at least 900, or null to notify once.
  Changing what a rule checks, or turning it off, resolves its firing alerts without a
  notification. `type` and its `params` are one of these (`macs` limits a rule to some
  devices, and is optional but for `device_inactive`):
  - `new_device`: `{"ignore_random_macs": false}`
  - `traffic_threshold`: `{"direction": "download" | "upload" | "total",
    "threshold_bytes", "window_s" (300 to 86400), "app", "macs": [...], "network": false}`,
    where `app` is optional and a name from `/api/alert-rule-options`
  - `device_inactive`: `{"inactive_s" (300 to 604800), "macs": [...]}`
  - `destination_port`: `{"ports": [23, ...], "protocol": "tcp" | "udp" | "any", "macs"}`
  - `destination_country`: `{"mode": "deny" | "allow", "countries": ["RU", ...], "macs"}`;
    `GeoIP` in `/api/alert-rule-options` tells whether netmon can look up countries
- A preview answers `{"Findings": [...], "Total", "Since"}`: the first 20 findings, each
  with a `Subject`, a `Label`, a `Value` (bytes, or seconds for quiet devices),
  `Fires`, and the `Title` and `Body` it would send. Rules about traffic and quiet
  devices list what they measure now, including what doesn't fire; rules about events
  what they would have found since `Since`, 24 hours ago. Nothing is saved or sent.
- Alerts have the rule's `RuleName`, a `Subject` (the device's MAC address, with the
  port or country for those rules, or `network`), `State`, `Title`, `Body`, `Labels`
  about the device (`mac`, `ip`, `device_name`, `hostname`, `vendor`, and `port`,
  `protocol`, `country`, `country_name`, `peer` or `peers` where they apply; `scope`
  is `network` for the whole network), times, and `Deliveries`: per channel and kind (`firing`, `reminder`,
  `resolved`), whether it was `sent`, `failed` (with `Error`) or `suppressed` by the rate
  limit. The number of matching alerts is in `X-Total-Count`.
- Webhook channels get alerts as JSON with `title`, `message`, `level`, `event`
  (`alert.firing`, `alert.reminder` or `alert.resolved`), `alert_id`, `rule_id`,
  `rule_name`, `rule_type`, `severity`, `subject`, `state`, `value`, `started_at`,
  `resolved_at`, `url`, the labels, and `payload_version` (`1`). All values are strings.

Invalid parameters are answered with a 400 and `{"error": "..."}`. With
[single sign-on](#single-sign-on) on, every request needs the session cookie, or is
answered with a 401.

`/api/config` answers `{"File", "Exists", "OS", "DefaultPublicURL", "Settings"}`, where
each setting has its `Key` (as in the YAML file), `Value` (what netmon would start with
now), `Running` (what it runs with), `Default`, `File` and `InFile`, `Env` (the variable
that sets it, if one does), `Live` (applies without a restart), `Locked` (why it can only
be changed in the file), `Secret` and `Set` (secrets have no values), and `Restart`
(netmon runs with something else until it starts again). `PUT /api/config` changes the
settings in its body, a string, a number or `true` or `false` as the setting is, or `null` to take one out of
the file; settings it leaves out stay. It answers like `GET`, or with a 400 and
`{"error", "setting"}` for a change it refuses, which saves nothing.

`/api/session` tells whether netmon asks people to sign in and who the request is from:
`{"AuthEnabled": true, "User": {"Name": "...", "Email": "..."}}`, where `Name` and
`Email` are null when the provider didn't say. Without single sign-on it is
`{"AuthEnabled": false, "User": null}`.

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
- [x] Let users pick an icon for each device (desktop, laptop, smartphone, TV, gaming console, access point, IP camera, ...)
- [x] Show the device manufacturer from an offline MAC vendor (OUI) database
- [ ] Get local hostnames from the DNS server, DHCP leases or mDNS, and persist resolved names

### Features
- [x] Live view of current bandwidth per device
- [x] Alerts (new device joined, usage over a threshold) via webhook, ntfy or Telegram
- [x] Single sign-on with OpenID Connect, such as Pocket ID
- [x] Networks (VLANs, subnets) to group devices by address, with a summary for each
- [ ] Limit alert rules to a network, and name the network in alerts
- [ ] Monthly usage per device, for ISP data caps
- [ ] CSV export

### Security and operations
- [ ] Add optional authentication and restrict CORS (currently any origin is allowed)
- [x] Add CI: Go tests and vet; frontend lint, typecheck and build
- [ ] Add frontend unit tests for flow direction and time-series code
- [ ] Build the frontend in the Dockerfile (a fresh checkout has no `client/dist` to embed) and fill in `arm64.Dockerfile`
- [x] Make port, database path, the GeoIP country path and DNS servers configurable via a YAML file or environment variables
- [x] Make the path of `GeoLite2-ASN.mmdb` configurable too
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
