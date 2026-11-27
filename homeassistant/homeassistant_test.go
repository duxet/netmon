package homeassistant

import (
	"database/sql"
	"encoding/json"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/duxet/netmon/collector"
	"github.com/duxet/netmon/storage"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	migrate "github.com/rubenv/sql-migrate"
)

type message struct {
	topic    string
	retained bool
	payload  []byte
}

type doneToken struct{ mqtt.Token }

func (doneToken) Wait() bool                     { return true }
func (doneToken) WaitTimeout(time.Duration) bool { return true }
func (doneToken) Done() <-chan struct{}          { c := make(chan struct{}); close(c); return c }
func (doneToken) Error() error                   { return nil }

// fakeClient keeps what was published, by topic.
type fakeClient struct {
	mu        sync.Mutex
	published map[string]message
}

func (f *fakeClient) Publish(topic string, _ byte, retained bool, payload any) mqtt.Token {
	f.mu.Lock()
	defer f.mu.Unlock()
	var data []byte
	switch p := payload.(type) {
	case []byte:
		data = p
	case string:
		data = []byte(p)
	}
	f.published[topic] = message{topic, retained, data}
	return doneToken{}
}

func (f *fakeClient) Disconnect(uint) {}

func (f *fakeClient) json(t *testing.T, topic string) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.published[topic]
	if !ok {
		t.Fatalf("nothing published to %s; topics: %v", topic, f.topics())
	}
	var v map[string]any
	if err := json.Unmarshal(m.payload, &v); err != nil {
		t.Fatalf("%s: %v in %q", topic, err, m.payload)
	}
	return v
}

func (f *fakeClient) topics() []string {
	var topics []string
	for topic := range f.published {
		topics = append(topics, topic)
	}
	return topics
}

const (
	laptopMAC = "a4:83:e7:1c:2f:90"
	tvMAC     = "3c:22:fb:8a:01:7e"
)

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

func addDevice(t *testing.T, db *sql.DB, mac, ip string, seen time.Time) net.HardwareAddr {
	t.Helper()
	hw, _ := net.ParseMAC(mac)
	_, err := db.Exec(`INSERT INTO devices VALUES (?, ?, ?, ?)`, []byte(hw), netip.MustParseAddr(ip).AsSlice(), seen, seen)
	if err != nil {
		t.Fatal(err)
	}
	return hw
}

func TestPublish(t *testing.T) {
	db := openTestDB(t)
	now := time.Date(2026, 11, 27, 20, 0, 0, 0, time.UTC)
	laptop := addDevice(t, db, laptopMAC, "192.168.1.23", now.Add(-time.Minute))
	addDevice(t, db, tvMAC, "192.168.1.41", now.Add(-time.Hour))
	if err := storage.SetDeviceName(db, laptop, "Ada's laptop"); err != nil {
		t.Fatal(err)
	}

	totals := collector.NewTotals()
	fake := &fakeClient{published: map[string]message{}}
	p := newPublisher(Config{}, db, totals)
	p.client = fake

	p.Publish(now)
	tracker := fake.json(t, "homeassistant/device_tracker/netmon_a483e71c2f90/presence/config")
	device := tracker["device"].(map[string]any)
	if device["name"] != "Ada's laptop" || tracker["state_topic"] != "netmon/device/a483e71c2f90/state" ||
		tracker["source_type"] != "router" || tracker["availability_topic"] != "netmon/status" {
		t.Errorf("tracker config = %v", tracker)
	}
	if download := fake.json(t, "homeassistant/sensor/netmon_a483e71c2f90/download/config"); download["unit_of_measurement"] != "B/s" {
		t.Errorf("download config = %v", download)
	}
	if state := fake.json(t, "netmon/device/a483e71c2f90/state"); state["presence"] != "home" || state["download"] != 0.0 || state["ip"] != "192.168.1.23" {
		t.Errorf("first state = %v, want home with no rate yet", state)
	}
	if _, ok := fake.published["netmon/device/3c22fb8a017e/state"]; ok {
		t.Error("the TV, which has no name or icon, was published")
	}
	fake.json(t, "homeassistant/sensor/netmon_network/download/config")

	// A minute later, the laptop downloaded 6 MB: 100 kB/s.
	totals.AddFlow(laptop, nil, 6_000_000, 600_000)
	p.Publish(now.Add(time.Minute))
	if state := fake.json(t, "netmon/device/a483e71c2f90/state"); state["download"] != 100_000.0 || state["upload"] != 10_000.0 {
		t.Errorf("state = %v, want 100 kB/s down and 10 kB/s up", state)
	}
	if network := fake.json(t, "netmon/network/state"); network["download"] != 100_000.0 {
		t.Errorf("network = %v, want 100 kB/s down", network)
	}

	// Half an hour on, it is away.
	p.Publish(now.Add(30 * time.Minute))
	if state := fake.json(t, "netmon/device/a483e71c2f90/state"); state["presence"] != "not_home" {
		t.Errorf("state = %v, want it away", state)
	}

	// Without its name, it is taken away from Home Assistant.
	if err := storage.DeleteDeviceName(db, laptop); err != nil {
		t.Fatal(err)
	}
	p.Publish(now.Add(31 * time.Minute))
	if m := fake.published["homeassistant/device_tracker/netmon_a483e71c2f90/presence/config"]; len(m.payload) != 0 || !m.retained {
		t.Errorf("tracker config after removing the name = %q, want it cleared", m.payload)
	}

	// With every device, the TV is there too, as it was seen in the last day.
	p.cfg.AllDevices = true
	p.Publish(now.Add(32 * time.Minute))
	if state := fake.json(t, "netmon/device/3c22fb8a017e/state"); state["presence"] != "not_home" {
		t.Errorf("TV state = %v", state)
	}
	if !strings.HasPrefix(string(fake.published["netmon/device/3c22fb8a017e/state"].payload), "{") {
		t.Error("TV state isn't JSON")
	}
}
