# Performance with a lot of data

How netmon copes with months and years of traffic, and what alert rules cost.
Measured on 2026-11-24 with the harness in [`perf/`](../perf), on synthetic data
(see [How it was measured](#how-it-was-measured)).

## Summary

- **Everyday use stays fast as data piles up.** The dashboard opens on the last 7
  days. On a year of data from 50 devices (17.8 M rows) it loads in about 0.2 s. On
  a single core with half a year of data, it takes 0.3 s. Endpoints that let DuckDB do the aggregating (`stats`,
  `clients`, `traffic-measurements`, `devices`, alerts) take 0.2–0.3 s even over the
  whole year.
- **Long ranges break the endpoints that load every row into Go.** These are the app
  traffic chart, `/api/apps`, the flow list and the flow summary. Their cost grows
  with the amount of data in the range, not with the size of the answer:
  - On "All time" over a year, the app chart takes **38 s, allocates 14.5 GB and
    peaks at 8 GB of RAM**. That would get netmon killed on any router.
  - `/api/flows?limit=50` reads 3.6 M flows to return 50: 7.8 s and 5 GB.
  - The UI refreshes open pages every minute, so a page left open repeats this.
  - Prototypes that aggregate in SQL bring the app chart from 38 s to 0.5 s.
- **The collector inserts flows one row at a time, ~1 ms per row.** It does so while
  holding the lock that the conntrack event reader needs. A home network writing
  ~300 rows a minute spends 0.4 s a minute on this. A busy one with 10,000 flows a
  minute (e.g. BitTorrent) spends 8–13 s, on fast x86 cores. DuckDB's Appender
  writes the same rows in 5 µs each.
- **Alert rules cost per rule, not per amount of history.**
  - A typical set of 8 rules takes 0.14–0.17 s a minute, the same with 30 days of
    data as with a year.
  - Each traffic rule with a 24 h window adds 30–40 ms, because every rule repeats
    the same query: 50 such rules take 1.6 s a minute.
  - Evaluating them while the dashboard loads slows it by ~30 %, but only for the
    0.3–1.6 % of each minute that evaluation lasts.
- **Client pages always fetch the device list for all time.** That puts a floor of
  0.3 s (0.56 s on one core with half a year of data) under a client page, even for
  its last 24 hours.

## Results

### Datasets

| | Devices (seen) | History | Hourly rows | Per-minute rows | `flows.db` |
|---|---|---|---|---|---|
| S | 25 (36) | 30 days | 0.70 M | 17.6 k | 19 MB |
| M | 50 (111) | 180 days | 8.77 M | 35.2 k | 183 MB |
| L | 50 (175) | 365 days | 17.8 M | 36.0 k | 366 MB |

"Seen" includes guests and phones with a private Wi-Fi address that came and went.
The 50-device household moves about 33 GB a day. The database grows by about 7 MB
per device and year, 21 bytes per hourly row.

### Pages of the web UI

Time until all the requests a page makes at once have been answered. This is
the median of 5 runs, on 4 cores unless marked. The client page is the one of the
busiest laptop.

| Page | Range | S | M | L | M, 1 core |
|---|---|---|---|---|---|
| Dashboard | 24h | 26 ms | 50 ms | 41 ms | 81 ms |
| Dashboard | 7d (default) | 88 ms | 212 ms | 213 ms | 298 ms |
| Dashboard | 30d | 314 ms | 617 ms | 613 ms | 1.0 s |
| Dashboard | All | 281 ms | 2.4 s | 4.6 s | 5.2 s |
| Client page | 24h | 39 ms | 185 ms | 354 ms | 796 ms |
| Client page | 7d | 61 ms | 221 ms | 373 ms | 721 ms |
| Client page | 30d | 180 ms | 321 ms | 458 ms | 1.1 s |
| Client page | All | 185 ms | 1.0 s | 2.0 s | 3.0 s |

The app traffic chart opens on top of the dashboard when an app is picked:

| App chart (`traffic-measurements?app=`) | S | M | L | M, 1 core |
|---|---|---|---|---|
| 7d | 227 ms | 539 ms | 526 ms | 649 ms |
| 30d | 1.1 s | 2.3 s | 2.1 s | 2.9 s |
| All | 1.1 s | 14.5 s | 38.3 s | 18.2 s |

### Endpoints that don't scale

On L. "Go alloc" is what the request allocated, and "Peak RSS" is the process's
peak resident memory while it ran. RSS includes DuckDB's buffer cache, which
DuckDB bounds with `memory_limit` (by default 80 % of RAM); the Go side has no
such bound.

| Request | Range | Time | Go alloc | Peak RSS | Aggregated in SQL (prototype) |
|---|---|---|---|---|---|
| `traffic-measurements?app=Netflix` | 30d | 2.1 s | 1.1 GB | 1.0 GB | 146 ms |
| `traffic-measurements?app=Netflix` | All | 38.3 s | 14.5 GB | 8.1 GB | 523 ms |
| `apps` | 30d | 492 ms | 244 MB | 451 MB | 168 ms |
| `apps` | All | 3.3 s | 1.2 GB | 1.9 GB | 541 ms |
| `flows?limit=50` | 30d | 1.0 s | 559 MB | 1.3 GB | 239 ms |
| `flows?limit=50` | All | 7.8 s | 4.1 GB | 5.0 GB | 2.5 s |
| `flows/summary` | 30d | 1.4 s | 620 MB | 1.2 GB | |
| `flows/summary` | All | 10.9 s | 4.0 GB | 4.2 GB | |
| `clients/:mac/flows?limit=15` (laptop) | All | 431 ms | 208 MB | 857 MB | |
| `flows/summary?mac=` (laptop) | All | 755 ms | 288 MB | 864 MB | |

For comparison, the endpoints that aggregate in SQL, over all of L:
`stats` 255 ms, `clients` 307 ms, `traffic-measurements` 196 ms, each allocating
under 10 MB in Go.

### Alert rules

`Engine.Evaluate`, which runs after every collection round, i.e. every minute.
"Later" is the median of the next 5 rounds, a minute apart. "First" is the first
round after a rule is created, when event rules look back an hour and raise
everything they find.

| Rules | Later, S | Later, M | Later, L | First, L | Later, M, 1 core |
|---|---|---|---|---|---|
| None (only updating devices) | 15 ms | 14 ms | 13 ms | 16 ms | 12 ms |
| new device | 20 ms | 26 ms | 21 ms | 33 ms | 22 ms |
| no traffic, 5 devices | 20 ms | 20 ms | 22 ms | 23 ms | 17 ms |
| traffic, total, 1h window | 28 ms | 31 ms | 33 ms | 31 ms | 24 ms |
| traffic, total, 24h window | 42 ms | 58 ms | 50 ms | 61 ms | 58 ms |
| traffic, one device, 24h | 39 ms | 46 ms | 52 ms | 58 ms | 54 ms |
| traffic, whole network, 24h | 38 ms | 48 ms | 51 ms | 55 ms | 48 ms |
| traffic, Netflix, 24h | 44 ms | 74 ms | 65 ms | 57 ms | 54 ms |
| port 23, 2323, 445 | 28 ms | 26 ms | 25 ms | 39 ms | 24 ms |
| country, deny CN and RU | 26 ms | 29 ms | 32 ms | 214 ms (49 alerts) | 31 ms |
| country, allow only 5 | 29 ms | 29 ms | 33 ms | 1.9 s (504 alerts) | 26 ms |
| Mix of 8 (all but "allow only") | 116 ms | 159 ms | 139 ms | 364 ms | 172 ms |
| 10 × traffic, 24h | 225 ms | 330 ms | 299 ms | 379 ms | 372 ms |
| 25 × traffic, 24h | 527 ms | 832 ms | 757 ms | 736 ms | 912 ms |
| 50 × traffic, 24h | 1.0 s | 1.6 s | 1.6 s | 1.5 s | 1.7 s |

- **The cost doesn't grow with history.** M and L cost the same: windows are at most
  24 hours, and hourly rows are stored in time order, so DuckDB skips everything
  older.
- **It grows with the number of rules.** Traffic rules repeat the same query, and
  each takes 30–40 ms over the baseline.
- **It grows with the devices and remote addresses in the window.** S has half the
  devices of M and L.
- Previews (`POST /api/alert-rules/preview`) take 7–70 ms on L.
- The first round of an "allow only these countries" rule raised 504 alerts. They
  are inserted one by one, at ~4 ms each. After that, the 24 h cooldown keeps
  rounds at ~30 ms.

**While the dashboard loads.** This loaded the 7-day dashboard for 20 s with the
rules evaluated back to back, which is far more often than once a minute (on L):

| | Dashboard median | p95 | One evaluation | Share of each minute |
|---|---|---|---|---|
| No evaluation | 186 ms | 226 ms | | |
| Mix of 8 rules | 242 ms | 280 ms | 185 ms | 0.3 % |
| 25 × traffic, 24h | 239 ms | 280 ms | 963 ms | 1.6 % |

### Writes

| | S | M | L | M, 1 core |
|---|---|---|---|---|
| Collector, one minute of this network's flows | 189 ms (152 rows) | 421 ms (304) | 392 ms (308) | 248 ms (304) |
| Collector, 2,000 flows | 2.5 s | 2.6 s | 2.6 s | 1.5 s |
| Collector, 10,000 flows | 13.0 s | 13.2 s | 12.4 s | 7.9 s |
| `SyncDevices`, every minute | 9 ms | 16 ms | 10 ms | 8 ms |
| `Compact`, every hour | 12 ms | 20 ms | 14 ms | 9 ms |
| `Compact`, first run with `retention_days: 90` | | 60 ms (4.4 M rows deleted) | 272 ms (13.4 M) | 57 ms |

How the same 2,000 rows can be written (a separate micro-benchmark):

| Method | Per row |
|---|---|
| A prepared `INSERT` per row in a transaction, as both collectors do | 1,150 µs |
| The same with `UBIGINT` instead of `UHUGEINT` columns | 1,130 µs |
| A prepared `INSERT` of 200 rows at a time | 112 µs |
| DuckDB's Appender into a staging table, then `INSERT … SELECT` | 5 µs |

## Findings

In order of how much they matter.

### 1. The app chart and `/api/apps` classify every row in Go

`/api/traffic-measurements?app=` ([api/http.go:223](../api/http.go)) asks
`GetEndpointPairTrafficByHour` for traffic per source, destination *and hour*. Over
a long range that is nearly every hourly row: 16.4 M on L. It then classifies both
addresses of each in Go. `/api/apps` ([api/http.go:200](../api/http.go)) does the
same per address pair. Both build the whole result as a slice of records before
summing it. They hold two heap-allocated `netip.Addr` each, hence gigabytes.

Few of those addresses are distinct (192 k on L, 46 k in a week), and the app depends
only on the remote address. The prototype:

1. selects the distinct remote addresses in the range;
2. classifies them in Go (`Classifier` caches);
3. loads the app's addresses into a temporary table with the Appender;
4. lets DuckDB sum per hour.

That took the chart from 38 s to 0.52 s and `/api/apps` from 3.3 s to 0.54 s, with
allocations in the megabytes. A DuckDB scalar UDF wrapping the classifier would do
the same in one query.

### 2. The flow list and summary load all flows to page them in Go

`serveFlows` and `/api/flows/summary` ([api/http.go:126, 164](../api/http.go)) call
`GetFlows`, which returns every flow in the range. They then filter, sort and page
in Go, or group by host, service and country. On L, "All" is 3.6 M flows for 50
rows. The client page has the same problem on its own scale: 176 k flows for the
laptop, 0.4–0.8 s.

- **Flow list.** Sorting and paging in SQL (`ORDER BY … LIMIT … OFFSET`, with
  `count(*) OVER ()` for `X-Total-Count`) took "All" from 7.8 s to 2.5 s and 30
  days from 1.0 s to 0.24 s, without the 4 GB. The `q` search matches parts of IP
  addresses, which SQL can't format from a `BLOB`. Searches could keep doing that
  in Go, or use a UDF.
- **Summary.** It can group by remote address and by protocol/port in SQL. It then
  needs Go only for the countries of the distinct remote addresses.

### 3. The collector writes slowly, and holds a lock while it does

Both collectors insert with a prepared statement per row:
[collector/netfilter.go:178](../collector/netfilter.go) on Linux and
[collector/flowtable.go:342](../collector/flowtable.go) on macOS. That costs
~1.15 ms a row here, whatever the column types. On Linux, `dumpFlows` holds `lock`
([netfilter.go:188](../collector/netfilter.go)) through the insert. The goroutine
that records destroyed connections needs that lock for every event
([netfilter.go:273](../collector/netfilter.go)).

While it waits, it doesn't read the unbuffered event channel, so the conntrack
listener's only worker stops reading its netlink socket. Once the socket's buffer is
full, the kernel drops events and the next read fails with `ENOBUFS`.
`conntrack`'s worker returns on any receive error. netmon logs it once
([netfilter.go:263](../collector/netfilter.go)) and never listens again. From then
on, connections that open and close between two dumps aren't recorded at all, until
netmon restarts.

This was found by reading the code and couldn't be reproduced here (there is no
conntrack in the test container). But the longer each flush takes, the likelier it
gets: busy networks, slow router CPUs, and many short connections such as DNS.

To fix it:

- write with the Appender (as `perf seed` does), into a staging table then
  `INSERT … SELECT` into `flows`, which needs no `UHUGEINT` support in the
  Appender;
- build the snapshots under the lock, but insert after releasing it, keeping the
  closed flows aside until the insert succeeds;
- set `NETLINK_NO_ENOBUFS` or a larger receive buffer
  (`conntrack.Conn.SetReadBuffer`), and start listening again when the worker exits.

### 4. Every traffic rule runs the same query

`trafficUsage` ([alerting/observe.go:115](../alerting/observe.go)) calls
`GetDeviceTraffic` for each rule. That query returns traffic per device *and remote
address* (24 k rows for 24 hours on L), in 32 ms. Rules with the same window get the
same rows.

- Caching the result per window for one `Evaluate` would make 50 rules cost about
  as much as one.
- Rules without an app and without "whole network" don't need the remote
  addresses. Summed per device in SQL, the query takes 5 ms instead of 32.

At today's cost, a typical set of rules is negligible on x86: 0.14–0.17 s a minute.
On a router a few times slower than these cores, 50 rules would take several
seconds of every minute.

Less important: each alert is a separate `INSERT … RETURNING`
([engine.go:316, 369](../alerting/engine.go)), and each rule a separate `UPDATE` of
`last_evaluated_at`. A transaction per round would make the first round of a broad
event rule (1.9 s for 504 alerts) a fraction of that.

### 5. Client pages always ask for the devices of all time

The client page uses `devicesQuery("all")`
([client/src/routes/clients/$clientId.tsx:232](../client/src/routes/clients/$clientId.tsx)),
which is `/api/clients` without `from`. That query costs 307 ms on L (561 ms on M on
one core) and is most of the client page's time for 24 hours and 7 days.

This is on purpose: who a device is doesn't depend on the range. But all the page
needs from it is the device's name, hostname, vendor and every address it has used.
It sums a year of traffic to get them. `/api/devices` (4 ms) has everything but the
earlier addresses. `SyncDevices` could also keep the (MAC, IP) pairs it sees in a
table, as it does the devices, and the page could use that.

### What is fine

- Endpoints that aggregate in SQL: `stats`, `clients`, `traffic-measurements`,
  `devices`, and the alert pages. Their time grows linearly with the rows in the
  range, to 0.2–0.3 s for a year of 50 devices (0.4–0.6 s for half a year on one
  core). Pre-aggregating per day would make "All" cheaper, but it isn't urgent.
- Compaction and retention:
  - the hourly roll-up takes 10–20 ms;
  - turning on `retention_days: 90` with a year of data deletes 13.4 M rows in 0.27 s.
- `SyncDevices`, `ExpireAlerts`, rule previews: a few milliseconds to tens of
  milliseconds.
- History length doesn't affect alert rules at all.

## How it was measured

`perf seed` generates a network of laptops, phones, tablets, TVs, consoles, IoT
devices and a NAS. Each kind has a daily rhythm, a few endpoints it always talks to,
and remote addresses drawn from a Zipf distribution, some within the networks of
known apps. The data also includes:

- DNS through the router;
- local traffic (to the NAS, casting to the TV);
- port-forwarded connections to the NAS;
- the router's own traffic;
- guests who come and go;
- a camera that went quiet;
- two devices that just joined;
- a smart plug that connects to telnet ports.

Older data is hourly, written in time order as compaction writes it. The last one to
two hours are per minute, as the collector writes them. There are also 5,000
resolved alerts from 30 days. The result is about 41 rows per device and hour.

`perf run` copies the database and runs the real code:

- the web UI's requests through `api.CreateHTTPApp` (handlers, JSON and all);
- `alerting.Engine.Evaluate` with a dispatcher that only counts;
- the `storage` functions for compaction;
- the collector's insert, reproduced statement for statement.

Hostnames are looked up on a local DNS stub that answers NXDOMAIN at once, so DNS
latency doesn't count. Countries come from a synthetic GeoLite2-Country database
with 500 k networks, about as many as a real one. Each case runs once to warm up and
then 5 times (3 for the one-core run), and the median is reported. Slow cases run
fewer times.

The machine had 4 x86-64 cores and 15 GB of RAM, with DuckDB's defaults (4
threads, `memory_limit` 80 % of RAM). The one-core runs used `taskset -c 0` and
`-threads 1`.

Limitations:

- The data is synthetic and IPv4 only. With IPv6, phones' rotating privacy
  addresses would add more addresses per device.
- A router's cores are several times slower than these, so expect its times to be
  a multiple of the one-core column.
- Everything ran with the database in the page cache.
- The conntrack event path (finding 3) was reviewed in the code, not exercised.

## Running it again

From the repository root:

```sh
go run ./perf seed -devices 50 -days 365 -out /tmp/l.db
go run ./perf run -db /tmp/l.db -geoip GeoLite2-Country.mmdb -json /tmp/l.json
```

`-sections` picks some of `api,alerts,contention,write`. Use `-threads` and
`-memory-limit` to set DuckDB's, and `taskset -c 0` for one core. To look at the
data, use `go run ./perf sql -db /tmp/l.db "SELECT …"`. Without `-geoip`, country
rules find nothing, and their timings mean little.
