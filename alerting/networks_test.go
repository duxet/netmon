package alerting

import (
	"database/sql"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/duxet/netmon/networks"
	"github.com/duxet/netmon/storage"
)

// iotNetwork adds the network 192.168.20.0/24, called IoT, and returns its ID.
func iotNetwork(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	n, err := storage.CreateNetwork(db, networks.Network{
		Name: "IoT", Color: networks.Colors[0], Prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.20.0/24")},
	}, start)
	if err != nil {
		t.Fatal(err)
	}
	return n.ID
}

func idList(ids ...int64) string {
	var s []string
	for _, id := range ids {
		s = append(s, itoa(id))
	}
	return "[" + strings.Join(s, ",") + "]"
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestTrafficOfANetwork(t *testing.T) {
	db := openTestDB(t)
	iot := iotNetwork(t, db)
	flow(t, db, tvMAC, "192.168.20.40", googleIP, 6000, 100, start.Add(-2*time.Minute))
	flow(t, db, laptopMAC, "192.168.1.23", "1.1.1.1", 7000, 100, start.Add(-2*time.Minute))
	if err := storage.SyncDevices(db, time.Time{}); err != nil {
		t.Fatal(err)
	}
	te := newTestEngine(t, db)
	rule(t, te, "IoT downloads", TrafficThreshold, `{"direction": "download", "threshold_bytes": 5000, "window_s": 3600, "networks": `+idList(iot)+`}`, true)

	ns := te.at(0)
	if len(ns) != 1 || ns[0].Message.Fields["mac"] != tvMAC {
		t.Fatalf("notifications = %v, want the TV's alone, which is on the IoT network", titles(ns))
	}
	if f := ns[0].Message.Fields; f["network"] != "IoT" || f["network_id"] != itoa(iot) ||
		!strings.Contains(ns[0].Message.Body, "192.168.20.40 on IoT") {
		t.Errorf("message = %q %v, want it to name the network", ns[0].Message.Body, f)
	}
}

func TestRuleOfDevicesAndNetworks(t *testing.T) {
	db := openTestDB(t)
	iot := iotNetwork(t, db)
	flow(t, db, tvMAC, "192.168.20.40", googleIP, 6000, 100, start.Add(-2*time.Minute))
	flow(t, db, laptopMAC, "192.168.1.23", "1.1.1.1", 7000, 100, start.Add(-2*time.Minute))
	flow(t, db, phoneMAC, "192.168.1.50", "1.1.1.1", 8000, 100, start.Add(-2*time.Minute))
	if err := storage.SyncDevices(db, time.Time{}); err != nil {
		t.Fatal(err)
	}
	te := newTestEngine(t, db)
	rule(t, te, "Downloads", TrafficThreshold, `{"direction": "download", "threshold_bytes": 5000, "window_s": 3600,
		"macs": ["`+laptopMAC+`"], "networks": `+idList(iot)+`}`, true)

	var macs []string
	for _, n := range te.at(0) {
		macs = append(macs, n.Message.Fields["mac"])
	}
	slices.Sort(macs)
	if want := []string{tvMAC, laptopMAC}; !slices.Equal(macs, want) {
		t.Errorf("alerts about %v, want the laptop and the TV on the IoT network, not the phone", macs)
	}
}

func TestTrafficOfTheWholeOfANetwork(t *testing.T) {
	db := openTestDB(t)
	iot := iotNetwork(t, db)
	flow(t, db, tvMAC, "192.168.20.40", googleIP, 6000, 100, start.Add(-2*time.Minute))
	flow(t, db, laptopMAC, "192.168.20.23", "1.1.1.1", 5000, 100, start.Add(-2*time.Minute))
	flow(t, db, phoneMAC, "192.168.1.50", "1.1.1.1", 90000, 100, start.Add(-2*time.Minute))
	if err := storage.SyncDevices(db, time.Time{}); err != nil {
		t.Fatal(err)
	}
	te := newTestEngine(t, db)
	rule(t, te, "IoT cap", TrafficThreshold, `{"direction": "download", "threshold_bytes": 10000, "window_s": 3600,
		"network": true, "networks": `+idList(iot)+`}`, true)

	ns := te.at(0)
	if len(ns) != 1 || ns[0].Message.Title != "Large download on the IoT network" ||
		!strings.HasPrefix(ns[0].Message.Body, "The IoT network downloaded 11 kB") || ns[0].Message.Fields["network"] != "IoT" {
		t.Fatalf("notifications = %v %+v", titles(ns), ns)
	}
	ns = te.at(2 * time.Hour)
	if len(ns) != 1 || !strings.HasPrefix(ns[0].Message.Body, "The IoT network is back under 10 kB") {
		t.Errorf("after the traffic stopped = %v %+v", titles(ns), ns)
	}
}

func TestInactiveDevicesOfANetwork(t *testing.T) {
	db := openTestDB(t)
	iot := iotNetwork(t, db)
	flow(t, db, tvMAC, "192.168.20.40", googleIP, 100, 100, start.Add(-2*time.Minute))
	flow(t, db, laptopMAC, "192.168.1.23", googleIP, 100, 100, start.Add(-2*time.Minute))
	if err := storage.SyncDevices(db, time.Time{}); err != nil {
		t.Fatal(err)
	}
	te := newTestEngine(t, db)
	rule(t, te, "IoT quiet", DeviceInactive, `{"inactive_s": 1800, "networks": `+idList(iot)+`}`, true)

	ns := te.at(31 * time.Minute)
	if len(ns) != 1 || ns[0].Message.Fields["mac"] != tvMAC {
		t.Fatalf("notifications = %v, want the TV's alone", titles(ns))
	}
}

func TestNewDeviceInANetwork(t *testing.T) {
	db := openTestDB(t)
	iot := iotNetwork(t, db)
	te := newTestEngine(t, db)
	rule(t, te, "New IoT device", NewDevice, `{"networks": `+idList(iot)+`}`, false)
	flow(t, db, tvMAC, "192.168.20.40", googleIP, 100, 100, start.Add(time.Minute))
	flow(t, db, laptopMAC, "192.168.1.23", googleIP, 100, 100, start.Add(time.Minute))

	ns := te.at(2 * time.Minute)
	if len(ns) != 1 || ns[0].Message.Fields["mac"] != tvMAC || !strings.Contains(ns[0].Message.Body, "joined the IoT network") {
		t.Fatalf("notifications = %v %+v, want the TV, which joined the IoT network", titles(ns), ns)
	}
}

func TestValidateNetworks(t *testing.T) {
	params, err := ValidateParams(DestinationPort, []byte(`{"ports": [22], "protocol": "tcp", "networks": [3, 1, 3]}`))
	if err != nil || params != `{"ports":[22],"protocol":"tcp","networks":[1,3]}` {
		t.Errorf("params = %s, %v, want the networks sorted once each", params, err)
	}
	if got := ParamNetworks(params); !slices.Equal(got, []int64{1, 3}) {
		t.Errorf("ParamNetworks = %v", got)
	}
	without, err := WithNetworks(params, nil)
	if err != nil || ParamNetworks(without) != nil || !strings.Contains(without, `"ports":[22]`) {
		t.Errorf("WithNetworks(nil) = %s, %v", without, err)
	}
	with, err := WithNetworks(without, []int64{7})
	if err != nil || !slices.Equal(ParamNetworks(with), []int64{7}) {
		t.Errorf("WithNetworks(7) = %s, %v", with, err)
	}

	for ruleType, raw := range map[string]string{
		DeviceInactive:   `{"inactive_s": 1800}`,
		TrafficThreshold: `{"direction": "total", "threshold_bytes": 1, "window_s": 3600, "networks": [0]}`,
		NewDevice:        `{"networks": [-1]}`,
	} {
		if _, err := ValidateParams(ruleType, []byte(raw)); !IsValidationError(err) {
			t.Errorf("%s %s: %v, want a validation error", ruleType, raw, err)
		}
	}
	if _, err := ValidateParams(DeviceInactive, []byte(`{"inactive_s": 1800, "networks": [1]}`)); err != nil {
		t.Errorf("an inactive rule of a network: %v", err)
	}
}
