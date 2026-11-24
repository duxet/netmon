package storage

import (
	"database/sql"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/duxet/netmon/common"
	_ "github.com/marcboeker/go-duckdb"
	migrate "github.com/rubenv/sql-migrate"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	migrations := &migrate.FileMigrationSource{Dir: "../migrations"}
	if _, err := migrate.Exec(db, "sqlite3", migrations, migrate.Up); err != nil {
		t.Fatal(err)
	}

	return db
}

func mustMAC(t *testing.T, s string) []byte {
	t.Helper()
	mac, err := net.ParseMAC(s)
	if err != nil {
		t.Fatal(err)
	}
	return mac
}

func mustIP(s string) []byte {
	return netip.MustParseAddr(s).AsSlice()
}

const (
	laptopMAC = "a4:83:e7:1c:2f:90"
	nasMAC    = "00:11:32:9b:c4:01"
)

func seed(t *testing.T, db *sql.DB) {
	t.Helper()

	at := time.Date(2026, 11, 23, 20, 15, 0, 0, time.UTC)
	rows := []struct {
		srcMAC, dstMAC []byte
		srcIP, dstIP   []byte
		proto          uint8
		port           uint16
		in, out        uint64
		createdAt      time.Time
	}{
		// Laptop to the internet, in two collection rounds an hour apart.
		{mustMAC(t, laptopMAC), nil, mustIP("192.168.1.23"), mustIP("142.250.186.78"), 6, 443, 1000, 100, at},
		{mustMAC(t, laptopMAC), nil, mustIP("192.168.1.23"), mustIP("142.250.186.78"), 6, 443, 500, 50, at.Add(time.Hour)},
		// Laptop to the NAS on the LAN.
		{mustMAC(t, laptopMAC), mustMAC(t, nasMAC), mustIP("192.168.1.23"), mustIP("192.168.1.10"), 6, 445, 20, 2000, at},
	}

	for _, r := range rows {
		_, err := db.Exec(`INSERT INTO flows VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.srcMAC, r.dstMAC, r.srcIP, r.dstIP, r.proto, r.port, r.in, r.in/10, r.out, r.out/10, r.createdAt)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestQueriesOnEmptyDatabase(t *testing.T) {
	db := openTestDB(t)

	stats, err := GetStats(db)
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalClients != 0 || stats.TotalBytes != nil || stats.TotalPackets != nil {
		t.Errorf("stats = %+v, want zero clients and no totals", stats)
	}

	if flows, err := GetFlows(db, FlowsFilter{}); err != nil || len(flows) != 0 {
		t.Errorf("GetFlows = %v, %v; want no flows and no error", flows, err)
	}
}

func TestQueries(t *testing.T) {
	db := openTestDB(t)
	seed(t, db)

	flows, err := GetFlows(db, FlowsFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(flows) != 2 {
		t.Fatalf("got %d flows, want 2 grouped flows", len(flows))
	}
	if flows[0].Port != 445 || flows[0].OutBytes != 2000 {
		t.Errorf("first flow = %+v, want the larger SMB flow first", flows[0])
	}

	nas, err := common.ParseMACAddress(nasMAC)
	if err != nil {
		t.Fatal(err)
	}
	nasFlows, err := GetFlows(db, FlowsFilter{MAC: nas})
	if err != nil {
		t.Fatal(err)
	}
	if len(nasFlows) != 1 || nasFlows[0].Port != 445 {
		t.Errorf("NAS flows = %+v, want only the flow where it is the destination", nasFlows)
	}

	clients, err := GetClients(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(clients) != 1 || clients[0].MACAddress.String() != laptopMAC || clients[0].InBytes != 1520 {
		t.Errorf("clients = %+v, want the laptop with 1520 bytes in", clients)
	}

	stats, err := GetStats(db)
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalClients != 1 || stats.TotalBytes == nil || *stats.TotalBytes != 3670 {
		t.Errorf("stats = %+v, want 1 client and 3670 bytes", stats)
	}

	measurements, err := GetTrafficMeasurements(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(measurements) != 2 {
		t.Errorf("got %d hourly buckets, want 2", len(measurements))
	}

	ip, err := common.ParseIPAddress("192.168.1.10")
	if err != nil {
		t.Fatal(err)
	}
	pairs, err := GetEndpointPairTraffic(db, FlowsFilter{IP: ip})
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || pairs[0].OutBytes != 2000 {
		t.Errorf("pairs = %+v, want the laptop-NAS pair", pairs)
	}
}

// A database error must reach the caller instead of stopping the process.
func TestQueriesReturnErrors(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`DROP TABLE flows`); err != nil {
		t.Fatal(err)
	}

	if _, err := GetFlows(db, FlowsFilter{}); err == nil {
		t.Error("GetFlows: want an error")
	}
	if _, err := GetStats(db); err == nil {
		t.Error("GetStats: want an error")
	}
	if _, err := GetClients(db); err == nil {
		t.Error("GetClients: want an error")
	}
	if _, err := GetTrafficMeasurements(db); err == nil {
		t.Error("GetTrafficMeasurements: want an error")
	}
	if _, err := GetEndpointPairTraffic(db, FlowsFilter{}); err == nil {
		t.Error("GetEndpointPairTraffic: want an error")
	}
}
