package storage

import (
	"database/sql"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/duxet/netmon/networks"
)

func prefixes(t *testing.T, values ...string) []netip.Prefix {
	t.Helper()
	p, err := networks.ParsePrefixes(values)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNetworksCRUD(t *testing.T) {
	db := openTestDB(t)
	now := time.Date(2026, 11, 24, 8, 0, 0, 0, time.UTC)

	if list, err := ListNetworks(db); err != nil || len(list) != 0 {
		t.Fatalf("ListNetworks = %v, %v; want none", list, err)
	}

	vlan := 20
	iot, err := CreateNetwork(db, networks.Network{
		Name: "IoT", VLAN: &vlan, Color: "orange", Prefixes: prefixes(t, "192.168.20.0/24", "fd00:20::/64"),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if iot.ID == 0 || iot.Name != "IoT" || iot.VLAN == nil || *iot.VLAN != 20 || iot.Color != "orange" ||
		len(iot.Prefixes) != 2 || iot.Prefixes[1].String() != "fd00:20::/64" || !iot.CreatedAt.Equal(now) {
		t.Errorf("created = %+v", iot)
	}

	lan, err := CreateNetwork(db, networks.Network{Name: "LAN", Color: "blue", Prefixes: prefixes(t, "192.168.1.0/24")}, now)
	if err != nil {
		t.Fatal(err)
	}
	if lan.VLAN != nil {
		t.Errorf("VLAN = %d, want none", *lan.VLAN)
	}

	later := now.Add(time.Hour)
	iot.Name, iot.VLAN, iot.Prefixes = "Smart home", nil, prefixes(t, "10.0.20.0/24")
	updated, err := UpdateNetwork(db, iot.Network, later)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Smart home" || updated.VLAN != nil || len(updated.Prefixes) != 1 ||
		!updated.UpdatedAt.Equal(later) || !updated.CreatedAt.Equal(now) {
		t.Errorf("updated = %+v", updated)
	}

	list, err := ListNetworks(db)
	if err != nil || len(list) != 2 || list[0].ID != iot.ID || list[1].ID != lan.ID {
		t.Errorf("ListNetworks = %+v, %v; want both, oldest first", list, err)
	}

	m, err := NetworkMatcher(db)
	if err != nil {
		t.Fatal(err)
	}
	if n := m.Match(netip.MustParseAddr("192.168.1.7")); n == nil || n.ID != lan.ID {
		t.Errorf("matcher put 192.168.1.7 in %v, want LAN", n)
	}

	if err := DeleteNetwork(db, lan.ID); err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{
		DeleteNetwork(db, lan.ID),
		func() error { _, err := GetNetwork(db, lan.ID); return err }(),
		func() error { _, err := UpdateNetwork(db, lan.Network, later); return err }(),
	} {
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("missing network: error = %v, want ErrNotFound", err)
		}
	}
}

// networkScopes returns the test networks and the scope of each: a LAN with
// an IPv6 block too, IoT, and a camera inside IoT.
func networkScopes(t *testing.T) (lan, iot, camera networks.Scope, all map[int64]networks.Scope) {
	t.Helper()
	m := networks.NewMatcher([]networks.Network{
		{ID: 1, Name: "LAN", Prefixes: prefixes(t, "192.168.1.0/24", "fd00:1::/64")},
		{ID: 2, Name: "IoT", Prefixes: prefixes(t, "192.168.20.0/24")},
		{ID: 3, Name: "Camera", Prefixes: prefixes(t, "192.168.20.99")},
	})
	all = map[int64]networks.Scope{}
	for _, id := range []int64{1, 2, 3} {
		scope, _ := m.Scope(id)
		all[id] = scope
	}
	return all[1], all[2], all[3], all
}

const (
	tvMAC     = "3c:22:fb:00:00:05"
	cameraMAC = "3c:22:fb:00:00:99"
	phoneMAC  = "a4:83:e7:00:00:07"
	otherMAC  = "a4:83:e7:00:00:08"
)

func seedNetworks(t *testing.T, db *sql.DB) {
	t.Helper()
	at := seedAt
	insert(t, db,
		// LAN: the laptop to the internet and to the NAS.
		row{mustMAC(t, laptopMAC), nil, mustIP("192.168.1.23"), mustIP("142.250.186.78"), 6, 443, 1000, 100, at},
		row{mustMAC(t, laptopMAC), mustMAC(t, nasMAC), mustIP("192.168.1.23"), mustIP("192.168.1.10"), 6, 445, 20, 2000, at},
		// A phone on the LAN's IPv6 block.
		row{mustMAC(t, phoneMAC), nil, mustIP("fd00:1::5"), mustIP("2a00:1450::1"), 6, 443, 400, 40, at},
		// IoT: the TV to the internet, and the laptop casting to it.
		row{mustMAC(t, tvMAC), nil, mustIP("192.168.20.5"), mustIP("142.250.186.78"), 6, 443, 5000, 50, at},
		row{mustMAC(t, laptopMAC), mustMAC(t, tvMAC), mustIP("192.168.1.23"), mustIP("192.168.20.5"), 6, 8009, 300, 30, at},
		// The camera, in its own network inside IoT's block.
		row{mustMAC(t, cameraMAC), nil, mustIP("192.168.20.99"), mustIP("3.120.0.1"), 6, 443, 10, 7000, at},
		// An IPv6 address that starts with the bytes of 192.168.20.0, which
		// is no IPv4 network's, and an address just past IoT's block.
		row{mustMAC(t, otherMAC), nil, mustIP("c0a8:1405::1"), mustIP("2a00:1450::1"), 6, 443, 1, 1, at},
		row{mustMAC(t, otherMAC), nil, mustIP("192.168.21.0"), mustIP("142.250.186.78"), 6, 443, 1, 1, at},
	)
}

func TestFlowsInNetwork(t *testing.T) {
	db := openTestDB(t)
	seedNetworks(t, db)
	lan, iot, camera, _ := networkScopes(t)

	flows, err := GetFlows(db, FlowsFilter{Network: &iot})
	if err != nil {
		t.Fatal(err)
	}
	ports := map[uint16]bool{}
	for _, f := range flows {
		ports[f.Port] = true
		if f.SourceIPAddress.String() == "192.168.20.99" {
			t.Error("IoT has the camera's flow, which belongs to the more specific camera network")
		}
	}
	if len(flows) != 2 || !ports[443] || !ports[8009] {
		t.Errorf("IoT flows = %+v, want the TV's and the laptop casting to it", flows)
	}

	// Counted from IoT's side: the TV downloaded 5000 and was sent 30 by the laptop.
	measurements, err := GetTrafficMeasurements(db, FlowsFilter{Network: &iot}, Hourly)
	if err != nil {
		t.Fatal(err)
	}
	if len(measurements) != 1 || measurements[0].InBytes != 5030 || measurements[0].OutBytes != 350 {
		t.Errorf("IoT measurements = %+v, want 5030 down and 350 up", measurements)
	}

	stats, err := GetStats(db, FlowsFilter{Network: &iot})
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalClients != 1 {
		t.Errorf("IoT clients = %d, want only the TV (the laptop is on the LAN)", stats.TotalClients)
	}

	clients, err := GetClients(db, FlowsFilter{Network: &lan})
	if err != nil {
		t.Fatal(err)
	}
	macs := map[string]bool{}
	for _, c := range clients {
		macs[c.MACAddress.String()] = true
	}
	if len(clients) != 2 || !macs[laptopMAC] || !macs[phoneMAC] {
		t.Errorf("LAN clients = %+v, want the laptop and the IPv6 phone", clients)
	}

	if clients, err := GetClients(db, FlowsFilter{Network: &camera}); err != nil || len(clients) != 1 ||
		clients[0].MACAddress.String() != cameraMAC {
		t.Errorf("camera clients = %+v, %v", clients, err)
	}

	// A network without blocks has nothing.
	empty := networks.Scope{}
	if flows, err := GetFlows(db, FlowsFilter{Network: &empty}); err != nil || len(flows) != 0 {
		t.Errorf("flows of an empty scope = %v, %v", flows, err)
	}
}

func TestGetNetworkTraffic(t *testing.T) {
	db := openTestDB(t)
	seedNetworks(t, db)
	// An hour earlier, outside the range asked for below.
	insert(t, db, row{mustMAC(t, tvMAC), nil, mustIP("192.168.20.5"), mustIP("142.250.186.78"), 6, 443, 1, 1, seedAt.Add(-time.Hour)})
	_, _, _, scopes := networkScopes(t)

	if records, err := GetNetworkTraffic(db, nil, FlowsFilter{}); err != nil || records != nil {
		t.Errorf("without networks = %v, %v; want nothing", records, err)
	}

	from := seedAt
	records, err := GetNetworkTraffic(db, scopes, FlowsFilter{From: &from})
	if err != nil {
		t.Fatal(err)
	}

	key := func(id *int64) int64 {
		if id == nil {
			return 0
		}
		return *id
	}
	got := map[[2]int64][2]uint64{}
	for _, r := range records {
		got[[2]int64{key(r.Source), key(r.Destination)}] = [2]uint64{r.InBytes, r.OutBytes}
	}
	want := map[[2]int64][2]uint64{
		{1, 0}: {1400, 140}, // the laptop and the phone to the internet
		{1, 1}: {20, 2000},  // the laptop to the NAS
		{1, 2}: {300, 30},   // the laptop casting to the TV
		{2, 0}: {5000, 50},  // the TV
		{3, 0}: {10, 7000},  // the camera
	}
	if len(got) != len(want) {
		t.Errorf("records = %v, want %v", got, want)
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("traffic from network %d to %d = %v, want %v", k[0], k[1], got[k], w)
		}
	}
}
