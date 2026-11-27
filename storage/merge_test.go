package storage

import (
	"errors"
	"net"
	"testing"
	"time"
)

func TestMergeDevices(t *testing.T) {
	db := openTestDB(t)
	const phoneA, phoneB = "da:a1:19:00:00:01", "da:a1:19:00:00:02"
	insert(t, db,
		row{mustMAC(t, phoneA), nil, mustIP("192.168.1.50"), mustIP("140.82.121.4"), 6, 443, 1000, 100, seedAt},
		row{mustMAC(t, phoneB), nil, mustIP("192.168.20.50"), mustIP("140.82.121.4"), 6, 443, 500, 50, seedAt.Add(time.Hour)},
		row{mustMAC(t, laptopMAC), mustMAC(t, phoneA), mustIP("192.168.1.23"), mustIP("192.168.1.50"), 6, 8080, 20, 30, seedAt},
	)
	if err := SyncDevices(db, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := SetDeviceName(db, mustMAC(t, phoneA), "Ada's phone"); err != nil {
		t.Fatal(err)
	}
	rule, err := CreateRule(db, RuleRecord{Name: "Phones", Type: "device_inactive", Params: `{"inactive_s":3600,"macs":["` + phoneA + `","` + laptopMAC + `"]}`,
		Severity: "info", Enabled: true}, seedAt)
	if err != nil {
		t.Fatal(err)
	}

	if err := MergeDevices(db, mustMAC(t, phoneB), []net.HardwareAddr{mustMAC(t, phoneA), mustMAC(t, phoneB)}, seedAt); err != nil {
		t.Fatal(err)
	}

	devices, err := GetDevices(db)
	if err != nil {
		t.Fatal(err)
	}
	byMAC := map[string]DeviceRecord{}
	for _, d := range devices {
		byMAC[d.MACAddress.String()] = d
	}
	b, ok := byMAC[phoneB]
	if _, stillThere := byMAC[phoneA]; stillThere || !ok || !b.FirstSeen.Equal(seedAt) || !b.LastSeen.Equal(seedAt.Add(time.Hour)) ||
		b.Name == nil || *b.Name != "Ada's phone" || b.IPAddress.String() != "192.168.20.50" {
		t.Fatalf("devices = %+v, want one phone, seen since the first one was, with its name and the last address", byMAC)
	}

	clients, err := GetClients(db, FlowsFilter{MAC: mac(t, phoneB)})
	if err != nil {
		t.Fatal(err)
	}
	var down uint64
	for _, c := range clients {
		if c.MACAddress.String() == phoneB {
			down += c.InBytes
		}
	}
	if down != 1500 {
		t.Errorf("the merged phone downloaded %d bytes, want the 1500 of both", down)
	}

	if got, _ := GetRule(db, rule.ID); got.Params != `{"inactive_s":3600,"macs":["`+phoneB+`","`+laptopMAC+`"]}` {
		t.Errorf("rule params = %s, want the merged phone in place of the old one", got.Params)
	}

	// Traffic to come from the merged address is the phone's too.
	insert(t, db, row{mustMAC(t, phoneA), nil, mustIP("192.168.1.51"), mustIP("1.1.1.1"), 17, 53, 90, 70, seedAt.Add(2 * time.Hour)})
	if err := SyncDevices(db, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if devices, _ := GetDevices(db); len(devices) != 2 {
		t.Errorf("devices after new traffic = %+v, want the laptop and the phone only", devices)
	}

	// Merging the phone into the laptop takes what was merged into it along.
	if err := MergeDevices(db, mustMAC(t, laptopMAC), []net.HardwareAddr{mustMAC(t, phoneB)}, seedAt); err != nil {
		t.Fatal(err)
	}
	aliases, err := GetAliases(db)
	if err != nil {
		t.Fatal(err)
	}
	if got := aliases[laptopMAC]; len(got) != 2 || len(aliases) != 1 {
		t.Errorf("aliases = %v, want both phones' addresses on the laptop", aliases)
	}

	if err := DeleteAlias(db, mustMAC(t, phoneA)); err != nil {
		t.Fatal(err)
	}
	if err := DeleteAlias(db, mustMAC(t, phoneA)); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting an alias twice: %v, want ErrNotFound", err)
	}
}

func TestForgetDevice(t *testing.T) {
	db := openTestDB(t)
	seed(t, db)
	if err := SyncDevices(db, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := SetDeviceName(db, mustMAC(t, nasMAC), "NAS"); err != nil {
		t.Fatal(err)
	}

	if err := ForgetDevice(db, mustMAC(t, nasMAC), false); err != nil {
		t.Fatal(err)
	}
	devices, _ := GetDevices(db)
	names, _ := GetDeviceNames(db)
	if len(devices) != 1 || len(names) != 0 {
		t.Errorf("devices %+v and names %v, want the NAS gone", devices, names)
	}
	if n := count(t, db, "flows"); n != 3 {
		t.Errorf("%d flows, want all 3 kept", n)
	}

	// From the internet to the NAS, which is forgotten with the NAS; and from
	// the laptop to it, which stays the laptop's.
	insert(t, db, row{nil, mustMAC(t, nasMAC), mustIP("203.0.113.7"), mustIP("192.168.1.10"), 6, 443, 50, 9000, seedAt})
	if err := ForgetDevice(db, mustMAC(t, nasMAC), true); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, "flows"); n != 3 {
		t.Errorf("%d flows after forgetting the NAS with its traffic, want the laptop's 3", n)
	}
	var withNAS int
	if err := db.QueryRow(`SELECT count(*) FROM flows WHERE dst_mac = ?`, mustMAC(t, nasMAC)).Scan(&withNAS); err != nil || withNAS != 0 {
		t.Errorf("%d flows still name the NAS (%v)", withNAS, err)
	}

	if err := ForgetDevice(db, mustMAC(t, laptopMAC), true); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, "flows"); n != 0 {
		t.Errorf("%d flows after forgetting the laptop with its traffic, want none", n)
	}
}
