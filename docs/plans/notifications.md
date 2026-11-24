# Plan: notifications and alert rules

Status: proposal, revision 3 · 2026-11-24 · based on `main` at `b12fcd2`
(after `next` was merged into it)

**Decisions so far:**

| # | Question | Decision |
|---|---|---|
| 1 | Channels | **Use [shoutrrr](https://shoutrrr.nickfedor.com) for all of them.** A channel is a shoutrrr service URL |
| 2 | Auth | **Out of scope for now**; it will be added later (§8) |
| 3 | Should `new_device` ignore randomized MACs by default? | **No.** The filter exists as an option and is off by default |
| 4 | Alert retention | Alert history and the delivery log are kept for **30 days by default**, configurable |
| 5 | Flow retention | **Configurable, and keep everything by default.** This already exists on `main`: `NETMON_RETENTION_DAYS`, applied by `storage.Compact`, default `0` (keep everything); the default stays as it is. Alert retention plugs into the same hourly loop (§3.4) |

## 1. Goal

netmon should tell you when something interesting happens on the home network,
so you don't have to keep the dashboard open:

- "A device I've never seen joined the network"
- "The TV uploaded 3 GB in the last hour", or "the kids' tablet used 5 GB of YouTube today"
- "The NAS hasn't sent any traffic for 30 minutes"
- "The smart plug is talking to port 23, or to a country I don't expect"

This is the README's TODO item "Alerts (new device joined, usage over a
threshold) via webhook, ntfy or Telegram".

Users see two concepts:

| Concept | What it is | Example |
|---|---|---|
| **Notification channel** | *Where* a message goes: a name plus a shoutrrr URL | `telegram://123456:ABC…@telegram?chats=@home`, `ntfy://ntfy.sh/home-net` |
| **Alert rule** | *When* to send: a condition over the collected flows, a scope (which devices), and the channels to notify | "Any device downloads > 5 GB in 1 h → Telegram + ntfy" |

There is also one internal concept, the **alert**: one rule firing for one subject
(usually a device). An alert has a lifecycle (`firing → resolved`) and is kept as
history for 30 days.

## 2. Starting point (current `main`)

```
Linux: conntrack dump every minute (collector/netfilter.go, gocron)  ┐
macOS: pcap → flowTable, flushed every minute (collector/pcap_darwin.go) ┴─► INSERT INTO flows (per minute)
                                                                         │
main.go compactPeriodically, at startup and hourly:                      │
  storage.Compact: minutes of complete hours → flows_hourly, then delete ◄┘
  hourly rows older than NETMON_RETENTION_DAYS
                                                                         │
all_flows view = flows ∪ flows_hourly ─► storage/sql.go ─► api (Fiber v3) ─► React UI
```

**Already done on `main`**, so these items from revision 2 are dropped:

- Storage returns errors instead of calling `log.Fatal`.
- The GeoIP database is opened once.
- Reverse DNS is configurable (`NETMON_LOCAL_DNS`, `NETMON_PUBLIC_DNS`), cached and
  has timeouts.
- An SPA fallback serves client routes such as `/alerts/42`. It is registered
  after the API routes.
- The Dockerfile uses `golang:1.27`, and `go.mod` pins `toolchain go1.27.4`.
- Device names (`device_names`) and MAC vendor lookup (`vendors.Lookup`), which
  are useful for message text.
- Per-app traffic (`apps` package).
- Tests: `storage/sql_test.go` has `openTestDB`, an in-memory DuckDB with the real
  migrations, and `api/*_test.go` exists.

**What's still missing for alerting:**

1. **There's no flush hook.** Neither collector tells anyone that a batch was
   written. Add `collector.Options{OnFlush func(time.Time)}` to all three
   `CollectTraffic` implementations (`netfilter.go`, `pcap_darwin.go`,
   `unsupported.go`). Call it after the insert commits, and also on minutes with
   no flows, so time-based rules still run.
2. **There are no first-seen or last-seen times per device.** `device_names` only
   holds devices that were renamed. Add a `devices` table (§3.1).
3. **Rollups break short sliding windows.** `Compact` rolls up everything before
   the start of the current hour. After the 11:xx compaction, a 1-hour window
   evaluated at 11:25 sees only the raw rows from 11:00 to 11:25; 10:25 to 10:59
   is now one hourly row stamped 10:00. **Fix:** keep one more complete hour as
   raw minutes:

   ```go
   cutoff := now.Add(-time.Hour).Truncate(time.Hour)
   ```

   API queries read `all_flows`, so they're unaffected, and `flows` holds 60 to
   120 minutes of rows instead of 0 to 60. Update the `Compact` tests in
   `storage/sql_test.go`.
4. **The lookups are private to `api`.** `countryLookup` (`api/geo.go`) and
   `hostnameResolver` (`api/hostnames.go`) are unexported, and alerting needs
   both. Move them into an exported `lookup` package, construct them once in
   `main.go`, and pass them to both `api.CreateHTTPApp` and the alert engine.
5. **The `go` directive is still `1.26.0`.** `go get
   github.com/nicholas-fedor/shoutrrr@v0.25.0` raises it to `go 1.27`, which
   matches the toolchain and Dockerfile already in use. I checked this together
   with `duckdb-go` v2.20001.0.

DuckDB facts this design relies on. I checked each against `duckdb-go`
v2.20001.0, which bundles DuckDB v2.0.1:

- ✅ Sequences, `BLOB PRIMARY KEY`, `INSERT … SELECT … GROUP BY … ON CONFLICT DO
  UPDATE` (an idempotent upsert), `least`/`greatest`, `RETURNING`, and
  `TIMESTAMPTZ - INTERVAL`, which is now supported.
- ❌ `FOREIGN KEY … ON DELETE CASCADE` still gives a parser error. Do cascades in
  Go, inside a transaction, and skip FK constraints.
- Following the style of `Compact(db, now, …)`, evaluators and expiry functions
  take `now` and cutoffs as Go parameters, so tests stay deterministic.

## 3. Data model

Each new migration ships with its milestone. The current latest is `003_device_names.sql`.

### 3.1 `004_devices.sql` (milestone 0)

```sql
-- +migrate Up
-- When each MAC address was first and last seen in flows.
CREATE TABLE devices (
    mac        BLOB PRIMARY KEY,
    last_ip    BLOB,
    first_seen TIMESTAMPTZ NOT NULL,
    last_seen  TIMESTAMPTZ NOT NULL
);

-- Seed from all history, including hourly rollups, so the first run
-- doesn't report every existing device as "new".
INSERT INTO devices
SELECT mac, arg_max(ip, created_at), min(created_at), max(created_at)
FROM (
    SELECT src_mac AS mac, src_ip AS ip, created_at FROM all_flows WHERE src_mac IS NOT NULL
    UNION ALL
    SELECT dst_mac, dst_ip, created_at FROM all_flows WHERE dst_mac IS NOT NULL
)
GROUP BY mac;

-- +migrate Down
DROP TABLE devices;
```

The table is kept up to date by **`storage.SyncDevices(db, since)`**, which the
alert engine runs first in every cycle. It's one idempotent statement, so neither
collector needs to change:

```sql
INSERT INTO devices
SELECT mac, arg_max(ip, created_at), min(created_at), max(created_at) FROM (
    SELECT src_mac AS mac, src_ip AS ip, created_at FROM flows WHERE src_mac IS NOT NULL AND created_at >= ?
    UNION ALL
    SELECT dst_mac, dst_ip, created_at FROM flows WHERE dst_mac IS NOT NULL AND created_at >= ?
) GROUP BY mac
ON CONFLICT (mac) DO UPDATE SET
    last_ip    = CASE WHEN excluded.last_seen >= devices.last_seen THEN excluded.last_ip ELSE devices.last_ip END,
    first_seen = least(devices.first_seen, excluded.first_seen),
    last_seen  = greatest(devices.last_seen, excluded.last_seen);
```

`since` is `now − 5 min` in each cycle, and `now − 2 h` at startup to cover all
raw rows. Re-running the statement changes nothing; I tested that.

### 3.2 `005_notification_channels.sql` (milestone 1)

```sql
-- +migrate Up
CREATE SEQUENCE notification_channels_id_seq;
CREATE TABLE notification_channels (
    id         INTEGER PRIMARY KEY DEFAULT nextval('notification_channels_id_seq'),
    name       VARCHAR NOT NULL,
    service    VARCHAR NOT NULL,           -- shoutrrr scheme, derived from url: telegram | ntfy | discord | smtp | generic | …
    url        VARCHAR NOT NULL,           -- shoutrrr service URL; contains secrets, never returned by the API
    enabled    BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT current_timestamp,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT current_timestamp
);

-- +migrate Down
DROP TABLE notification_channels;
DROP SEQUENCE notification_channels_id_seq;
```

### 3.3 `006_alerts.sql` (milestone 2)

```sql
-- +migrate Up
CREATE SEQUENCE alert_rules_id_seq;
CREATE TABLE alert_rules (
    id                INTEGER PRIMARY KEY DEFAULT nextval('alert_rules_id_seq'),
    name              VARCHAR NOT NULL,
    type              VARCHAR NOT NULL,    -- new_device | traffic_threshold | device_inactive | destination_port | destination_country
    params            VARCHAR NOT NULL,    -- JSON, type-specific, includes scope (see §5.1)
    severity          VARCHAR NOT NULL DEFAULT 'warning',   -- info | warning | critical
    repeat_interval_s INTEGER,             -- stateful: reminder while firing; event: per-subject cooldown
    notify_on_resolve BOOLEAN NOT NULL DEFAULT true,
    enabled           BOOLEAN NOT NULL DEFAULT true,
    last_evaluated_at TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT current_timestamp,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT current_timestamp
);

CREATE TABLE alert_rule_channels (
    rule_id    INTEGER NOT NULL,
    channel_id INTEGER NOT NULL,
    PRIMARY KEY (rule_id, channel_id)
);

CREATE SEQUENCE alerts_id_seq;
CREATE TABLE alerts (
    id                INTEGER PRIMARY KEY DEFAULT nextval('alerts_id_seq'),
    rule_id           INTEGER NOT NULL,
    rule_name         VARCHAR NOT NULL,    -- snapshot, so history survives rule deletion
    subject           VARCHAR NOT NULL,    -- dedup key: MAC, "network", "aa:bb..|23/tcp", …
    state             VARCHAR NOT NULL,    -- firing | resolved
    severity          VARCHAR NOT NULL,
    value             DOUBLE,              -- observed value (bytes, seconds, …)
    title             VARCHAR NOT NULL,
    body              VARCHAR NOT NULL,
    started_at        TIMESTAMPTZ NOT NULL,
    last_evaluated_at TIMESTAMPTZ NOT NULL,
    last_notified_at  TIMESTAMPTZ,
    resolved_at       TIMESTAMPTZ
);

CREATE SEQUENCE notification_deliveries_id_seq;
CREATE TABLE notification_deliveries (
    id         INTEGER PRIMARY KEY DEFAULT nextval('notification_deliveries_id_seq'),
    alert_id   INTEGER,                    -- NULL for test messages
    channel_id INTEGER NOT NULL,
    kind       VARCHAR NOT NULL,           -- firing | reminder | resolved | test
    status     VARCHAR NOT NULL,           -- sent | failed
    attempts   INTEGER NOT NULL,
    error      VARCHAR,                    -- redacted (§4.4)
    created_at TIMESTAMPTZ NOT NULL DEFAULT current_timestamp
);

-- +migrate Down
DROP TABLE notification_deliveries; DROP SEQUENCE notification_deliveries_id_seq;
DROP TABLE alerts;                  DROP SEQUENCE alerts_id_seq;
DROP TABLE alert_rule_channels;
DROP TABLE alert_rules;             DROP SEQUENCE alert_rules_id_seq;
```

### 3.4 Retention

Both kinds of retention are configurable and run in the same place:
`compactPeriodically` in `main.go`, at startup and then every hour.

| Data | Variable | Default | Applied by |
|---|---|---|---|
| Flows (`flows_hourly`) | `NETMON_RETENTION_DAYS` | `0`, keep everything | `storage.Compact`, **already on `main`** |
| Alerts + delivery log | `NETMON_ALERT_RETENTION_DAYS` (new) | `30` | `storage.ExpireAlerts`, new in milestone 2 |

`storage.ExpireAlerts(db, now, retention)` runs after `Compact` in the same
loop. It uses one transaction, and `0` keeps everything, as with flows:

```sql
DELETE FROM notification_deliveries WHERE created_at < ?;          -- now - retention
DELETE FROM alerts WHERE state = 'resolved' AND resolved_at < ?;   -- alerts still firing are never deleted
```

Validate the new variable the same way `NETMON_RETENTION_DAYS` is validated: a
non-negative integer, otherwise `log.Fatalf` at startup. Add it to the README's
Configuration table, and extend the log line, e.g. "…, 12 alerts expired". Flow
retention needs no new work. Every rule window is at most 24 h, and `Compact`
never expires the current day's data (the minimum retention is 1 day), so the
two can't interfere.

## 4. Notification channels (shoutrrr)

### 4.1 Library

- **Use `github.com/nicholas-fedor/shoutrrr` v0.25.0**, the maintained fork
  (released 2026-11-17). It has releases roughly every week and supports 33 URL
  schemes. The original `containrrr/shoutrrr` stopped at v0.8.0 in 2023.
- v0.20 and later need Go 1.27. The repo's toolchain and Dockerfile already use
  1.27, and adding the module raises the `go` directive (§2, item 5).

### 4.2 A channel is a URL

Example URLs for the UI. Full reference:
<https://shoutrrr.nickfedor.com/latest/services/overview/>

| Service | URL format |
|---|---|
| Telegram | `telegram://<bot-token>@telegram?chats=<chat-id or @channel>` |
| ntfy | `ntfy://[:<token>@]ntfy.sh/<topic>` (or a self-hosted host) |
| Discord | `discord://<token>@<webhook-id>` |
| Email | `smtp://<user>:<pass>@<host>:587/?fromAddress=<from>&toAddresses=<to1>,<to2>` |
| Gotify | `gotify://<host>/<app-token>` |
| Pushover | `pushover://shoutrrr:<api-token>@<user-key>/` |
| Slack | `slack://hook:<token>@webhook` |
| Webhook (JSON) | `generic://<host>/<path>?template=json[&@Authorization=Bearer+<token>]` |

- **Validate on save without sending anything:** use
  `router.NewWithOptions(nil, types.SenderOptions{}).Locate(url)`. I checked that a
  malformed Telegram token is rejected with `telegram: invalid telegram token`.
  The API returns that error as a 400.
- **Store** the raw URL. The `service` column is the scheme, kept for display and
  filtering.

### 4.3 Sending

Package `notify`, files `notify/{dispatcher.go, message.go, redact.go}`:

```go
type Message struct {
    Title    string
    Body     string            // plain text
    Severity Severity          // info | warning | critical
    Event    string            // alert.firing | alert.reminder | alert.resolved | test
    Fields   map[string]string // structured data, sent only to generic:// (webhook) channels
}

func send(ctx context.Context, channel Channel, msg Message) error {
    params := types.Params{}
    params.SetTitle(severityEmoji(msg.Severity) + " " + msg.Title)
    params.SetLevel(levelFor(msg.Severity)) // info→Info, warning→Warning, critical→Error
    if channel.Service == "generic" {
        maps.Copy(params, msg.Fields)        // ends up in the JSON body
    }
    return shoutrrr.SendContext(ctx, channel.URL, msg.Body, &params)
}
```

Rules this sketch follows. I confirmed each against v0.25.0:

- **Send only `title` and `level` to normal services.** They are reserved params:
  services that support them use them, and the rest ignore them. Any other param
  makes the send fail. For example, Telegram with `priority` returns
  `setting param "priority": not a valid config key`.
- **`level` drives severity where a service supports it.** ntfy maps
  Warning→high and Error→max priority. Discord picks the embed colour. Gotify,
  Pushover, Opsgenie and PagerDuty also use it. A setting in the user's URL (such
  as `?priority=5`) still takes precedence. For all other services, the emoji
  prefix in the title carries the severity.
- **Webhook channels** (`generic://…?template=json`) put every extra param into
  the JSON body. That makes it the structured integration point, for example for
  Home Assistant or n8n. Tested payload:

  ```json
  {"title":"⚠️ Large download on Living room TV","message":"Living room TV (Samsung, 3c:22:fb:12:34:56, 192.168.1.40) downloaded 6.2 GB in the last 1h (threshold 5 GB).",
   "level":"Warning","event":"alert.firing","alert_id":"42","rule_id":"3","rule_name":"Big downloads",
   "rule_type":"traffic_threshold","severity":"warning","subject":"3c:22:fb:12:34:56","value":"6657199308",
   "mac":"3c:22:fb:12:34:56","ip":"192.168.1.40","device_name":"Living room TV","vendor":"Samsung",
   "hostname":"tv.lan.","started_at":"2026-11-24T18:03:00Z","url":"http://netmon.lan:2137/alerts/42","payload_version":"1"}
  ```

  Every value is a string. Field names must **not** collide with generic's config
  keys (`title`, `method`, `template`, `contenttype`, `titlekey`, `messagekey`,
  `disabletls`, `skiptlsverify`), because a matching param reconfigures the
  request instead of becoming a field.
- **Plain-text bodies.** netmon never sets `parsemode` or `usehtml`. If a user adds
  `parsemode=HTML` or `Markdown` to a Telegram URL, names containing `_`, `<` and
  similar characters may break the formatting. Document this next to the URL field.
- **Use a one-shot `shoutrrr.SendContext` per message** instead of a long-lived
  router per channel. Each attempt gets a 10 s `context.WithTimeout`, and there's
  no router cache to invalidate when a channel is edited.

### 4.4 Secrets in URLs

- **The API never returns `url`.** It returns `Service` and `MaskedURL`, built with
  `format.MaskedURL(format.GetServiceConfig(svc))` after `Locate`. Tested output:
  - `telegram://REDACTED@telegram?chats=%40mychan`
  - `discord://REDACTED@123456`
  - `smtp://user:REDACTED@smtp.example.com:587/?…`
  - `ntfy://ntfy.sh/home-net` (the token is dropped)
- **Editing:** a `PUT` without `url` keeps the stored URL, so you can rename or
  toggle a channel without re-pasting secrets. To change any part of the URL, paste
  the whole URL again.
- **Stored errors:** shoutrrr prefixes errors with the service ID, not the URL.
  However, a wrapped `net/http` error can contain the request URL, and Telegram
  puts the bot token in the path. Before storing or logging an error, replace
  every userinfo value, path segment and query value of at least 8 characters from
  the channel URL with `REDACTED`. Never log channel URLs.
- URLs are stored in plain text in `flows.db`. Encrypting them belongs with the
  auth work (§8).

### 4.5 Dispatcher

- A buffered queue (256 jobs) and 4 workers, so a slow SMTP server doesn't block
  Telegram.
- Up to 3 attempts per message, with backoff of 2 s, 10 s and 30 s. shoutrrr's
  errors don't expose HTTP status codes, so every send error is retried. Validation
  errors from `Locate` are not retried; they fail immediately.
- Every send is written to `notification_deliveries`, with `attempts` and the
  redacted error. The UI shows this on the alert detail page.
- **Per-channel rate limit** (for example 20 messages per 5 minutes). Overflow
  becomes a single "N more alerts suppressed" message.
- **Grouping:** when one evaluation cycle produces more than 3 alerts for the same
  channel, send one combined message.
- The queue is in-memory. On shutdown, drain for up to 5 s, then drop what's left
  and log it. Alerts are persisted, so history isn't lost. Shutdown order in
  `main.go`: stop the collector (its `Shutdown` already waits for the final flush),
  then the engine, then `dispatcher.Close(5s)`, then Fiber. After that,
  `<-compactionDone` as today.

### 4.6 Later: generated channel forms

`format.GetServiceConfigFormat(service)` returns each field's `Name`,
`Description`, `DefaultValue`, `Required`, `Sensitive` and `Keys`. For Telegram,
`Token` is required and sensitive and `Chats` is required. Fill a config with
`format.SetConfigField` and build the URL with `config.GetURL()`. That would
replace "paste a URL" with a real form for every service. It's planned for
milestone 4.

## 5. Alert rules

### 5.1 Rule types

Every rule's `params` includes `"scope": {"macs": [...]}`; an empty list means all
devices. Durations are stored in seconds.

| Type | Kind | Params | Subject | Fires when | Resolves when |
|---|---|---|---|---|---|
| `new_device` | event | `ignore_random_macs` (default **`false`**) | MAC | a device's `first_seen` is after the previous evaluation (and after the rule's `created_at`) | n/a (one-shot) |
| `traffic_threshold` | stateful | `direction` in/out/total, `threshold_bytes`, `window_s` (300…86400), optional `app` (a name from `/api/apps`), optional `aggregate: "network"` | MAC, or `network` | sum over the sliding window > threshold | sum < 90 % of threshold (hysteresis) |
| `device_inactive` | stateful | `inactive_s`; **scope required** | MAC | `now - devices.last_seen > inactive_s` | the device is seen again |
| `destination_port` | event | `ports` [], `proto` tcp/udp/any | `mac\|port/proto` | a flow since the last evaluation matches | n/a |
| `destination_country` | event | `mode` deny/allow, `countries` [ISO codes] | `mac\|country` | a public destination's country is in the deny list (or missing from the allow list) | n/a |

Details:

- **Random MACs.** Phones use private Wi-Fi addresses, which have the
  locally-administered bit set (`mac[0] & 0x02 != 0`). By default netmon alerts on
  these too. The rule form shows the option with a hint: "Enable if phones that
  rotate their Wi-Fi address cause repeated alerts." `vendors.Lookup` already
  returns nothing for randomized addresses, so their messages show "unknown vendor".
- **Windows and rollups** (with the `Compact` change from §2, item 3):
  - Windows up to 1 h are exact. They read per-minute rows from `flows`, which
    now always covers the last full hour.
  - Windows from 1 h to 24 h read `all_flows`: per-minute rows inside the window,
    plus hourly rows whose hour starts inside it. That rounds the window to whole
    hours, as the API's README already says for ranges, and undercounts at most
    part of one hour.
  - The minimum window is 5 minutes, because data arrives in 1-minute batches.
- **Traffic direction** uses the same client-side orientation as the API with a
  `mac` filter ("what it received is download"). Reuse `FlowsFilter` and its
  `outbound()` helper from `storage/sql.go`, so alert numbers match the client
  page exactly.
- **`app` filter:** reuse `storage.GetEndpointPairTraffic` and
  `apps.Default()`, the same matching that `/api/apps` uses. Example: "YouTube > 5
  GB in 24 h on the kids' tablet".
- **Event rules** read only rows since the last evaluation. That start time is
  capped at `now − 1 h`, so a restart after long downtime doesn't produce a storm
  of alerts from old data.
- **`destination_country`** uses the shared `lookup` country reader (§2, item 4),
  looks up only the distinct public `dst_ip` values in the batch, and caches the
  results.
- **Event-rule cooldown:** do not notify the same `(rule, subject)` again within
  `repeat_interval_s`. The default is 24 h for event rules and "never" for
  stateful rules.

### 5.2 Package `alerting`

```go
type Observation struct {
    Subject string
    Value   float64
    Labels  map[string]string // mac, ip, device_name, vendor, hostname, country, port, app…
}

type Evaluator interface {
    Kind() Kind // Stateful | Event
    Evaluate(ctx context.Context, db *sql.DB, rule Rule, since, now time.Time) ([]Observation, error)
}

type Engine struct { /* db, dispatcher, lookups, evaluators map[string]Evaluator, clock, trigger chan time.Time */ }

func (e *Engine) Trigger(t time.Time)        // non-blocking; chan of size 1, so triggers coalesce
func (e *Engine) Run(ctx context.Context)    // the loop
```

Layout: `alerting/{engine.go, rules.go (types, params, validation), evaluators.go,
templates.go}`, `storage/{alerts.go, channels.go, rules.go}`, plus
`storage.SyncDevices` and `storage.ExpireAlerts`.

**One evaluation cycle** runs after every flush, about once a minute:

1. `storage.SyncDevices(db, now − 5 min)`.
2. Load the enabled rules and their channel IDs.
3. For each rule, run its evaluator with a 10 s timeout and `recover`. On error,
   log it and continue with the next rule.
4. Reconcile the result with the open alerts for that rule:
   - **Stateful rules.**
     - Firing subject with no open alert: insert a `firing` alert and notify `alert.firing`.
     - Firing subject with an open alert: update `value` and `last_evaluated_at`.
       If `repeat_interval_s` has passed since `last_notified_at`, notify `alert.reminder`.
     - Open alert whose subject is no longer firing: set `resolved`, and notify
       `alert.resolved` if `notify_on_resolve` is set.
   - **Event rules.** For each observation not covered by the cooldown, insert an
     alert that is already `resolved` (`resolved_at = started_at`) and notify
     `alert.firing`.
5. Set `rule.last_evaluated_at = now`.

Because open alerts live in the database, a restart doesn't re-send "firing"
messages. Editing a rule resolves its open alerts **silently**. Deleting a rule
removes its channel links and resolves its open alerts, but keeps the history
(`rule_name` is a snapshot) until retention removes it.

**Wiring** in `main.go`:

```go
lookups    := lookup.New(lookup.Options{GeoIPDatabase: "GeoLite2-Country.mmdb", LocalDNS: …, PublicDNS: …})
dispatcher := notify.NewDispatcher(db)
engine     := alerting.NewEngine(db, dispatcher, lookups)
go engine.Run(ctx)

coll, err := collector.CollectTraffic(db, collector.Options{OnFlush: engine.Trigger})
app := api.CreateHTTPApp(db, assets, api.Options{Lookups: lookups, Dispatcher: dispatcher, …})
```

Both collectors flush every minute, including minutes with no flows (§2, item 1),
so `device_inactive` sees time pass without needing a separate timer. On
unsupported platforms there is no collector, so no data arrives and nothing is
evaluated.

### 5.3 Message text

Each rule type has a default `text/template` for the title and body. A device is
named by the first of these that exists: its **device name** (`device_names`),
its reverse-DNS **hostname**, its **vendor**, or the MAC address. The MAC, IP and
vendor follow in brackets. The emoji prefix comes from severity (ℹ️ / ⚠️ / 🚨),
and resolved messages use ✅.

- `New device: Samsung 3c:22:fb:12:34:56` / `First seen 18:03 as 192.168.1.40 (galaxy.lan.).`
- `Large download on Living room TV` / `Living room TV (Samsung, …) downloaded 6.2 GB in the last 1h (threshold 5 GB).`
- `Resolved: Large download on Living room TV` / `Back under threshold after 47 min.`
- `NAS is inactive` / `No traffic since 17:20 (45 min).`

Bodies end with the deep link `NETMON_PUBLIC_URL/alerts/:id`, which defaults to
`http://<hostname>:2137` (the SPA fallback already serves it). Byte formatting
matches the UI's `pretty-bytes` output. Custom templates per rule come in
milestone 4.

## 6. HTTP API

These follow the conventions already in `api/`:

- Routes are registered by a `register…Routes(app, …)` function, as in
  `registerDeviceRoutes`.
- Response DTOs have **no JSON tags** (PascalCase), mirrored in
  `client/src/lib/api.ts`. Request bodies are lower-case, like `{"name": …}`.
- Errors go through `badRequest`, which answers `400 {"error": "..."}`.

| Method & path | Purpose |
|---|---|
| `GET /api/notification-services` | A curated list of common services (Telegram, ntfy, Discord, email, Gotify, Pushover, Slack, Webhook) with an example URL and a docs link, plus every scheme from `router.SupportedSchemas()` |
| `GET /api/notification-channels` | list: `ID, Name, Service, MaskedURL, Enabled, UsedByRules` |
| `POST /api/notification-channels` | create `{"name", "url", "enabled"}`; the URL is validated with `Locate` |
| `GET/PUT/DELETE /api/notification-channels/:id` | read, update (no `url` = keep the stored one), delete (also unlinks it from rules) |
| `POST /api/notification-channels/:id/test` | send a test message through a saved channel |
| `POST /api/notification-channels/test` | send a test message to an **unsaved** URL from the form |
| `GET /api/alert-rule-types` | param specs per rule type (drives the rule form) |
| `GET /api/alert-rules` | list, with channel IDs and the count of firing alerts |
| `POST /api/alert-rules` | create |
| `GET/PUT/DELETE /api/alert-rules/:id` | read, update, delete |
| `PATCH /api/alert-rules/:id` | `{"enabled": false}` (toggle from the list) |
| `POST /api/alert-rules/preview` | **dry run** of a rule body against current data: returns the observations, sends nothing |
| `GET /api/alerts?state=&rule_id=&mac=&limit=&offset=` | firing alerts and history (`X-Total-Count`, as `/api/flows` does) |
| `GET /api/alerts/:id` | detail, including deliveries |
| `GET /api/devices` | **new:** every device with `Name`, `Vendor`, `Hostname`, `LastIP`, `FirstSeen`, `LastSeen`, for pickers. `PUT/DELETE /api/devices/:mac/name` already exist |

Add the new endpoints and `NETMON_ALERT_RETENTION_DAYS` to the README's API and
Configuration sections.

## 7. Frontend

Follow the current structure: non-lazy file routes with a `route.tsx` layout,
types in `src/lib/api.ts`, query factories in `src/lib/queries.ts`, and mutations
inside components, as `rename-device.tsx` does with `useMutation`,
`invalidateQueries` and the shadcn `Field` component.

```
client/src/routes/alerts/route.tsx               Layout (page header, tabs: Alerts · Rules)
client/src/routes/alerts/index.tsx               "Firing now" + history (data-table.tsx)
client/src/routes/alerts/$alertId.tsx            Detail + delivery log (target of message links)
client/src/routes/alerts/rules/index.tsx         Rules list with enable switch
client/src/routes/alerts/rules/new.tsx           Create (search params ?type=&mac= to prefill)
client/src/routes/alerts/rules/$ruleId.tsx       Edit
client/src/routes/settings/notifications.tsx     Channels list + add/edit dialog + "Send test"
```

- **Sidebar** (`components/app-sidebar.tsx` nav list): add **Alerts** (lucide
  `BellIcon`, with a `badge` showing the firing count; poll
  `/api/alerts?state=firing` every 30 s) and **Settings** (`SettingsIcon`).
- **shadcn components to add** with `npx shadcn@latest add`: `select`, `switch`,
  `checkbox`, `alert-dialog` (delete confirmation) and `sonner` (toasts). `field`,
  `dialog`, `badge`, `textarea`, `empty`, `dropdown-menu` and `toggle-group` are
  already there. There's no need for `react-hook-form`; follow the `Field` plus
  local-state pattern of `rename-device.tsx`.
- **Channel form (MVP):**
  - Name.
  - A service picker from `/api/notification-services`. Choosing one fills the URL
    field's placeholder with its example and links to the shoutrrr docs page for
    that service.
  - A URL field (password-style input with a reveal toggle, using `input-group`).
    When editing, it shows `MaskedURL` read-only and a "Replace URL" button.
  - **Send test**, which calls the unsaved-URL endpoint and shows the `Locate` or
    send error verbatim.
- **Rule form:**
  - Name and type.
  - Type-specific params: threshold with a MB/GB unit picker; window as a
    `toggle-group` of 5 m/15 m/1 h/6 h/24 h; an optional app from `/api/apps`; a
    device multi-select from `/api/devices` that shows the name, else the
    hostname, else the vendor; port and country inputs; `ignore_random_macs`,
    off by default.
  - Then channels, severity, repeat interval and notify-on-resolve.
  - A **Preview** button that lists what would fire right now.
- **Entry points:**
  - "Create alert" on `clients/$clientId.tsx`, prefilled with the MAC.
  - An `empty` state on the rules page with presets: "Tell me about new devices"
    and "Tell me when a device uses more than 10 GB a day".

## 8. Security

**Auth is deferred** by decision. The README already tracks it: "Add optional
authentication and restrict CORS". Until then, anyone who can reach port 2137 can
read and change channels and rules. This plan still does the parts the UI needs
anyway:

- Channel URLs are write-only in the API (`MaskedURL` only), and errors are
  redacted before they are stored or logged (§4.4).

These are left for the auth work: restricting CORS (`cors.New` still allows any
origin), an auth token or login, encrypting channel URLs at rest, and SSRF limits
on `generic://` targets.

## 9. Testing

Build on the existing helpers: `openTestDB` in `storage/sql_test.go` (in-memory
DuckDB plus the real migrations) and the `api/*_test.go` setup.

- **storage:**
  - `SyncDevices`: first and last seen, the latest IP, idempotence.
  - The `004` seed reading `all_flows`.
  - `ExpireAlerts`: expired resolved alerts are deleted and firing ones kept.
  - The new `Compact` cutoff, including updates to the existing `Compact` tests.
- **notify:**
  - Send to `generic://<httptest host>/hook?template=json&disabletls=yes`. This
    works offline, and I checked it. Assert the title, message, level and
    structured fields, and that no extra params reach non-generic services.
  - Test redaction, and that `MaskedURL` never contains a secret.
  - Test retry, rate-limit and grouping with a fake send function.
- **alerting:**
  - Evaluators over fixture flows: windows of 1 h or less read raw minutes;
    longer windows mix raw and hourly rows; the `app` filter works.
  - Engine state machine with a fake clock and a fake dispatcher. Check: fires
    once; no duplicate; reminder; resolve with hysteresis; no re-fire after a
    restart; event cooldown; the 1-hour cap on `since`; one failing rule doesn't
    block the others.
- **api:** `app.Test()` for CRUD, `Locate` errors as `400 {"error"}`,
  `MaskedURL`, and "a `PUT` without `url` keeps the stored one".
- **Manual end to end** on Linux and macOS: an ntfy.sh topic plus a Telegram bot,
  a `new_device` rule, and a phone joining the network.

## 10. Milestones

| # | Milestone | Contents | Done when |
|---|---|---|---|
| 0 | **Groundwork** | `lookup` package (moved out of `api/`); `collector.Options{OnFlush}` on Linux, macOS and unsupported, firing on empty minutes too; `004_devices.sql` + `storage.SyncDevices` + `GET /api/devices`; `Compact` keeps the last complete hour raw | Existing UI and tests unchanged; `devices` fills; a 1 h window right after compaction counts the full hour |
| 1 | **Channels** | shoutrrr v0.25.0 (`go` directive → 1.27); `005_notification_channels.sql`; `notify` dispatcher + delivery log + redaction; channel API + test endpoints; Settings → Notifications page | You can paste a Telegram or ntfy URL in the UI and receive a test message |
| 2 | **Rules MVP** | `006_alerts.sql`; engine; `new_device` + `traffic_threshold` (with `app`); rules API; rules list + form; alerts list; `NETMON_ALERT_RETENTION_DAYS` (default 30) + `storage.ExpireAlerts` in the hourly loop; README updates (tick the Alerts TODO) | A phone joining the network produces a message within about a minute; a big download produces firing, then resolved; alerts older than 30 days disappear |
| 3 | **More rules + UX** | `device_inactive`, `destination_port`, `destination_country`; preview/dry run; alert detail + delivery log; sidebar badge; presets; "Create alert" from the client page | All five rule types are usable from the UI |
| 4 | **Polish** (optional) | Generated per-service channel forms (§4.6); calendar windows ("per day", "per month") for `traffic_threshold`, which covers the README's ISP data-cap TODO; custom templates; quiet hours and "mute until"; digest mode | n/a |
| n/a | **Auth** (separate work) | Login or token, CORS, secret encryption, SSRF limits | n/a |

## 11. Open questions

None. All decisions are recorded at the top of this document.
