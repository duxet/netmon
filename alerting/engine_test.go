package alerting

import (
	"context"
	"database/sql"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/duxet/netmon/apps"
	"github.com/duxet/netmon/notify"
	"github.com/duxet/netmon/storage"
	migrate "github.com/rubenv/sql-migrate"
)

const (
	tvMAC     = "3c:22:fb:12:34:56"
	laptopMAC = "a4:83:e7:1c:2f:90"
	// A locally administered address, as phones use for private Wi-Fi.
	phoneMAC = "da:a1:19:5e:00:01"

	googleIP   = "142.250.186.78"
	telegramIP = "149.154.167.50"
)

// start is when the tests begin.
var start = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrate.Exec(db, "sqlite3", &migrate.FileMigrationSource{Dir: "../migrations"}, migrate.Up); err != nil {
		t.Fatal(err)
	}
	return db
}

// flow records traffic from a device (by MAC and IP) to a remote address:
// download is what the device received.
func flow(t *testing.T, db *sql.DB, mac, ip, remote string, download, upload uint64, at time.Time) {
	t.Helper()
	hw, err := net.ParseMAC(mac)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO flows VALUES (?, NULL, ?, ?, 6, 443, ?, 1, ?, 1, ?)`,
		[]byte(hw), netip.MustParseAddr(ip).AsSlice(), netip.MustParseAddr(remote).AsSlice(), download, upload, at)
	if err != nil {
		t.Fatal(err)
	}
}

type fakeDispatcher struct {
	mu   sync.Mutex
	sent []notify.Notification
}

func (f *fakeDispatcher) Dispatch(ns []notify.Notification) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, ns...)
}

// take returns the notifications dispatched since the last call.
func (f *fakeDispatcher) take() []notify.Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	sent := f.sent
	f.sent = nil
	return sent
}

type testEngine struct {
	*Engine
	now        time.Time
	dispatcher *fakeDispatcher
}

func newTestEngine(t *testing.T, db *sql.DB) *testEngine {
	t.Helper()
	return newTestEngineWith(t, db, nil)
}

func newTestEngineWith(t *testing.T, db *sql.DB, countries CountryLookup) *testEngine {
	t.Helper()
	te := &testEngine{now: start, dispatcher: &fakeDispatcher{}}
	te.Engine = NewEngine(db, te.dispatcher, Options{
		Classifier: apps.NewClassifier(apps.Known, ""),
		Countries:  countries,
		PublicURL:  "http://netmon.lan:2137",
		Now:        func() time.Time { return te.now },
	})
	return te
}

// at evaluates the rules at a time after start and returns the
// notifications.
func (te *testEngine) at(offset time.Duration) []notify.Notification {
	te.now = start.Add(offset)
	te.Evaluate(context.Background())
	return te.dispatcher.take()
}

func addRule(t *testing.T, db *sql.DB, r storage.RuleRecord, createdAt time.Time) storage.RuleRecord {
	t.Helper()
	if r.Severity == "" {
		r.Severity = "warning"
	}
	r.Enabled = true
	created, err := storage.CreateRule(db, r, createdAt)
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func trafficRule(t *testing.T, db *sql.DB, params string, repeat *int64) storage.RuleRecord {
	t.Helper()
	normalized, err := ValidateParams(TrafficThreshold, []byte(params))
	if err != nil {
		t.Fatal(err)
	}
	return addRule(t, db, storage.RuleRecord{
		Name: "Big downloads", Type: TrafficThreshold, Params: normalized,
		RepeatIntervalSeconds: repeat, NotifyOnResolve: true, ChannelIDs: []int64{7},
	}, start)
}

func titles(ns []notify.Notification) []string {
	var titles []string
	for _, n := range ns {
		titles = append(titles, n.Kind+": "+n.Message.Title)
	}
	return titles
}

func TestTrafficThreshold(t *testing.T) {
	db := openTestDB(t)
	tv, _ := net.ParseMAC(tvMAC)
	if err := storage.SetDeviceName(db, tv, "Living room TV"); err != nil {
		t.Fatal(err)
	}
	hour := int64(3600)
	rule := trafficRule(t, db, `{"direction": "download", "threshold_bytes": 5000, "window_s": 3600}`, &hour)
	engine := newTestEngine(t, db)

	flow(t, db, tvMAC, "192.168.1.40", googleIP, 6000, 100, start.Add(-2*time.Minute))
	flow(t, db, laptopMAC, "192.168.1.23", "1.1.1.1", 3000, 100, start.Add(-2*time.Minute))

	// The TV is over the threshold; the laptop isn't.
	ns := engine.at(0)
	if len(ns) != 1 || ns[0].Kind != "firing" || ns[0].Message.Title != "Large download on Living room TV" {
		t.Fatalf("notifications = %v, want the TV firing", titles(ns))
	}
	n := ns[0]
	if !strings.Contains(n.Message.Body, "downloaded 6 kB in the last hour, over the 5 kB of the rule “Big downloads”") ||
		!strings.Contains(n.Message.Body, "http://netmon.lan:2137/alerts/"+n.Message.Fields["alert_id"]) {
		t.Errorf("body = %q", n.Message.Body)
	}
	if n.Message.Severity != notify.Warning || len(n.ChannelIDs) != 1 || n.ChannelIDs[0] != 7 {
		t.Errorf("notification = %+v, want a warning for channel 7", n)
	}
	fields := n.Message.Fields
	if fields["event"] != "alert.firing" || fields["mac"] != tvMAC || fields["ip"] != "192.168.1.40" ||
		fields["device_name"] != "Living room TV" || fields["value"] != "6000" || fields["rule_type"] != TrafficThreshold ||
		fields["url"] != "http://netmon.lan:2137/alerts/"+fields["alert_id"] {
		t.Errorf("fields = %v", fields)
	}

	firing, _ := storage.FiringAlerts(db, rule.ID)
	alert, ok := firing[tvMAC]
	if !ok || len(firing) != 1 || fields["alert_id"] != strconv.FormatInt(alert.ID, 10) || *alert.Value != 6000 {
		t.Fatalf("firing alerts = %+v, want the TV's", firing)
	}

	// Still over a minute later: nothing new to say.
	if ns := engine.at(time.Minute); len(ns) != 0 {
		t.Errorf("a minute later = %v, want nothing", titles(ns))
	}

	// An hour later, more traffic keeps it firing, and the repeat interval
	// has passed.
	flow(t, db, tvMAC, "192.168.1.40", googleIP, 6000, 100, start.Add(50*time.Minute))
	ns = engine.at(61 * time.Minute)
	if len(ns) != 1 || ns[0].Kind != "reminder" || !strings.HasPrefix(ns[0].Message.Title, "Still: ") ||
		!strings.Contains(ns[0].Message.Body, "since") {
		t.Errorf("after an hour = %v, want a reminder", titles(ns))
	}

	// Just under the threshold, but not under 90 % of it: still firing.
	flow(t, db, tvMAC, "192.168.1.40", googleIP, 4600, 100, start.Add(100*time.Minute))
	if ns := engine.at(111 * time.Minute); len(ns) != 0 {
		t.Errorf("at 92 %% of the threshold = %v, want nothing", titles(ns))
	}
	if firing, _ := storage.FiringAlerts(db, rule.ID); len(firing) != 1 || *firing[tvMAC].Value != 4600 {
		t.Errorf("firing at 92 %% = %+v, want the TV's at 4600", firing)
	}

	// Once the traffic is out of the window, it resolves.
	ns = engine.at(170 * time.Minute)
	if len(ns) != 1 || ns[0].Kind != "resolved" || ns[0].Message.Title != "Resolved: Large download on Living room TV" ||
		!strings.Contains(ns[0].Message.Body, "back under 5 kB in the last hour, after 2 hours 50 minutes") {
		t.Fatalf("after the traffic stopped = %v %+v, want it resolved", titles(ns), ns)
	}
	if ns[0].Message.Fields["resolved_at"] == "" || ns[0].Message.Fields["state"] != storage.AlertResolved {
		t.Errorf("resolved fields = %v", ns[0].Message.Fields)
	}
	if firing, _ := storage.FiringAlerts(db, rule.ID); len(firing) != 0 {
		t.Errorf("firing after resolving = %+v", firing)
	}

	// A new burst is a new alert.
	flow(t, db, tvMAC, "192.168.1.40", googleIP, 9000, 100, start.Add(175*time.Minute))
	if ns := engine.at(176 * time.Minute); len(ns) != 1 || ns[0].Kind != "firing" {
		t.Errorf("after a new burst = %v, want a new alert", titles(ns))
	}
	if alerts, total, _ := storage.ListAlerts(db, storage.AlertsFilter{RuleID: rule.ID}); total != 2 || alerts[0].State != storage.AlertFiring {
		t.Errorf("alerts = %+v, want the old one resolved and a new one firing", alerts)
	}
}

func TestTrafficWithAppAndDevices(t *testing.T) {
	db := openTestDB(t)
	flow(t, db, tvMAC, "192.168.1.40", telegramIP, 6000, 100, start.Add(-2*time.Minute))
	flow(t, db, tvMAC, "192.168.1.40", googleIP, 6000, 100, start.Add(-2*time.Minute))
	flow(t, db, laptopMAC, "192.168.1.23", telegramIP, 100, 7000, start.Add(-2*time.Minute))

	// Only Telegram's traffic counts: 6 kB down for the TV, 7.1 kB in all
	// for the laptop.
	trafficRule(t, db, `{"direction": "total", "threshold_bytes": 5000, "window_s": 900, "app": "Telegram"}`, nil)
	// Limited to the laptop, which downloaded little.
	trafficRule(t, db, `{"direction": "download", "threshold_bytes": 5000, "window_s": 900, "macs": ["A4-83-E7-1C-2F-90"]}`, nil)

	ns := newTestEngine(t, db).at(0)
	if len(ns) != 2 {
		t.Fatalf("notifications = %v, want both devices' Telegram traffic and nothing for the laptop's downloads", titles(ns))
	}
	want := map[string]string{tvMAC: "6100", laptopMAC: "7100"}
	for _, n := range ns {
		mac := n.Message.Fields["mac"]
		if n.Message.Fields["value"] != want[mac] || !strings.HasPrefix(n.Message.Title, "High Telegram traffic on") ||
			!strings.Contains(n.Message.Body, "with Telegram in the last 15 minutes") {
			t.Errorf("%s: value %s, %q / %q; want %s of Telegram traffic",
				mac, n.Message.Fields["value"], n.Message.Title, n.Message.Body, want[mac])
		}
	}
}

func TestNewDevice(t *testing.T) {
	db := openTestDB(t)
	engine := newTestEngine(t, db)

	// The laptop was there before the rules.
	flow(t, db, laptopMAC, "192.168.1.23", "1.1.1.1", 100, 100, start.Add(-time.Minute))
	all := addRule(t, db, storage.RuleRecord{Name: "New devices", Type: NewDevice, Params: `{}`, ChannelIDs: []int64{1}}, start)
	addRule(t, db, storage.RuleRecord{Name: "New devices (no random)", Type: NewDevice, Params: `{"ignore_random_macs": true}`}, start)

	flow(t, db, tvMAC, "192.168.1.40", googleIP, 100, 100, start.Add(time.Minute))
	flow(t, db, phoneMAC, "192.168.1.77", googleIP, 100, 100, start.Add(time.Minute))

	ns := engine.at(2 * time.Minute)
	if len(ns) != 3 {
		t.Fatalf("notifications = %v, want the TV and phone for one rule, the TV for the other", titles(ns))
	}
	for _, n := range ns {
		if !strings.HasPrefix(n.Message.Title, "New device: ") || n.Message.Fields["first_seen"] == "" ||
			!strings.Contains(n.Message.Body, "joined the network, first seen at") {
			t.Errorf("notification = %+v", n.Message)
		}
	}

	// Event alerts are over at once, and not raised again.
	alerts, total, _ := storage.ListAlerts(db, storage.AlertsFilter{RuleID: all.ID})
	if total != 2 || alerts[0].State != storage.AlertResolved {
		t.Errorf("alerts = %+v, want two resolved", alerts)
	}
	if ns := engine.at(3 * time.Minute); len(ns) != 0 {
		t.Errorf("a minute later = %v, want nothing", titles(ns))
	}
	// Not after a restart either.
	if ns := newTestEngine(t, db).at(4 * time.Minute); len(ns) != 0 {
		t.Errorf("after a restart = %v, want nothing", titles(ns))
	}
}

// A rule that fails doesn't keep the others from running, and isn't marked
// as evaluated.
func TestBrokenRule(t *testing.T) {
	db := openTestDB(t)
	broken := addRule(t, db, storage.RuleRecord{Name: "Broken", Type: TrafficThreshold, Params: `{`}, start)
	addRule(t, db, storage.RuleRecord{Name: "Unknown", Type: "nope", Params: `{}`}, start)
	disabled := addRule(t, db, storage.RuleRecord{Name: "Off", Type: NewDevice, Params: `{}`}, start)
	disabled.Enabled = false
	if _, err := storage.UpdateRule(db, disabled, start, false); err != nil {
		t.Fatal(err)
	}
	working := addRule(t, db, storage.RuleRecord{Name: "New devices", Type: NewDevice, Params: `{}`}, start)

	flow(t, db, tvMAC, "192.168.1.40", googleIP, 100, 100, start.Add(time.Minute))
	if ns := newTestEngine(t, db).at(2 * time.Minute); len(ns) != 1 {
		t.Errorf("notifications = %v, want the working rule's", titles(ns))
	}

	if r, _ := storage.GetRule(db, broken.ID); r.LastEvaluatedAt != nil {
		t.Error("the broken rule was marked as evaluated")
	}
	if r, _ := storage.GetRule(db, working.ID); r.LastEvaluatedAt == nil {
		t.Error("the working rule wasn't marked as evaluated")
	}
}

// The public URL can change while the engine runs, from the configuration page.
func TestSetPublicURL(t *testing.T) {
	te := newTestEngine(t, openTestDB(t))
	alert := storage.AlertRecord{ID: 7}

	if got := te.link(alert); got != "http://netmon.lan:2137/alerts/7" {
		t.Errorf("link = %q, want the configured URL's", got)
	}
	te.SetPublicURL("https://netmon.example.com")
	if got := te.link(alert); got != "https://netmon.example.com/alerts/7" {
		t.Errorf("link after SetPublicURL = %q", got)
	}
	te.SetPublicURL("")
	if got := te.link(alert); got != "" {
		t.Errorf("link without a URL = %q, want none", got)
	}
}
