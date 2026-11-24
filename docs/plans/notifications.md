# Plan: notifications and alert rules

Status: proposal, revision 2 · 2026-11-24

**Decisions so far** (from review of revision 1):

| # | Question | Decision |
|---|---|---|
| 1 | Which channels first? | **Use [shoutrrr](https://shoutrrr.nickfedor.com) for all of them.** A channel is a shoutrrr service URL, so there are no hand-written senders |
| 2 | Auth | **Out of scope for now**; it will be added later (§8) |
| 3 | Should `new_device` ignore randomized MACs by default? | **No.** The filter exists as an option and is off by default |
| 4 | Retention | Alert history and the delivery log are kept for **30 days by default**, configurable |

## 1. Goal

netmon should tell you when something interesting happens on the home network,
so you don't have to keep the dashboard open:

- "A device I've never seen joined the network"
- "The TV uploaded 3 GB in the last hour"
- "The NAS hasn't sent any traffic for 30 minutes"
- "The smart plug is talking to port 23, or to a country I don't expect"

Users see two concepts:

| Concept | What it is | Example |
|---|---|---|
| **Notification channel** | *Where* a message goes: a name plus a shoutrrr URL | `telegram://123456:ABC…@telegram?chats=@home`, `ntfy://ntfy.sh/home-net` |
| **Alert rule** | *When* to send: a condition over the collected flows, a scope (which devices), and the channels to notify | "Any device downloads > 5 GB in 1 h → Telegram + ntfy" |

There is also one internal concept, the **alert**: one rule firing for one subject
(usually a device). An alert has a lifecycle (`firing → resolved`) and is kept as
history for 30 days.

## 2. How it fits the current code

The current pipeline:

```
conntrack DESTROY events + 1-min dump (collector/netfilter.go, gocron)
  → INSERT INTO flows (DuckDB, migrations/001_init.sql)
  → storage/sql.go → api/http.go (Fiber :2137) → React UI (client/)
```

The pipeline after this work:

```
collector.dumpFlows ──► INSERT flows ──► UPSERT devices
        │
        └─ OnFlush(now) ──► alerting.Engine (own goroutine, coalescing trigger)
                               │  for each enabled rule: evaluator(rule, now) → observations
                               │  reconcile with open alerts (dedup / reminder / resolve)
                               ▼
                         notify.Dispatcher ──► queue ──► shoutrrr.SendContext(url, msg, params)
                                                     └─► notification_deliveries (log)
```

Problems in the current code that this plan has to handle:

1. **`log.Fatal` on every query error** (`storage/sql.go`, and the insert in
   `dumpFlows`). If a rule query failed, the whole process would exit. All new
   storage code returns errors, and the engine isolates each rule with an error
   check plus `recover`.
2. **GeoIP and DNS live inside `api/http.go`.** `getCountry` opens the `.mmdb` file
   on every call, and `getHostname` hardcodes `192.168.1.1:54` and `8.8.8.8:53`.
   Alert messages need both, so move them into a shared `lookup` package with one
   opened reader, a small TTL cache and a configurable resolver.
3. **There is no device table.** Clients are computed with `GROUP BY` over `flows`.
   The `new_device` and `device_inactive` rules need first-seen and last-seen times
   per MAC, so add a `devices` table that is updated on every flush.
4. **SPA deep links return 404.** The filesystem middleware is registered *before*
   the API routes and has no `NotFoundFile`. Notifications will contain links like
   `/alerts/42`, so a fallback to `index.html` is needed. Setting `NotFoundFile`
   alone would also swallow `/api/*`, because the static middleware runs first.
   Use `Next: skip /api` together with `NotFoundFile: "client/dist/index.html"`.
   `PathPrefix` is *not* applied to `NotFoundFile`; I checked this in fiber v2.52.5.
5. **The Go version is too old for shoutrrr.** The repo targets Go 1.23
   (`go.mod`, and `golang:1.23` in the `Dockerfile`), and Go 1.23 is no longer
   supported. Current releases of the maintained shoutrrr fork need Go 1.27 (§4.1).
6. **There are no tests yet.** This feature adds the first ones (§9).

DuckDB facts this design relies on. I checked each against go-duckdb v1.8.3, which
bundles DuckDB v1.1.3, under both Go 1.24 and Go 1.27.4:

- ✅ Sequences for IDs, `BLOB PRIMARY KEY`, `INSERT … ON CONFLICT (mac) DO UPDATE`,
  `RETURNING id`, `arg_max(...)`, `::JSON ->> 'key'`.
- ❌ `FOREIGN KEY … ON DELETE CASCADE` gives a parser error. Do cascades in Go,
  inside a transaction, and skip FK constraints entirely.
- ❌ `TIMESTAMPTZ - INTERVAL` is not available (no ICU in the bundled build). Rule
  and retention queries must **compute the cutoff time in Go and pass it as a
  parameter** (`created_at > ?` with a `time.Time` works). This also makes tests
  deterministic.

## 3. Data model

There are three migrations, one per milestone, so each can ship on its own.
JSON is stored as `VARCHAR` and (un)marshalled into typed Go structs per rule type.

### `002_devices.sql` (milestone 0)

```sql
-- +migrate Up
CREATE TABLE devices (
    mac        BLOB PRIMARY KEY,
    last_ip    BLOB,
    first_seen TIMESTAMPTZ NOT NULL,
    last_seen  TIMESTAMPTZ NOT NULL
);

-- Seed from history so the first run doesn't report every existing device as "new".
INSERT INTO devices
SELECT mac, arg_max(ip, created_at), min(created_at), max(created_at)
FROM (
    SELECT src_mac AS mac, src_ip AS ip, created_at FROM flows WHERE src_mac IS NOT NULL
    UNION ALL
    SELECT dst_mac, dst_ip, created_at FROM flows WHERE dst_mac IS NOT NULL
)
GROUP BY mac;

-- +migrate Down
DROP TABLE devices;
```

`dumpFlows` upserts every MAC it saw in the batch:

```sql
INSERT INTO devices VALUES (?, ?, ?, ?)
ON CONFLICT (mac) DO UPDATE SET last_ip = excluded.last_ip, last_seen = excluded.last_seen;
```

This table also enables device aliases later ("Living room TV").

### `003_notification_channels.sql` (milestone 1)

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

### `004_alerts.sql` (milestone 2)

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

### Retention (milestone 2)

Retention is 30 days by default, and `NETMON_ALERT_RETENTION_DAYS` overrides it.
A daily gocron job, at about 03:00 local time, computes `cutoff = now - retention`
in Go (see the ICU note in §2) and runs:

```sql
DELETE FROM notification_deliveries WHERE created_at < ?;
DELETE FROM alerts WHERE state = 'resolved' AND resolved_at < ?;  -- alerts still firing are never deleted
```

The same job also runs once at startup. `flows` is not affected; it still has no
retention (§11).

## 4. Notification channels (shoutrrr)

### 4.1 Library and Go version

- **Use `github.com/nicholas-fedor/shoutrrr`**, the maintained fork. Its latest
  release is v0.25.0 (2026-11-17), it has releases roughly every week, and it
  supports 33 URL schemes.
  The original `github.com/containrrr/shoutrrr` stopped at v0.8.0 (2023-08), so
  don't use it.
- What each fork line needs: v0.8.x needs Go 1.24, v0.9 to v0.19 need Go 1.25,
  and **v0.20 and later need Go 1.27**.
- **Plan: bump Go to 1.27 in milestone 0.** That means `go 1.27` in `go.mod` and
  `FROM golang:1.27` in the `Dockerfile`. Go 1.23 is out of support anyway.
  I checked that go-duckdb v1.8.3, the rest of the stack and shoutrrr v0.25.0 build
  and run together on go1.27.4.
- Fallback, only if the Go bump is blocked: pin fork v0.8.17 on Go 1.24. This
  loses about 15 months of fixes, so it's not recommended.

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
  The response returns this error to the form.
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
  {"title":"⚠️ Large download on tv.lan","message":"tv.lan (3c:22:fb:12:34:56, 192.168.1.40) downloaded 6.2 GB in the last 1h (threshold 5 GB).",
   "level":"Warning","event":"alert.firing","alert_id":"42","rule_id":"3","rule_name":"Big downloads",
   "rule_type":"traffic_threshold","severity":"warning","subject":"3c:22:fb:12:34:56","value":"6657199308",
   "mac":"3c:22:fb:12:34:56","ip":"192.168.1.40","hostname":"tv.lan","started_at":"2026-11-24T18:03:00Z",
   "url":"http://netmon.lan:2137/alerts/42","payload_version":"1"}
  ```

  Every value is a string. Field names must **not** collide with generic's config
  keys (`title`, `method`, `template`, `contenttype`, `titlekey`, `messagekey`,
  `disabletls`, `skiptlsverify`), because a matching param reconfigures the
  request instead of becoming a field.
- **Plain-text bodies.** netmon never sets `parsemode` or `usehtml`. If a user adds
  `parsemode=HTML` or `Markdown` to a Telegram URL, hostnames containing `_`, `<`
  and similar characters may break the formatting. Document this next to the URL field.
- **Use a one-shot `shoutrrr.SendContext` per message** instead of a long-lived
  router per channel. At home-network volumes the cost doesn't matter, and there's
  no router cache to invalidate when a channel is edited. Each attempt gets a 10 s
  `context.WithTimeout`.

### 4.4 Secrets in URLs

- **The API never returns `url`.** It returns `service` and `masked_url`, built with
  `format.MaskedURL(format.GetServiceConfig(svc))` after `Locate`. Tested output:
  - `telegram://REDACTED@telegram?chats=%40mychan`
  - `discord://REDACTED@123456`
  - `smtp://user:REDACTED@smtp.example.com:587/?…`
  - `ntfy://ntfy.sh/home-net` (the token is dropped)
- **Editing:** a `PUT` without `url` keeps the stored URL, so you can rename or
  toggle a channel without re-pasting secrets. To change any part of the URL, paste
  the whole URL again.
- **Errors stored in `notification_deliveries.error`:** shoutrrr prefixes errors
  with the service ID, not the URL. However, a wrapped `net/http` error can contain
  the request URL, and Telegram puts the bot token in the path. Before storing or
  logging an error, replace every userinfo value, path segment and query value of
  at least 8 characters from the channel URL with `REDACTED`. Never log channel URLs.
- URLs are stored in plain text in `flows.db`, the same trust boundary as the
  database file. Encrypting them is a later option (§8).

### 4.5 Dispatcher

- A buffered queue (256 jobs) and 4 workers, so a slow SMTP server doesn't block
  Telegram.
- Up to 3 attempts per message, with backoff of 2 s, 10 s and 30 s. shoutrrr's
  errors don't expose HTTP status codes, so every send error is retried. Validation
  errors from `Locate` are not retried; they fail immediately.
- Every send is written to `notification_deliveries`, with `attempts` and the
  redacted error. The UI shows this on the alert detail page.
- **Per-channel rate limit** (for example 20 messages per 5 minutes). Overflow
  becomes a single "N more alerts suppressed" message. This protects against a bad
  rule matching every device, and against Telegram's flood limits.
- **Grouping:** when one evaluation cycle produces more than 3 alerts for the same
  channel, send one combined message.
- The queue is in-memory. On shutdown, drain for up to 5 s, then drop what's left
  and log it. Alerts are persisted, so history isn't lost.
  `main.go` shutdown order: stop the collector, then the engine, then
  `dispatcher.Close(5s)`, then Fiber.

### 4.6 Later: generated channel forms

The fork exposes enough metadata to build a form for every service without
writing per-service code. `format.GetServiceConfigFormat(service)` returns each
field's `Name`, `Description`, `DefaultValue`, `Required`, `Sensitive` and `Keys`.
For Telegram, `Token` is required and sensitive, `Chats` is required, and
`ParseMode`, `Notification` and the rest are optional. Fill a config with
`format.SetConfigField` and build the URL with `config.GetURL()`.

That would replace "paste a URL" with a real form. It is planned for milestone 4;
the MVP form is the URL field plus examples (§7).

## 5. Alert rules

### 5.1 Rule types

Every rule's `params` includes `"scope": {"macs": [...]}`; an empty list means all
devices. Durations are stored in seconds.

| Type | Kind | Params | Subject | Fires when | Resolves when |
|---|---|---|---|---|---|
| `new_device` | event | `ignore_random_macs` (default **`false`**) | MAC | a device's `first_seen` is after the previous evaluation (and after the rule's `created_at`) | n/a (one-shot) |
| `traffic_threshold` | stateful | `direction` in/out/total, `threshold_bytes`, `window_s` (300…86400); optional `aggregate: "network"` | MAC, or `network` | sum over the sliding window > threshold | sum < 90 % of threshold (hysteresis against flapping) |
| `device_inactive` | stateful | `inactive_s`; **scope required** | MAC | `now - last_seen > inactive_s` | the device is seen again |
| `destination_port` | event | `ports` [], `proto` tcp/udp/any | `mac\|port/proto` | a flow in the window since the last evaluation matches | n/a |
| `destination_country` | event | `mode` deny/allow, `countries` [ISO codes] | `mac\|country` | a public destination's country is in the deny list (or missing from the allow list) | n/a |

Details:

- **Random MACs.** Phones use private Wi-Fi addresses, which have the
  locally-administered bit set (`mac[0] & 0x02 != 0`). By default netmon alerts on
  these too. The rule form shows the option with a hint: "Enable if phones that
  rotate their Wi-Fi address cause repeated alerts."
- **Traffic direction** follows the same `CASE WHEN src_mac IS NOT NULL …` logic as
  `GetClients`, so alert numbers match the UI exactly. Example evaluator query
  (the window start comes from Go, per §2):

  ```sql
  SELECT
      CASE WHEN src_mac IS NOT NULL THEN src_mac ELSE dst_mac END AS mac,
      sum(in_bytes)::UBIGINT  AS in_bytes,
      sum(out_bytes)::UBIGINT AS out_bytes
  FROM flows
  WHERE created_at > ?            -- now - window, computed in Go
  GROUP BY mac
  HAVING mac IS NOT NULL
  ```
  Direction, threshold and scope are then applied in Go.
- **Minimum window is 5 minutes.** Data arrives in 1-minute batches, so anything
  shorter is just noise.
- **`destination_country`** uses the shared GeoIP reader from milestone 0, looks up
  only distinct public `dst_ip` values in the batch, and caches the results.
- **Event-rule cooldown:** do not notify the same `(rule, subject)` again within
  `repeat_interval_s`. The default is 24 h for event rules and "never" for
  stateful rules.

### 5.2 Package `alerting`

```go
type Observation struct {
    Subject string
    Value   float64
    Labels  map[string]string // mac, ip, hostname, country, port… → message text and webhook fields
}

type Evaluator interface {
    Kind() Kind // Stateful | Event
    Evaluate(ctx context.Context, db *sql.DB, rule Rule, since, now time.Time) ([]Observation, error)
}

type Engine struct { /* db, dispatcher, evaluators map[string]Evaluator, clock, trigger chan time.Time */ }

func (e *Engine) Trigger(t time.Time)        // non-blocking; chan of size 1, so triggers coalesce
func (e *Engine) Run(ctx context.Context)    // the loop
```

Layout: `alerting/{engine.go, rules.go (types, params, validation), evaluators.go,
templates.go, retention.go}` and `storage/{devices.go, channels.go, rules.go,
alerts.go}`.

**One evaluation cycle** runs after every flush, about once a minute:

1. Load the enabled rules and their channel IDs.
2. For each rule, run its evaluator with a 10 s timeout and `recover`. On error,
   log it and continue with the next rule.
3. Reconcile the result with the open alerts for that rule:
   - **Stateful rules.**
     - Firing subject with no open alert: insert a `firing` alert and notify `alert.firing`.
     - Firing subject with an open alert: update `value` and `last_evaluated_at`.
       If `repeat_interval_s` has passed since `last_notified_at`, notify `alert.reminder`.
     - Open alert whose subject is no longer firing: set `resolved`, and notify
       `alert.resolved` if `notify_on_resolve` is set.
   - **Event rules.** For each observation not covered by the cooldown, insert an
     alert that is already `resolved` (`resolved_at = started_at`) and notify
     `alert.firing`.
4. Set `rule.last_evaluated_at = now`. The next run uses this as `since`.

Because open alerts live in the database, a restart doesn't re-send "firing"
messages. Editing a rule resolves its open alerts **silently**, and the next cycle
re-evaluates it with the new parameters. Deleting a rule removes its channel links
and resolves its open alerts, but keeps the history (`rule_name` is a snapshot)
until retention removes it.

**Wiring** (`main.go` and `collector`):

```go
dispatcher := notify.NewDispatcher(db)
engine     := alerting.NewEngine(db, dispatcher)
go engine.Run(ctx)

coll, err := collector.CollectTraffic(db, collector.Options{OnFlush: engine.Trigger})
```

`dumpFlows` already runs every minute even when there are no flows, so the
`device_inactive` rule sees time pass without needing a separate timer. On
non-Linux builds there is no collector, so no new data arrives and nothing is
evaluated, which is correct.

### 5.3 Message text

Each rule type has a default `text/template` for the title and body, enriched with
hostname, last IP and country from the `lookup` package. The emoji prefix comes
from severity (ℹ️ / ⚠️ / 🚨), and resolved messages use ✅:

- `New device: 3c:22:fb:12:34:56` / `First seen 18:03 as 192.168.1.40 (iphone.lan).`
- `Large download on tv.lan` / `tv.lan (…) downloaded 6.2 GB in the last 1h (threshold 5 GB).`
- `Resolved: Large download on tv.lan` / `Back under threshold after 47 min.`
- `nas.lan is inactive` / `No traffic since 17:20 (45 min).`

Bodies end with the deep link `NETMON_PUBLIC_URL/alerts/:id`, which defaults to
`http://<hostname>:2137`. Byte formatting reproduces the UI's `pretty-bytes` style
on the Go side. Custom templates per rule come in milestone 4.

## 6. HTTP API

New handlers go in `api/notifications.go` and `api/alerts.go`. New DTOs use
explicit `json:"snake_case"` tags, matching the existing `HostnamesBody`.
Validation errors return `400 {"errors": {"url": "telegram: invalid telegram token"}}`.

| Method & path | Purpose |
|---|---|
| `GET /api/notification-services` | A curated list of common services (Telegram, ntfy, Discord, email, Gotify, Pushover, Slack, Webhook) with an example URL and a docs link, plus every scheme from `router.SupportedSchemas()` |
| `GET /api/notification-channels` | list: `id, name, service, masked_url, enabled, used_by_rules` |
| `POST /api/notification-channels` | create `{name, url, enabled}`; the URL is validated with `Locate` |
| `GET/PUT/DELETE /api/notification-channels/:id` | read, update (no `url` = keep the stored one), delete (also unlinks it from rules) |
| `POST /api/notification-channels/:id/test` | send a test message through a saved channel |
| `POST /api/notification-channels/test` | send a test message to an **unsaved** URL from the form |
| `GET /api/alert-rule-types` | param specs per rule type (drives the rule form) |
| `GET /api/alert-rules` | list, with channel IDs and the count of firing alerts |
| `POST /api/alert-rules` | create |
| `GET/PUT/DELETE /api/alert-rules/:id` | read, update, delete |
| `PATCH /api/alert-rules/:id` | `{"enabled": false}` (toggle from the list) |
| `POST /api/alert-rules/preview` | **dry run** of a rule body against current data: returns the observations, sends nothing |
| `GET /api/alerts?state=&rule_id=&mac=&limit=&offset=` | firing alerts and history (last 30 days) |
| `GET /api/alerts/:id` | detail, including deliveries |
| `GET /api/devices` | MAC, last IP, hostname, first and last seen (for pickers) |

The SPA fallback (§2, item 4) is part of milestone 0.

## 7. Frontend

New routes. TanStack's file-based routing regenerates `routeTree.gen.ts` through
the Vite plugin.

```
client/src/routes/alerts/index.lazy.tsx            Alerts: "firing now" + history table
client/src/routes/alerts/$alertId.lazy.tsx         Detail + delivery log (target of message links)
client/src/routes/alerts/rules/index.lazy.tsx      Rules list with enable switch
client/src/routes/alerts/rules/new.lazy.tsx        Create (accepts ?type=&mac= to prefill)
client/src/routes/alerts/rules/$ruleId.lazy.tsx    Edit
client/src/routes/settings/notifications.lazy.tsx  Channels list + add/edit Sheet + "Send test"
```

- **Sidebar** (`__root.tsx`): add **Alerts** (lucide `Bell`, with a badge showing
  the firing count; poll `/api/alerts?state=firing` every 30 s) and **Settings**
  (lucide `Settings`).
- **Shared API layer:** `client/src/api/notifications.ts` and `alerts.ts` hold typed
  fetchers and React Query hooks, and mutations invalidate the matching queries.
- **shadcn components to add:** `dialog`, `alert-dialog`, `select`, `switch`,
  `checkbox`, `label`, `badge`, `dropdown-menu`, `sonner` (toasts), `textarea` and
  `form`, which needs `react-hook-form`, `zod` and `@hookform/resolvers`.
- **Channel form (MVP):**
  - Name.
  - A service picker from `/api/notification-services`. Choosing one fills the URL
    field's placeholder with its example and shows a "How to get these values"
    link to the shoutrrr docs page for that service.
  - A URL field (password-style input with a reveal toggle). When editing, it shows
    `masked_url` read-only and a "Replace URL" button.
  - **Send test**, which calls the unsaved-URL endpoint and shows the
    `Locate` or send error verbatim. This is the main feedback loop for getting a
    URL right.
- **Rule form:** name and type, then type-specific params: threshold with a MB/GB
  unit picker, window as 5 m/15 m/1 h/6 h/24 h, a device multi-select from
  `/api/devices` showing hostnames, port and country inputs, and
  `ignore_random_macs` off by default. Then channels (multi-select, with a link to
  create one if none exist), severity, repeat interval and notify-on-resolve. A
  **Preview** button lists the devices that would fire right now, with their values.
- **Entry points:** a "Create alert" button on `clients/$clientId` that goes to
  `/alerts/rules/new?mac=…&type=traffic_threshold`. The empty state on the rules
  page offers one-click presets: "Tell me about new devices" and "Tell me when a
  device uses more than 10 GB a day".

## 8. Security

**Auth is deferred** by decision and will be added later. Until then, anyone who
can reach port 2137 can read and change channels and rules. What this plan still
does, because it costs nothing and the UI needs it anyway:

- Channel URLs are write-only in the API (`masked_url` only), and errors are
  redacted before they are stored or logged (§4.4).
- **Remove `cors.New()`** in milestone 0. The UI is same-origin in production, and
  in development Vite already proxies `/api` (`client/vite.config.ts`), so CORS is
  never needed. Removing it stops other websites open in the browser from reading
  the API.

These are left for the auth work: an auth token or basic auth, encrypting channel
URLs at rest, and SSRF limits on `generic://` targets.

## 9. Testing

The repo has no tests yet. Add:

- **notify:**
  - Send to `generic://<httptest host>/hook?template=json&disabletls=yes`. This
    works offline, and I checked it. Assert the title, message, level and
    structured fields, and that no extra params reach non-generic services.
  - Test redaction of errors, and that `masked_url` never contains a secret.
  - Test the retry, rate-limit and grouping logic with a fake send function.
- **alerting evaluators:** in-memory DuckDB (`sql.Open("duckdb", "")`), run the
  embedded migrations, insert fixture flows with explicit `created_at`, and check
  the observations.
- **engine state machine:** fake clock plus fake dispatcher. Check: fires once; no
  duplicate on the next cycle; reminder after `repeat_interval_s`; resolve with
  hysteresis; no re-fire after a restart; event cooldown; one failing rule doesn't
  block the others.
- **retention:** with a fake clock, resolved alerts older than 30 days and old
  deliveries are deleted, and firing alerts are kept.
- **API:** `app.Test()` for CRUD, `Locate` validation errors, `masked_url`, and
  "a `PUT` without `url` keeps the stored one".
- **Manual end to end** on a Linux router: an ntfy.sh topic plus a Telegram bot, a
  `new_device` rule, and a phone joining the network.
- **CI (optional):** a GitHub Action on Go 1.27 running `go test ./...` and
  `npm run build`. The root package embeds `client/dist`, so build the client
  first, or limit `go vet` to the subpackages.

## 10. Milestones

| # | Milestone | Contents | Done when |
|---|---|---|---|
| 0 | **Groundwork** | **Go 1.27** (`go.mod`, `Dockerfile`); `lookup` package (GeoIP reader + DNS cache, configurable resolver); new storage functions return errors; `collector.Options{OnFlush}`; `002_devices.sql` + upsert in `dumpFlows`; SPA fallback; remove `cors.New()` | Builds on 1.27; existing UI unchanged; `devices` fills; refreshing `/clients/...` works |
| 1 | **Channels** | `003_notification_channels.sql`; shoutrrr dependency; `notify` dispatcher + delivery log + redaction; channel API + test endpoints; Settings → Notifications page (URL field + examples + Send test) | You can paste a Telegram or ntfy URL in the UI and receive a test message |
| 2 | **Rules MVP** | `004_alerts.sql`; engine; `new_device` + `traffic_threshold`; rules API; rules list + form; alerts list; **30-day retention job** | A phone joining the network produces a message within about a minute; a big download produces firing, then resolved; old history is deleted |
| 3 | **More rules + UX** | `device_inactive`, `destination_port`, `destination_country`; preview/dry run; alert detail + delivery log; sidebar badge; presets; "Create alert" from the client page | All five rule types are usable from the UI |
| 4 | **Polish** (optional) | Generated per-service channel forms (§4.6); custom templates per rule; quiet hours and "mute until"; daily digest mode; device aliases | n/a |
| n/a | **Auth** (separate work) | Login or token; secret encryption; SSRF limits | n/a |

Milestones 0 and 1 can each be merged on their own. Milestone 2 is the first one
users will notice.

## 11. Open questions

1. **`flows` retention:** the 30-day default applies to alert history and the
   delivery log. `flows` still grows forever. Should it get the same kind of
   retention job (configurable, possibly with a different default)?
