package alerting

import (
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/duxet/netmon/blocklists"
	"github.com/duxet/netmon/storage"
)

func TestInQuietHours(t *testing.T) {
	at := func(hour, minute int) time.Time { return time.Date(2026, 11, 24, hour, minute, 0, 0, time.UTC) }
	for _, test := range []struct {
		from, to string
		t        time.Time
		want     bool
	}{
		{"22:00", "07:00", at(23, 30), true},
		{"22:00", "07:00", at(3, 0), true},
		{"22:00", "07:00", at(7, 0), false},
		{"22:00", "07:00", at(12, 0), false},
		{"09:00", "17:30", at(17, 29), true},
		{"09:00", "17:30", at(8, 59), false},
	} {
		if got := inQuietHours(test.from, test.to, test.t); got != test.want {
			t.Errorf("%s–%s at %s = %v, want %v", test.from, test.to, test.t.Format("15:04"), got, test.want)
		}
	}

	for _, bad := range [][2]string{{"22:00", ""}, {"25:00", "07:00"}, {"07:00", "07:00"}, {"10pm", "7am"}} {
		if err := ValidateQuietHours(bad[0], bad[1]); err == nil {
			t.Errorf("quiet hours %q–%q are valid, want an error", bad[0], bad[1])
		}
	}
	if err := ValidateQuietHours("", ""); err != nil {
		t.Errorf("no quiet hours: %v", err)
	}
}

func TestSnoozedAndQuietRulesHoldTheirNotifications(t *testing.T) {
	db := openTestDB(t)
	rule := addRule(t, db, storage.RuleRecord{Name: "New devices", Type: NewDevice, Params: "{}", ChannelIDs: []int64{1}}, start)
	engine := newTestEngine(t, db)

	until := start.Add(time.Hour)
	if err := storage.SnoozeRule(db, rule.ID, &until); err != nil {
		t.Fatal(err)
	}
	flow(t, db, tvMAC, "192.168.1.40", googleIP, 100, 100, start.Add(time.Minute))
	ns := engine.at(2 * time.Minute)
	if len(ns) != 1 || !strings.Contains(ns[0].Hold, "snoozed until") {
		t.Fatalf("notifications while snoozed = %+v, want one held", ns)
	}

	// Snoozed no longer, but quiet all day but the minute it is.
	if err := storage.SnoozeRule(db, rule.ID, nil); err != nil {
		t.Fatal(err)
	}
	rule.QuietFrom, rule.QuietTo = "00:00", "23:59"
	if _, err := storage.UpdateRule(db, rule, start, false); err != nil {
		t.Fatal(err)
	}
	flow(t, db, laptopMAC, "192.168.1.23", googleIP, 100, 100, start.Add(3*time.Minute))
	ns = engine.at(4 * time.Minute)
	if len(ns) != 1 || !strings.Contains(ns[0].Hold, "quiet from 00:00 to 23:59") {
		t.Fatalf("notifications in quiet hours = %+v, want one held", ns)
	}
}

func TestAcknowledgedAlertsAreNotRepeated(t *testing.T) {
	db := openTestDB(t)
	hour := int64(3600)
	rule := trafficRule(t, db, `{"direction": "download", "threshold_bytes": 5000, "window_s": 3600}`, &hour)
	engine := newTestEngine(t, db)

	flow(t, db, tvMAC, "192.168.1.40", googleIP, 6000, 100, start.Add(-2*time.Minute))
	if ns := engine.at(0); len(ns) != 1 {
		t.Fatalf("notifications = %v, want the TV firing", titles(ns))
	}
	firing, _ := storage.FiringAlerts(db, rule.ID)
	if err := storage.AcknowledgeAlert(db, firing[tvMAC].ID, start, "Ada"); err != nil {
		t.Fatal(err)
	}

	flow(t, db, tvMAC, "192.168.1.40", googleIP, 6000, 100, start.Add(50*time.Minute))
	if ns := engine.at(61 * time.Minute); len(ns) != 0 {
		t.Errorf("an hour later = %v, want no reminder for an acknowledged alert", titles(ns))
	}
	if ns := engine.at(3 * time.Hour); len(ns) != 1 || ns[0].Kind != "resolved" {
		t.Errorf("once it's over = %v, want it resolved all the same", titles(ns))
	}
}

func TestDeviceOnline(t *testing.T) {
	db := openTestDB(t)
	params, err := ValidateParams(DeviceOnline, []byte(`{"inactive_s": 3600, "macs": ["`+strings.ToUpper(tvMAC)+`"]}`))
	if err != nil {
		t.Fatal(err)
	}
	addRule(t, db, storage.RuleRecord{Name: "TV back", Type: DeviceOnline, Params: params}, start.Add(-3*time.Hour))
	engine := newTestEngine(t, db)

	flow(t, db, tvMAC, "192.168.1.40", googleIP, 100, 100, start.Add(-3*time.Hour))
	flow(t, db, laptopMAC, "192.168.1.23", googleIP, 100, 100, start.Add(-3*time.Hour))
	if err := storage.SyncDevices(db, time.Time{}); err != nil {
		t.Fatal(err)
	}

	// The TV is back after 3 hours; the laptop too, but the rule is only
	// about the TV.
	flow(t, db, tvMAC, "192.168.1.40", googleIP, 100, 100, start.Add(time.Minute))
	flow(t, db, laptopMAC, "192.168.1.23", googleIP, 100, 100, start.Add(time.Minute))
	ns := engine.at(2 * time.Minute)
	if len(ns) != 1 || !strings.HasSuffix(ns[0].Message.Title, " is back online") || ns[0].Message.Fields["mac"] != tvMAC ||
		!strings.Contains(ns[0].Message.Body, "after 3 hours without") || ns[0].Message.Fields["quiet_s"] != "10800" {
		t.Fatalf("notifications = %v %+v, want the TV back after 3 hours", titles(ns), ns)
	}

	// Still online a minute later is nothing new.
	flow(t, db, tvMAC, "192.168.1.40", googleIP, 100, 100, start.Add(3*time.Minute))
	if ns := engine.at(4 * time.Minute); len(ns) != 0 {
		t.Errorf("a minute later = %v, want nothing", titles(ns))
	}
}

// fakeASNs knows the networks of a few addresses.
type fakeASNs map[string]uint

func (f fakeASNs) Available() bool { return f != nil }

func (f fakeASNs) Lookup(addr netip.Addr) (uint, string, bool) {
	n, ok := f[addr.String()]
	return n, "AS" + strconv.FormatUint(uint64(n), 10) + " INC", ok
}

func TestNewDestination(t *testing.T) {
	db := openTestDB(t)
	countries := fakeCountries{googleIP: {"US", "United States"}, telegramIP: {"GB", "United Kingdom"}, "1.1.1.1": {"AU", "Australia"}}
	params, err := ValidateParams(NewDestination, []byte(`{"kind": "country"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(params, `"baseline_days":30`) {
		t.Errorf("params = %s, want 30 days by default", params)
	}
	addRule(t, db, storage.RuleRecord{Name: "New countries", Type: NewDestination, Params: params, ChannelIDs: []int64{1}}, start.Add(-10*24*time.Hour))
	engine := newTestEngineWith(t, db, countries)

	// The laptop talks to the US every day; the phone is new.
	for day := 10; day >= 1; day-- {
		flow(t, db, laptopMAC, "192.168.1.23", googleIP, 100, 100, start.Add(-time.Duration(day)*24*time.Hour))
	}
	flow(t, db, phoneMAC, "192.168.1.50", googleIP, 100, 100, start.Add(-time.Hour))
	if err := storage.SyncDevices(db, time.Time{}); err != nil {
		t.Fatal(err)
	}

	flow(t, db, laptopMAC, "192.168.1.23", googleIP, 100, 100, start.Add(time.Minute))
	flow(t, db, laptopMAC, "192.168.1.23", telegramIP, 5000, 100, start.Add(time.Minute))
	flow(t, db, phoneMAC, "192.168.1.50", "1.1.1.1", 100, 100, start.Add(time.Minute))
	ns := engine.at(2 * time.Minute)
	if len(ns) != 1 || !strings.HasSuffix(ns[0].Message.Title, " exchanged traffic with United Kingdom for the first time") ||
		ns[0].Message.Fields["country"] != "GB" || ns[0].Message.Fields["mac"] != laptopMAC {
		t.Fatalf("notifications = %v, want the laptop's first traffic with the UK, and nothing about the new phone", titles(ns))
	}

	// The UK is known from then on.
	flow(t, db, laptopMAC, "192.168.1.23", telegramIP, 5000, 100, start.Add(26*time.Hour))
	if ns := engine.at(26*time.Hour + time.Minute); len(ns) != 0 {
		t.Errorf("the UK again, a day later = %v, want nothing", titles(ns))
	}

	// Networks need their database.
	asnParams, _ := ValidateParams(NewDestination, []byte(`{"kind": "asn", "baseline_days": 7}`))
	addRule(t, db, storage.RuleRecord{Name: "New networks", Type: NewDestination, Params: asnParams}, start)
	engine.Engine.opts.ASNs = fakeASNs{googleIP: 15169, telegramIP: 62041}
	flow(t, db, laptopMAC, "192.168.1.23", "1.1.1.1", 5000, 100, start.Add(27*time.Hour))
	engine.Engine.opts.ASNs = fakeASNs{googleIP: 15169, telegramIP: 62041, "1.1.1.1": 13335}
	ns = engine.at(27*time.Hour + time.Minute)
	if len(titles(ns)) != 2 {
		t.Errorf("first traffic with Australia and AS13335 = %v, want an alert from each rule", titles(ns))
	}
}

func TestNewRuleParams(t *testing.T) {
	for _, bad := range []struct{ ruleType, params string }{
		{DeviceOnline, `{"inactive_s": 10}`},
		{DeviceOnline, `{"inactive_s": 3600, "macs": ["nope"]}`},
		{NewDestination, `{"kind": "city"}`},
		{NewDestination, `{"kind": "asn", "baseline_days": 365}`},
	} {
		if _, err := ValidateParams(bad.ruleType, []byte(bad.params)); err == nil {
			t.Errorf("%s %s is valid, want an error", bad.ruleType, bad.params)
		}
	}
	_ = net.HardwareAddr{}
}

func TestBlocklist(t *testing.T) {
	db := openTestDB(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "feodo.txt"), []byte("# C2\n50.16.16.211\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lists := blocklists.New(dir)

	params, err := ValidateParams(Blocklist, []byte(`{"lists": ["feodo"]}`))
	if err != nil {
		t.Fatal(err)
	}
	addRule(t, db, storage.RuleRecord{Name: "Botnets", Type: Blocklist, Params: params, ChannelIDs: []int64{1}}, start.Add(-time.Hour))
	te := newTestEngine(t, db)
	te.Engine.opts.Blocklists = lists

	flow(t, db, laptopMAC, "192.168.1.23", "50.16.16.211", 900, 300, start.Add(time.Minute))
	flow(t, db, laptopMAC, "192.168.1.23", googleIP, 900, 300, start.Add(time.Minute))
	ns := te.at(2 * time.Minute)
	if len(ns) != 1 || !strings.Contains(ns[0].Message.Title, "talked to an address on abuse.ch Feodo Tracker") ||
		ns[0].Message.Fields["peer"] != "50.16.16.211" || ns[0].Message.Fields["list"] != "feodo" {
		t.Fatalf("notifications = %v %+v, want the laptop talking to the C2 server", titles(ns), ns)
	}

	// Once a day per device and list.
	flow(t, db, laptopMAC, "192.168.1.23", "50.16.16.211", 900, 300, start.Add(3*time.Minute))
	if ns := te.at(4 * time.Minute); len(ns) != 0 {
		t.Errorf("again a minute later = %v, want nothing", titles(ns))
	}

	if _, err := ValidateParams(Blocklist, []byte(`{"lists": ["nope"]}`)); err == nil {
		t.Error("an unknown list is valid")
	}
}
