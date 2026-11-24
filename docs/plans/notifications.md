# Plan: notifications and alert rules

Status: proposal · 2026-11-24

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
| **Notification channel** | *Where* a message goes: a type plus its settings | Telegram bot → chat `12345`; ntfy topic `home-net` |
| **Alert rule** | *When* to send: a condition over the collected flows, a scope (which devices), and the channels to notify | "Any device downloads > 5 GB in 1 h → Telegram + ntfy" |

There is also one internal concept, the **alert**: one rule firing for one subject
(usually a device). An alert has a lifecycle (`firing → resolved`) and is kept as history.

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
                         notify.Dispatcher ──► queue ──► Notifier (telegram, ntfy, …)
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
5. **No auth, and `cors.New()` allows every origin.** Today that exposes only
   traffic stats. Once the database holds bot tokens and SMTP passwords, and the
   server can be told to POST to any URL (webhook test), any page open in the
   user's browser could read or abuse it. Fix this before shipping (§8).
6. **There are no tests yet.** This feature adds the first ones (§9).

DuckDB facts this design relies on. I checked each against go-duckdb v1.8.3, which bundles DuckDB v1.1.3:

- ✅ Sequences for IDs, `BLOB PRIMARY KEY`, `INSERT … ON CONFLICT (mac) DO UPDATE`,
  `RETURNING id`, `arg_max(...)`, `::JSON ->> 'key'`.
- ❌ `FOREIGN KEY … ON DELETE CASCADE` gives a parser error. Do cascades in Go,
  inside a transaction, and skip FK constraints entirely.
- ❌ `TIMESTAMPTZ - INTERVAL` is not available (no ICU in the bundled build). Rule
  queries must **compute the window start in Go and pass it as a parameter**
  (`created_at > ?` with a `time.Time` works). This also makes tests deterministic.

## 3. Data model

There are three migrations, one per milestone, so each can ship on its own.
JSON is stored as `VARCHAR` and (un)marshalled into typed Go structs per
channel or rule type.

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
    type       VARCHAR NOT NULL,           -- webhook | ntfy | telegram | discord | email
    config     VARCHAR NOT NULL,           -- JSON, type-specific (see §4.2)
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
    error      VARCHAR,
    created_at TIMESTAMPTZ NOT NULL DEFAULT current_timestamp
);

-- +migrate Down
DROP TABLE notification_deliveries; DROP SEQUENCE notification_deliveries_id_seq;
DROP TABLE alerts;                  DROP SEQUENCE alerts_id_seq;
DROP TABLE alert_rule_channels;
DROP TABLE alert_rules;             DROP SEQUENCE alert_rules_id_seq;
```

## 4. Notification channels

### 4.1 Package `notify`

```go
type Severity string // "info" | "warning" | "critical"

type Message struct {
    Title    string
    Body     string            // plain text; senders convert to HTML/Markdown as needed
    Severity Severity
    URL      string            // deep link back to netmon (NETMON_PUBLIC_URL + /alerts/:id)
    Event    string            // "alert.firing" | "alert.reminder" | "alert.resolved" | "test"
    Alert    *AlertPayload     // structured data for the webhook channel; nil for tests
}

type Notifier interface {
    Send(ctx context.Context, msg Message) error
}

type FieldSpec struct {
    Name, Label, Help, Placeholder string
    Kind     string   // text | password | number | bool | select | list | headers
    Required bool
    Secret   bool     // never returned by the API (§4.4)
    Options  []string // for select
    Default  any
}

type ChannelType struct {
    ID     string                                    // "telegram"
    Label  string                                    // "Telegram"
    Fields []FieldSpec                               // drives validation AND the UI form
    New    func(cfg json.RawMessage) (Notifier, error)
}

var Registry = map[string]ChannelType{ /* webhook, ntfy, telegram, discord, email */ }
```

Layout: `notify/{notify.go, registry.go, dispatcher.go, webhook.go, ntfy.go,
telegram.go, discord.go, email.go}`. Each sender gets an injectable base URL and
`*http.Client` so it can be tested.

### 4.2 Channel types for the MVP

| Type | Config fields (`*` = secret) | Transport |
|---|---|---|
| `webhook` | `url`, `method` (POST/PUT), `headers` (map), `signing_secret`* | JSON body (§4.6). With a secret, adds `X-Netmon-Signature: sha256=<hmac>` |
| `ntfy` | `server` (default `https://ntfy.sh`), `topic`, `token`* | `POST {server}/{topic}` with `Title`, `Priority`, `Tags`, `Click` headers |
| `telegram` | `bot_token`*, `chat_id` | `POST https://api.telegram.org/bot{token}/sendMessage`, `parse_mode=HTML` |
| `discord` | `webhook_url`* | `POST` JSON with one embed, colour from severity |
| `email` | `host`, `port`, `security` (starttls/tls/none), `username`, `password`*, `from`, `to` (list) | SMTP, via `net/smtp` or `github.com/wneessen/go-mail` (simpler TLS modes) |

Later: Slack (incoming webhook, nearly the same as Discord), Gotify, Pushover,
Matrix. There could also be a catch-all "URL" type backed by shoutrrr; check
which fork is maintained before adopting it.

**Why write the senders ourselves instead of using shoutrrr for everything:**
each sender is about 50–80 lines over `net/http`. Typed fields give a real form
in the UI rather than a URL-syntax puzzle, and a failed test send can show the
exact error from Telegram or Discord.

Severity mapping:

| Severity | ntfy priority | Telegram/email prefix | Discord colour |
|---|---|---|---|
| info | 3 (default) | ℹ️ | blue |
| warning | 4 (high) | ⚠️ | amber |
| critical | 5 (urgent) | 🚨 | red |

### 4.3 Schema-driven forms

`GET /api/notification-channel-types` returns the registry (`ID`, `Label`,
`Fields`). The UI renders the form generically from this, and the backend
validates against the same spec. Adding a channel type later then needs Go code
only.

### 4.4 Secrets

- API responses **never** include fields with `Secret: true`. Instead they return
  `"secrets_set": ["bot_token"]`. On `PUT`, a missing or empty secret field means
  "keep the stored value".
- Secrets are stored in plain text in `flows.db`, which is the same trust boundary
  as the database file. Document this. Optional later step: encrypt them with a key
  from `NETMON_SECRET_KEY`.
- Never log channel config. Log send errors only after redacting tokens
  (the Telegram token is part of the URL path).

### 4.5 Dispatcher

- A buffered queue (256 jobs) with a small worker pool (4 workers). Each send has
  a 10 s timeout and up to 3 attempts, with backoff of 2 s, 10 s and 30 s. Don't
  retry 4xx responses other than 429; for 429, honour `Retry-After`.
- Every attempt chain is written to `notification_deliveries`, which the UI shows
  on the alert detail page.
- **Per-channel rate limit** (for example 20 messages per 5 minutes). Overflow
  becomes a single "N more alerts suppressed" message. This protects against a bad
  rule matching every device, and against Telegram's flood limits.
- **Grouping:** when one evaluation cycle produces more than 3 alerts for the same
  channel, send one combined message instead of many.
- The queue is in-memory. On shutdown, drain for up to 5 s, then drop what's left
  and log it. That is acceptable for a home tool. The alerts themselves are
  persisted, so nothing is lost from history.
- `main.go` shutdown order: stop the collector, then the engine, then
  `dispatcher.Close(5s)`, then Fiber.

### 4.6 Webhook payload (versioned)

```json
{
  "version": 1,
  "event": "alert.firing",
  "alert": {
    "id": 42,
    "rule_id": 3,
    "rule_name": "Big downloads",
    "rule_type": "traffic_threshold",
    "severity": "warning",
    "subject": "3c:22:fb:12:34:56",
    "value": 6657199308,
    "title": "Large download on tv.lan",
    "body": "tv.lan (3c:22:fb:12:34:56, 192.168.1.40) downloaded 6.2 GB in the last 1h (threshold 5 GB).",
    "started_at": "2026-11-24T18:03:00Z",
    "resolved_at": null,
    "labels": { "mac": "3c:22:fb:12:34:56", "ip": "192.168.1.40", "hostname": "tv.lan" }
  },
  "url": "http://netmon.lan:2137/alerts/42"
}
```

## 5. Alert rules

### 5.1 Rule types

Every rule's `params` includes `"scope": {"macs": [...]}`; an empty list means all
devices. Durations are stored in seconds.

| Type | Kind | Params | Subject | Fires when | Resolves when |
|---|---|---|---|---|---|
| `new_device` | event | `ignore_random_macs` (default `true`) | MAC | a device's `first_seen` is after the previous evaluation (and after the rule's `created_at`) | n/a (one-shot) |
| `traffic_threshold` | stateful | `direction` in/out/total, `threshold_bytes`, `window_s` (300…86400); optional `aggregate: "network"` | MAC, or `network` | sum over the sliding window > threshold | sum < 90 % of threshold (hysteresis against flapping) |
| `device_inactive` | stateful | `inactive_s`; **scope required** | MAC | `now - last_seen > inactive_s` | the device is seen again |
| `destination_port` | event | `ports` [], `proto` tcp/udp/any | `mac\|port/proto` | a flow in the window since the last evaluation matches | n/a |
| `destination_country` | event | `mode` deny/allow, `countries` [ISO codes] | `mac\|country` | a public destination's country is in the deny list (or missing from the allow list) | n/a |

Details:

- **Random MACs.** Phones use private Wi-Fi addresses, which have the
  locally-administered bit set (`mac[0] & 0x02 != 0`). Without the
  `ignore_random_macs` filter, `new_device` would be noisy.
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
    Labels  map[string]string // mac, ip, hostname, country, port… → message templates and webhook
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
templates.go}` and `storage/{devices.go, channels.go, rules.go, alerts.go}`.

**One evaluation cycle** runs after every flush, about once a minute:

1. Load the enabled rules and their channel IDs.
2. For each rule, run its evaluator with a 10 s timeout and `recover`. On error,
   log it and continue with the next rule. Repeated failures can surface in the UI later.
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
and resolves its open alerts, but keeps the history (`rule_name` is a snapshot).

**Wiring** (`main.go` and `collector`):

```go
dispatcher := notify.NewDispatcher(db, notify.Registry)
engine     := alerting.NewEngine(db, dispatcher)
go engine.Run(ctx)

coll, err := collector.CollectTraffic(db, collector.Options{OnFlush: engine.Trigger})
```

`dumpFlows` already runs every minute even when there are no flows, so the
`device_inactive` rule sees time pass without needing a separate timer. On
non-Linux builds there is no collector, so no new data arrives and nothing is
evaluated, which is correct.

### 5.3 Message templates

Each rule type has a default `text/template` for the title and body, enriched with
hostname, last IP and country from the `lookup` package:

- `🆕 New device: 3c:22:fb:12:34:56` / `First seen 18:03 as 192.168.1.40 (iphone.lan).`
- `📈 Large download on tv.lan` / `tv.lan (…) downloaded 6.2 GB in the last 1h (threshold 5 GB).`
- `✅ Resolved: Large download on tv.lan` / `Back under threshold after 47 min.`
- `💤 nas.lan is inactive` / `No traffic since 17:20 (45 min).`

Byte formatting reproduces the UI's `pretty-bytes` style on the Go side. Custom
templates per rule come in milestone 4.

## 6. HTTP API

New handlers go in `api/notifications.go` and `api/alerts.go`. New DTOs use
explicit `json:"snake_case"` tags, matching the existing `HostnamesBody` and the
config JSON. Validation errors return `400 {"errors": {"chat_id": "required"}}`.

| Method & path | Purpose |
|---|---|
| `GET /api/notification-channel-types` | registry with field specs (drives the form) |
| `GET /api/notification-channels` | list (secrets masked, `used_by_rules` count) |
| `POST /api/notification-channels` | create (validated against the field spec) |
| `GET/PUT/DELETE /api/notification-channels/:id` | read, update (empty secret = keep), delete (also unlinks it from rules) |
| `POST /api/notification-channels/:id/test` | send a test message through a saved channel |
| `POST /api/notification-channels/test` | send a test message from an **unsaved** form config |
| `GET /api/alert-rule-types` | param specs per rule type (same idea as channel types) |
| `GET /api/alert-rules` | list, with channel IDs and the count of firing alerts |
| `POST /api/alert-rules` | create |
| `GET/PUT/DELETE /api/alert-rules/:id` | read, update, delete |
| `PATCH /api/alert-rules/:id` | `{"enabled": false}` (toggle from the list) |
| `POST /api/alert-rules/preview` | **dry run** of a rule body against current data: returns the observations, sends nothing |
| `GET /api/alerts?state=&rule_id=&mac=&limit=&offset=` | firing alerts and history |
| `GET /api/alerts/:id` | detail, including deliveries |
| `GET /api/devices` | MAC, last IP, hostname, first and last seen (for pickers) |

Also in this milestone: the SPA fallback (§2, item 4), and `NETMON_PUBLIC_URL` for
links in messages, defaulting to `http://<hostname>:2137`.

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
  fetchers and React Query hooks (`useChannels`, `useCreateChannel`, …), and
  mutations invalidate the matching queries. Today's routes fetch inline; this
  feature has enough mutations to justify a shared layer.
- **shadcn components to add:** `dialog`, `alert-dialog` (delete confirmation),
  `select`, `switch`, `checkbox`, `label`, `badge`, `dropdown-menu`, `sonner`
  (toasts) and `form`, which needs `react-hook-form`, `zod` and `@hookform/resolvers`.
- **Channel form:** a type picker, then fields rendered from
  `/api/notification-channel-types`. Secrets show as "•••• set · Change". "Send
  test" calls the unsaved-config endpoint and shows the provider's error verbatim.
- **Rule form:** name and type, then type-specific params: threshold with a MB/GB
  unit picker, window as 5 m/15 m/1 h/6 h/24 h, a device multi-select from
  `/api/devices` showing hostnames, port and country inputs. Then channels
  (multi-select, with a link to create one if none exist), severity, repeat
  interval and notify-on-resolve. A **Preview** button lists the devices that
  would fire right now, with their values, which makes thresholds easy to tune.
- **Entry points:** a "Create alert" button on `clients/$clientId` that goes to
  `/alerts/rules/new?mac=…&type=traffic_threshold`. The empty state on the rules
  page offers one-click presets: "Tell me about new devices" and "Tell me when a
  device uses more than 10 GB a day".

## 8. Security (must ship with milestone 1)

- **CORS:** the UI is served from the same origin, so replace `cors.New()` with a
  config that allows only the Vite dev origin, enabled only when `NETMON_DEV=1`.
- **Optional auth:** when `NETMON_AUTH_TOKEN` is set, `/api/*` requires a
  `Bearer` token or basic auth. Strongly recommended once channels hold secrets.
- Secrets are write-only in the API (§4.4).
- Outbound calls (webhook URL, test endpoint) get a 10 s timeout, read at most
  64 KB of the response, and echo back only a short error excerpt.
- Mutating endpoints accept only `application/json`, which blocks simple
  form-POST CSRF.

## 9. Testing

The repo has no tests yet. Add:

- **notify:** each sender tested against `httptest.Server` (base URLs are
  injectable), checking method, path, headers and payload. Email: unit-test message
  building, plus a minimal fake SMTP listener for one happy-path test.
- **alerting evaluators:** in-memory DuckDB (`sql.Open("duckdb", "")`), run the
  embedded migrations, insert fixture flows with explicit `created_at`, and check
  the observations. Because the window start is a parameter, this needs no clock
  mocking in SQL.
- **engine state machine:** fake clock plus fake notifier. Check: fires once; no
  duplicate on the next cycle; reminder after `repeat_interval_s`; resolve with
  hysteresis; no re-fire after a restart; event cooldown; one failing rule doesn't
  block the others.
- **API:** `app.Test()` for CRUD, validation errors, secret masking and "empty
  secret keeps the old value".
- **Manual end to end** on a Linux router: an ntfy.sh topic, a `new_device` rule,
  and a phone joining with private MAC off and then on.
- **CI (optional):** a GitHub Action running `go test ./...` and `npm run build`.
  The root package embeds `client/dist`, so build the client first, or limit
  `go vet` to the subpackages.

## 10. Milestones

| # | Milestone | Contents | Done when |
|---|---|---|---|
| 0 | **Groundwork** | `lookup` package (GeoIP reader + DNS cache, configurable resolver); new storage functions return errors; `collector.Options{OnFlush}`; `002_devices.sql` + upsert in `dumpFlows`; SPA fallback; CORS tightened | Existing UI unchanged; `devices` fills; refreshing `/clients/...` works |
| 1 | **Channels** | `003_notification_channels.sql`; `notify` package with webhook, ntfy, telegram, discord, email; dispatcher + delivery log; channel API + test endpoints; Settings → Notifications page; secret masking; optional auth token | You can add a Telegram or ntfy channel in the UI and receive a test message |
| 2 | **Rules MVP** | `004_alerts.sql`; engine; `new_device` + `traffic_threshold`; rules API; rules list + form; alerts list | A phone joining the network produces a message within about a minute; a big download produces firing, then resolved |
| 3 | **More rules + UX** | `device_inactive`, `destination_port`, `destination_country`; preview/dry run; alert detail + delivery log; sidebar badge; presets; "Create alert" from the client page | All five rule types are usable from the UI |
| 4 | **Polish** (optional) | Custom templates per rule; quiet hours and "mute until"; daily digest mode; retention for `alerts`/`notification_deliveries` (and `flows`, which has none today); device aliases; secret encryption; Slack/Gotify/Pushover | n/a |

Milestones 0 and 1 can each be merged on their own. Milestone 2 is the first one
users will notice.

## 11. Open questions

1. **Channel priority:** is ntfy + Telegram + Discord + webhook + email the right
   first set, or is one or two of them enough to start?
2. **Auth:** is netmon only ever reachable on the LAN? Is an optional
   `NETMON_AUTH_TOKEN` acceptable, or should it be required once channels exist?
3. **`new_device` default:** ignore randomized (private) MACs by default?
4. **Retention:** how long to keep alert history (proposal: 90 days)? Should
   `flows` retention be handled in the same pass?
