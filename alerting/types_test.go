package alerting

import (
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/duxet/netmon/storage"
)

// connect records a device's connection to a port of a remote address.
func connect(t *testing.T, te *testEngine, mac, ip, remote string, port uint16, proto uint8, bytes uint64, at time.Time) {
	t.Helper()
	hw, err := net.ParseMAC(mac)
	if err != nil {
		t.Fatal(err)
	}
	_, err = te.db.Exec(`INSERT INTO flows VALUES (?, NULL, ?, ?, ?, ?, ?, 1, ?, 1, ?)`,
		[]byte(hw), netip.MustParseAddr(ip).AsSlice(), netip.MustParseAddr(remote).AsSlice(), proto, port, bytes/2, bytes-bytes/2, at)
	if err != nil {
		t.Fatal(err)
	}
}

func rule(t *testing.T, te *testEngine, name, ruleType, params string, notifyOnResolve bool) storage.RuleRecord {
	t.Helper()
	normalized, err := ValidateParams(ruleType, []byte(params))
	if err != nil {
		t.Fatal(err)
	}
	return addRule(t, te.db, storage.RuleRecord{Name: name, Type: ruleType, Params: normalized, NotifyOnResolve: notifyOnResolve}, start)
}

func TestDeviceInactive(t *testing.T) {
	db := openTestDB(t)
	te := newTestEngine(t, db)
	// The TV was last seen before netmon started; the other device never.
	flow(t, db, tvMAC, "192.168.1.40", googleIP, 100, 100, start.Add(-2*time.Minute))
	rule(t, te, "TV", DeviceInactive, `{"inactive_s": 1800, "macs": ["`+tvMAC+`", "02:00:00:00:00:99"]}`, true)

	// netmon only knows about the time it ran: quiet since it started.
	if ns := te.at(0); len(ns) != 0 {
		t.Errorf("at the start = %v, want nothing", titles(ns))
	}
	ns := te.at(31 * time.Minute)
	if len(ns) != 1 || ns[0].Message.Title != "No traffic from Apple device" ||
		!strings.Contains(ns[0].Message.Body, "no traffic since netmon started at") ||
		!strings.Contains(ns[0].Message.Body, "for 31 minutes") || !strings.Contains(ns[0].Message.Body, "every 30 minutes") {
		t.Fatalf("after 31 quiet minutes = %v %+v", titles(ns), ns)
	}
	if ns := te.at(32 * time.Minute); len(ns) != 0 {
		t.Errorf("a minute later = %v, want nothing", titles(ns))
	}

	// Traffic again resolves it.
	flow(t, db, tvMAC, "192.168.1.40", googleIP, 100, 100, start.Add(40*time.Minute))
	ns = te.at(41 * time.Minute)
	if len(ns) != 1 || ns[0].Kind != "resolved" || ns[0].Message.Title != "Traffic again from Apple device" ||
		!strings.Contains(ns[0].Message.Body, "after 32 minutes without") {
		t.Fatalf("after traffic again = %v %+v", titles(ns), ns)
	}

	// Quiet after traffic seen while netmon ran says since when.
	ns = te.at(75 * time.Minute)
	if len(ns) != 1 || !strings.Contains(ns[0].Message.Body, "since "+clock(start.Add(40*time.Minute))+", for 35 minutes") {
		t.Errorf("quiet again = %v %+v", titles(ns), ns)
	}
}

func TestTrafficOfTheNetwork(t *testing.T) {
	db := openTestDB(t)
	te := newTestEngine(t, db)
	flow(t, db, tvMAC, "192.168.1.40", googleIP, 6000, 100, start.Add(-2*time.Minute))
	flow(t, db, laptopMAC, "192.168.1.23", "1.1.1.1", 5000, 100, start.Add(-2*time.Minute))
	// Traffic on the local network doesn't count.
	flow(t, db, laptopMAC, "192.168.1.23", "192.168.1.10", 9000, 100, start.Add(-2*time.Minute))
	rule(t, te, "Data cap", TrafficThreshold, `{"direction": "download", "threshold_bytes": 10000, "window_s": 3600, "network": true}`, true)

	ns := te.at(0)
	if len(ns) != 1 || ns[0].Message.Title != "Large download on the network" ||
		!strings.HasPrefix(ns[0].Message.Body, "The network downloaded 11 kB from the internet in the last hour") ||
		ns[0].Message.Fields["subject"] != networkSubject || ns[0].Message.Fields["scope"] != "network" {
		t.Fatalf("notifications = %v %+v", titles(ns), ns)
	}

	ns = te.at(2 * time.Hour)
	if len(ns) != 1 || !strings.HasPrefix(ns[0].Message.Body, "The network is back under 10 kB") {
		t.Errorf("after the traffic stopped = %v %+v", titles(ns), ns)
	}
}

func TestDestinationPort(t *testing.T) {
	db := openTestDB(t)
	te := newTestEngine(t, db)
	// Before the rule existed: not its business.
	connect(t, te, tvMAC, "192.168.1.40", "203.0.113.9", 23, 6, 100, start.Add(-time.Minute))
	r := rule(t, te, "Telnet", DestinationPort, `{"ports": [2323, 23], "protocol": "tcp"}`, false)

	connect(t, te, tvMAC, "192.168.1.40", "203.0.113.5", 23, 6, 3000, start.Add(time.Minute))
	connect(t, te, tvMAC, "192.168.1.40", "198.51.100.7", 23, 6, 1000, start.Add(time.Minute))
	connect(t, te, laptopMAC, "192.168.1.23", "203.0.113.5", 443, 6, 1000, start.Add(time.Minute))
	connect(t, te, laptopMAC, "192.168.1.23", "203.0.113.5", 23, 17, 1000, start.Add(time.Minute))

	ns := te.at(2 * time.Minute)
	if len(ns) != 1 || ns[0].Message.Title != "Apple device connected to port 23/tcp" ||
		!strings.Contains(ns[0].Message.Body, "of 203.0.113.5 and 1 other address, 4 kB in all") ||
		!strings.Contains(ns[0].Message.Body, "watches ports 23, 2323") {
		t.Fatalf("notifications = %v %+v", titles(ns), ns)
	}
	f := ns[0].Message.Fields
	if f["subject"] != tvMAC+"|23/tcp" || f["port"] != "23" || f["protocol"] != "tcp" || f["peer"] != "203.0.113.5" || f["peers"] != "2" {
		t.Errorf("fields = %v", f)
	}
	if alerts, _, _ := storage.ListAlerts(db, storage.AlertsFilter{RuleID: r.ID}); len(alerts) != 1 || alerts[0].State != storage.AlertResolved {
		t.Errorf("alerts = %+v, want one event", alerts)
	}

	// Doing it again is no news, for a day.
	connect(t, te, tvMAC, "192.168.1.40", "203.0.113.5", 23, 6, 100, start.Add(150*time.Second))
	if ns := te.at(3 * time.Minute); len(ns) != 0 {
		t.Errorf("again a minute later = %v, want nothing", titles(ns))
	}
	connect(t, te, tvMAC, "192.168.1.40", "203.0.113.5", 23, 6, 100, start.Add(25*time.Hour-time.Minute))
	if ns := te.at(25 * time.Hour); len(ns) != 1 {
		t.Errorf("again a day later = %v, want a new alert", titles(ns))
	}
}

// fakeCountries knows the countries of a few addresses.
type fakeCountries map[string][2]string

func (f fakeCountries) Available() bool { return f != nil }

func (f fakeCountries) Lookup(addr netip.Addr) (string, string, bool) {
	c, ok := f[addr.String()]
	return c[0], c[1], ok
}

func TestDestinationCountry(t *testing.T) {
	countries := fakeCountries{
		"5.6.7.8": {"RU", "Russia"}, "5.6.7.9": {"RU", "Russia"},
		"9.9.9.9": {"DE", "Germany"}, "1.1.1.1": {"US", "United States"},
	}
	db := openTestDB(t)
	te := newTestEngineWith(t, db, countries)
	rule(t, te, "Unusual countries", DestinationCountry, `{"mode": "deny", "countries": ["ru"]}`, false)
	rule(t, te, "Kids' tablet", DestinationCountry, `{"mode": "allow", "countries": ["DE", "PL"], "macs": ["`+laptopMAC+`"]}`, false)

	connect(t, te, tvMAC, "192.168.1.40", "5.6.7.8", 443, 6, 2000, start.Add(time.Minute))
	connect(t, te, tvMAC, "192.168.1.40", "5.6.7.9", 443, 6, 1000, start.Add(time.Minute))
	connect(t, te, tvMAC, "192.168.1.40", "9.9.9.9", 443, 6, 1000, start.Add(time.Minute))
	connect(t, te, laptopMAC, "192.168.1.23", "9.9.9.9", 443, 6, 1000, start.Add(time.Minute))
	connect(t, te, laptopMAC, "192.168.1.23", "1.1.1.1", 443, 6, 1000, start.Add(time.Minute))

	ns := te.at(2 * time.Minute)
	got := titles(ns)
	if len(ns) != 2 || got[0] != "firing: Apple device exchanged traffic with Russia" ||
		got[1] != "firing: Apple device exchanged traffic with United States" {
		t.Fatalf("notifications = %v", got)
	}
	if !strings.Contains(ns[0].Message.Body, "exchanged 3 kB with 5.6.7.8 and 1 other address in Russia (RU)") ||
		!strings.Contains(ns[1].Message.Body, "allows only DE, PL") || ns[1].Message.Fields["country"] != "US" {
		t.Errorf("messages = %+v", ns)
	}

	// Without a GeoIP database, country rules can't run, and say so once.
	other := openTestDB(t)
	blind := newTestEngineWith(t, other, nil)
	r := rule(t, blind, "Unusual countries", DestinationCountry, `{"mode": "deny", "countries": ["RU"]}`, false)
	connect(t, blind, tvMAC, "192.168.1.40", "5.6.7.8", 443, 6, 2000, start.Add(time.Minute))
	if ns := blind.at(2 * time.Minute); len(ns) != 0 {
		t.Errorf("without GeoIP = %v, want nothing", titles(ns))
	}
	if r, _ := storage.GetRule(other, r.ID); r.LastEvaluatedAt != nil {
		t.Error("the rule was marked as evaluated without GeoIP")
	}
}

func TestValidateNewParams(t *testing.T) {
	for _, tc := range []struct {
		ruleType, params, want string
	}{
		{DeviceInactive, `{"inactive_s": 1800, "macs": ["3C-22-FB-12-34-56"]}`, `{"inactive_s":1800,"macs":["3c:22:fb:12:34:56"]}`},
		{DestinationPort, `{"ports": [23, 22, 23], "protocol": "any"}`, `{"ports":[22,23],"protocol":"any"}`},
		{DestinationCountry, `{"mode": "allow", "countries": [" de", "PL", "DE"]}`, `{"mode":"allow","countries":["DE","PL"]}`},
		{TrafficThreshold, `{"direction": "total", "threshold_bytes": 1, "window_s": 300, "network": true}`, `{"direction":"total","threshold_bytes":1,"window_s":300,"network":true}`},
	} {
		got, err := ValidateParams(tc.ruleType, []byte(tc.params))
		if err != nil || got != tc.want {
			t.Errorf("ValidateParams(%s, %s) = %s, %v; want %s", tc.ruleType, tc.params, got, err, tc.want)
		}
	}

	for _, tc := range []struct{ ruleType, params string }{
		{DeviceInactive, `{"inactive_s": 1800}`},
		{DeviceInactive, `{"inactive_s": 60, "macs": ["3c:22:fb:12:34:56"]}`},
		{DestinationPort, `{"ports": [], "protocol": "tcp"}`},
		{DestinationPort, `{"ports": [70000], "protocol": "tcp"}`},
		{DestinationPort, `{"ports": [23], "protocol": "icmp"}`},
		{DestinationCountry, `{"mode": "deny", "countries": []}`},
		{DestinationCountry, `{"mode": "deny", "countries": ["Germany"]}`},
		{DestinationCountry, `{"mode": "maybe", "countries": ["DE"]}`},
	} {
		if _, err := ValidateParams(tc.ruleType, []byte(tc.params)); !IsValidationError(err) {
			t.Errorf("ValidateParams(%s, %s) = %v, want a validation error", tc.ruleType, tc.params, err)
		}
	}
}
