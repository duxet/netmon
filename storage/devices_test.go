package storage

import (
	"database/sql"
	"net"
	"testing"
	"time"

	migrate "github.com/rubenv/sql-migrate"
)

func TestDeviceNames(t *testing.T) {
	db := openTestDB(t)
	laptop, _ := net.ParseMAC(laptopMAC)
	nas, _ := net.ParseMAC(nasMAC)

	names, err := GetDeviceNames(db)
	if err != nil || len(names) != 0 {
		t.Fatalf("names = %v, %v; want none", names, err)
	}

	if err := SetDeviceName(db, laptop, "Work laptop"); err != nil {
		t.Fatal(err)
	}
	if err := SetDeviceName(db, nas, "NAS"); err != nil {
		t.Fatal(err)
	}
	// Setting a name again replaces it.
	if err := SetDeviceName(db, laptop, "Anna's laptop"); err != nil {
		t.Fatal(err)
	}

	names, err = GetDeviceNames(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[laptopMAC] != "Anna's laptop" || names[nasMAC] != "NAS" {
		t.Errorf("names = %v", names)
	}

	if err := DeleteDeviceName(db, laptop); err != nil {
		t.Fatal(err)
	}
	if err := DeleteDeviceName(db, laptop); err != nil {
		t.Errorf("deleting a missing name: %v", err)
	}
	names, _ = GetDeviceNames(db)
	if len(names) != 1 || names[nasMAC] != "NAS" {
		t.Errorf("after delete, names = %v; want only the NAS", names)
	}
}

func TestDeviceIcons(t *testing.T) {
	db := openTestDB(t)
	laptop, _ := net.ParseMAC(laptopMAC)
	nas, _ := net.ParseMAC(nasMAC)

	icons, err := GetDeviceIcons(db)
	if err != nil || len(icons) != 0 {
		t.Fatalf("icons = %v, %v; want none", icons, err)
	}

	if err := SetDeviceIcon(db, laptop, "desktop"); err != nil {
		t.Fatal(err)
	}
	if err := SetDeviceIcon(db, nas, "nas"); err != nil {
		t.Fatal(err)
	}
	// Setting an icon again replaces it.
	if err := SetDeviceIcon(db, laptop, "laptop"); err != nil {
		t.Fatal(err)
	}
	// Icons are kept apart from names: naming a device leaves its icon.
	if err := SetDeviceName(db, nas, "NAS"); err != nil {
		t.Fatal(err)
	}

	icons, err = GetDeviceIcons(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(icons) != 2 || icons[laptopMAC] != "laptop" || icons[nasMAC] != "nas" {
		t.Errorf("icons = %v", icons)
	}

	if err := DeleteDeviceIcon(db, laptop); err != nil {
		t.Fatal(err)
	}
	if err := DeleteDeviceIcon(db, laptop); err != nil {
		t.Errorf("deleting a missing icon: %v", err)
	}
	icons, _ = GetDeviceIcons(db)
	if len(icons) != 1 || icons[nasMAC] != "nas" {
		t.Errorf("after delete, icons = %v; want only the NAS", icons)
	}
	// And removing the name leaves the icon.
	if err := DeleteDeviceName(db, nas); err != nil {
		t.Fatal(err)
	}
	if icons, _ = GetDeviceIcons(db); icons[nasMAC] != "nas" {
		t.Errorf("after removing the name, icons = %v; want the NAS's", icons)
	}
}

// deviceList returns the devices by MAC address, failing the test on errors.
func deviceList(t *testing.T, db *sql.DB) map[string]DeviceRecord {
	t.Helper()
	devices, err := GetDevices(db)
	if err != nil {
		t.Fatal(err)
	}
	byMAC := map[string]DeviceRecord{}
	for _, d := range devices {
		byMAC[d.MACAddress.String()] = d
	}
	return byMAC
}

func checkDevice(t *testing.T, devices map[string]DeviceRecord, mac, ip string, firstSeen, lastSeen time.Time) {
	t.Helper()
	d, ok := devices[mac]
	if !ok {
		t.Errorf("device %s not listed", mac)
		return
	}
	if d.IPAddress.String() != ip || !d.FirstSeen.Equal(firstSeen) || !d.LastSeen.Equal(lastSeen) {
		t.Errorf("device %s = %s, first %v, last %v; want %s, %v, %v",
			mac, d.IPAddress, d.FirstSeen, d.LastSeen, ip, firstSeen, lastSeen)
	}
}

func TestSyncDevices(t *testing.T) {
	db := openTestDB(t)
	laptop := mustMAC(t, laptopMAC)
	nas := mustMAC(t, nasMAC)

	insert(t, db,
		// The laptop to the internet, then from a new address a minute later.
		row{laptop, nil, mustIP("192.168.1.23"), mustIP("142.250.186.78"), 6, 443, 1000, 100, seedAt},
		row{laptop, nil, mustIP("192.168.1.24"), mustIP("142.250.186.78"), 6, 443, 1000, 100, seedAt.Add(time.Minute)},
		// The NAS is only ever a destination.
		row{laptop, nas, mustIP("192.168.1.24"), mustIP("192.168.1.10"), 6, 445, 20, 2000, seedAt.Add(time.Minute)},
		// A flow without MAC addresses (e.g. the collector's own traffic) is no
		// device. The nil slices are stored as empty BLOBs, as the Linux
		// collector does for incomplete neighbor entries.
		row{nil, nil, mustIP("10.8.0.2"), mustIP("1.1.1.1"), 17, 53, 90, 70, seedAt},
	)

	if err := SyncDevices(db, seedAt.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	devices := deviceList(t, db)
	if len(devices) != 2 {
		t.Fatalf("devices = %+v, want the laptop and the NAS", devices)
	}
	checkDevice(t, devices, laptopMAC, "192.168.1.24", seedAt, seedAt.Add(time.Minute))
	checkDevice(t, devices, nasMAC, "192.168.1.10", seedAt.Add(time.Minute), seedAt.Add(time.Minute))

	// Syncing the same rows again changes nothing.
	if err := SyncDevices(db, seedAt.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	checkDevice(t, deviceList(t, db), laptopMAC, "192.168.1.24", seedAt, seedAt.Add(time.Minute))

	// A later round updates the last address and time, but not first_seen,
	// although the window no longer covers the first rows.
	later := seedAt.Add(10 * time.Minute)
	insert(t, db, row{laptop, nil, mustIP("192.168.1.23"), mustIP("1.1.1.1"), 17, 53, 90, 70, later})
	if err := SyncDevices(db, later.Add(-5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	devices = deviceList(t, db)
	checkDevice(t, devices, laptopMAC, "192.168.1.23", seedAt, later)
	checkDevice(t, devices, nasMAC, "192.168.1.10", seedAt.Add(time.Minute), seedAt.Add(time.Minute))

	// A window reaching back over older rows doesn't move the last address back.
	if err := SyncDevices(db, time.Time{}); err != nil {
		t.Fatal(err)
	}
	checkDevice(t, deviceList(t, db), laptopMAC, "192.168.1.23", seedAt, later)

	// Most recently seen first, with names and icons, each set on its own.
	if err := SetDeviceName(db, nas, "NAS"); err != nil {
		t.Fatal(err)
	}
	if err := SetDeviceIcon(db, laptop, "laptop"); err != nil {
		t.Fatal(err)
	}
	list, err := GetDevices(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].MACAddress.String() != laptopMAC ||
		list[0].Name != nil || list[0].Icon == nil || *list[0].Icon != "laptop" ||
		list[1].Name == nil || *list[1].Name != "NAS" || list[1].Icon != nil {
		t.Errorf("devices = %+v, want the laptop first with its icon and the NAS named", list)
	}
}

// The migration that adds the devices table records the devices already in
// the database, including those only left in hourly rollups.
func TestDevicesMigrationSeedsHistory(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	migrations := &migrate.FileMigrationSource{Dir: "../migrations"}
	if _, err := migrate.ExecMax(db, "sqlite3", migrations, migrate.Up, 3); err != nil {
		t.Fatal(err)
	}

	seed(t, db)
	// Roll the seeded hours up, then add a per-minute row.
	if _, err := Compact(db, seedAt.Add(3*time.Hour), 0, 0); err != nil {
		t.Fatal(err)
	}
	current := seedAt.Add(3 * time.Hour)
	insert(t, db, row{mustMAC(t, laptopMAC), nil, mustIP("192.168.1.30"), mustIP("1.1.1.1"), 17, 53, 90, 70, current})

	if _, err := migrate.Exec(db, "sqlite3", migrations, migrate.Up); err != nil {
		t.Fatal(err)
	}

	devices := deviceList(t, db)
	if len(devices) != 2 {
		t.Fatalf("devices = %+v, want the laptop and the NAS", devices)
	}
	// Hourly rows are stamped with the start of their hour.
	checkDevice(t, devices, laptopMAC, "192.168.1.30", seedAt.Truncate(time.Hour), current)
	checkDevice(t, devices, nasMAC, "192.168.1.10", seedAt.Truncate(time.Hour), seedAt.Truncate(time.Hour))
}

func TestDeviceTraffic(t *testing.T) {
	db := openTestDB(t)
	seed(t, db)

	traffic := func(since time.Time) map[string][2]uint64 {
		t.Helper()
		records, err := GetDeviceTraffic(db, since)
		if err != nil {
			t.Fatal(err)
		}
		byPair := map[string][2]uint64{}
		for _, r := range records {
			key := r.MACAddress.String() + " " + r.Peer.String()
			byPair[key] = [2]uint64{r.Download, r.Upload}
		}
		return byPair
	}

	// The laptop's HTTPS over both rounds, and SMB with the NAS seen from
	// both sides: what the laptop sent, the NAS received.
	all := traffic(seedAt)
	want := map[string][2]uint64{
		laptopMAC + " 142.250.186.78": {1500, 150},
		laptopMAC + " 192.168.1.10":   {20, 2000},
		nasMAC + " 192.168.1.23":      {2000, 20},
	}
	if len(all) != len(want) {
		t.Errorf("traffic = %v, want %v", all, want)
	}
	for key, value := range want {
		if all[key] != value {
			t.Errorf("traffic[%s] = %v, want %v", key, all[key], value)
		}
	}

	// Only the second round is in a window starting after the first.
	if later := traffic(seedAt.Add(time.Minute)); len(later) != 1 || later[laptopMAC+" 142.250.186.78"] != [2]uint64{500, 50} {
		t.Errorf("traffic of the second round = %v", later)
	}

	// Rolled up, the first round counts as of the start of its hour.
	if _, err := Compact(db, seedAt.Add(3*time.Hour), 0, 0); err != nil {
		t.Fatal(err)
	}
	if rolled := traffic(seedAt.Truncate(time.Hour)); rolled[laptopMAC+" 142.250.186.78"] != [2]uint64{1500, 150} {
		t.Errorf("rolled up traffic = %v", rolled)
	}
}

func TestPortTraffic(t *testing.T) {
	db := openTestDB(t)
	seed(t, db)
	insert(t, db, row{mustMAC(t, laptopMAC), nil, mustIP("192.168.1.23"), mustIP("203.0.113.5"), 6, 23, 40, 60, seedAt})

	records, err := GetPortTraffic(db, seedAt, []uint16{23, 445}, []int{6})
	if err != nil {
		t.Fatal(err)
	}
	ports := map[uint16]uint64{}
	for _, r := range records {
		if r.MACAddress.String() != laptopMAC {
			t.Errorf("record of %s, want only the laptop's connections", r.MACAddress)
		}
		ports[r.Port] += r.Bytes
	}
	if len(records) != 2 || ports[23] != 100 || ports[445] != 2020 {
		t.Errorf("records = %+v, want telnet and SMB", records)
	}

	if records, _ := GetPortTraffic(db, seedAt, []uint16{23}, []int{17}); len(records) != 0 {
		t.Errorf("UDP records = %+v, want none", records)
	}
	if records, _ := GetPortTraffic(db, seedAt.Add(time.Minute), []uint16{23, 443}, nil); len(records) != 1 || records[0].Port != 443 {
		t.Errorf("later records = %+v, want the second HTTPS round", records)
	}
}
