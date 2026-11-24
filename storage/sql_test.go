package storage

import (
	"database/sql"
	"net"
	"net/netip"
	"reflect"
	"testing"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/duxet/netmon/common"
	migrate "github.com/rubenv/sql-migrate"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// One connection, so session settings such as TimeZone apply to every query.
	db.SetMaxOpenConns(1)

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

// seedAt is the time of the first seeded row.
var seedAt = time.Date(2026, 11, 23, 20, 15, 0, 0, time.UTC)

type row struct {
	srcMAC, dstMAC []byte
	srcIP, dstIP   []byte
	proto          uint8
	port           uint16
	in, out        uint64
	createdAt      time.Time
}

func insert(t *testing.T, db *sql.DB, rows ...row) {
	t.Helper()
	for _, r := range rows {
		_, err := db.Exec(`INSERT INTO flows VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.srcMAC, r.dstMAC, r.srcIP, r.dstIP, r.proto, r.port, r.in, r.in/10, r.out, r.out/10, r.createdAt)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func seed(t *testing.T, db *sql.DB) {
	t.Helper()

	insert(t, db,
		// Laptop to the internet, in two collection rounds an hour apart.
		row{mustMAC(t, laptopMAC), nil, mustIP("192.168.1.23"), mustIP("142.250.186.78"), 6, 443, 1000, 100, seedAt},
		row{mustMAC(t, laptopMAC), nil, mustIP("192.168.1.23"), mustIP("142.250.186.78"), 6, 443, 500, 50, seedAt.Add(time.Hour)},
		// Laptop to the NAS on the LAN.
		row{mustMAC(t, laptopMAC), mustMAC(t, nasMAC), mustIP("192.168.1.23"), mustIP("192.168.1.10"), 6, 445, 20, 2000, seedAt},
	)
}

func mac(t *testing.T, s string) *common.MACAddress {
	t.Helper()
	m, err := common.ParseMACAddress(s)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func ip(t *testing.T, s string) *common.IPAddress {
	t.Helper()
	a, err := common.ParseIPAddress(s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestQueriesOnEmptyDatabase(t *testing.T) {
	db := openTestDB(t)

	stats, err := GetStats(db, FlowsFilter{})
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

	nasFlows, err := GetFlows(db, FlowsFilter{MAC: mac(t, nasMAC)})
	if err != nil {
		t.Fatal(err)
	}
	if len(nasFlows) != 1 || nasFlows[0].Port != 445 {
		t.Errorf("NAS flows = %+v, want only the flow where it is the destination", nasFlows)
	}

	clients, err := GetClients(db, FlowsFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(clients) != 1 || clients[0].MACAddress.String() != laptopMAC || clients[0].InBytes != 1520 {
		t.Errorf("clients = %+v, want the laptop with 1520 bytes in", clients)
	}

	stats, err := GetStats(db, FlowsFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalClients != 1 || stats.TotalBytes == nil || *stats.TotalBytes != 3670 {
		t.Errorf("stats = %+v, want 1 client and 3670 bytes", stats)
	}

	measurements, err := GetTrafficMeasurements(db, FlowsFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(measurements) != 2 {
		t.Errorf("got %d hourly buckets, want 2", len(measurements))
	}

	pairs, err := GetEndpointPairTraffic(db, FlowsFilter{IP: ip(t, "192.168.1.10")})
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || pairs[0].OutBytes != 2000 {
		t.Errorf("pairs = %+v, want the laptop-NAS pair", pairs)
	}
}

func TestTimeRange(t *testing.T) {
	db := openTestDB(t)
	seed(t, db)

	from := seedAt.Add(30 * time.Minute)
	stats, err := GetStats(db, FlowsFilter{From: &from})
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalBytes == nil || *stats.TotalBytes != 550 {
		t.Errorf("bytes from %v = %v, want only the second round's 550", from, stats.TotalBytes)
	}

	to := from
	flows, err := GetFlows(db, FlowsFilter{To: &to, MAC: mac(t, laptopMAC)})
	if err != nil {
		t.Fatal(err)
	}
	var in uint64
	for _, flow := range flows {
		in += flow.InBytes
	}
	if in != 1020 {
		t.Errorf("bytes in before %v = %d, want the first round's 1020", to, in)
	}

	empty := seedAt.Add(-time.Hour)
	clients, err := GetClients(db, FlowsFilter{To: &empty})
	if err != nil || len(clients) != 0 {
		t.Errorf("clients before any traffic = %v, %v; want none", clients, err)
	}
}

func TestMeasurementsFromClientSide(t *testing.T) {
	db := openTestDB(t)
	seed(t, db)

	// The NAS is the destination of the SMB flow: it uploaded the 20 reply
	// bytes and downloaded the 2000 the laptop sent.
	measurements, err := GetTrafficMeasurements(db, FlowsFilter{MAC: mac(t, nasMAC)})
	if err != nil {
		t.Fatal(err)
	}
	if len(measurements) != 1 || measurements[0].InBytes != 2000 || measurements[0].OutBytes != 20 {
		t.Errorf("NAS measurements = %+v, want 2000 down and 20 up", measurements)
	}

	measurements, err = GetTrafficMeasurements(db, FlowsFilter{IP: ip(t, "192.168.1.23")})
	if err != nil {
		t.Fatal(err)
	}
	if len(measurements) != 2 || measurements[0].InBytes != 1020 || measurements[0].OutBytes != 2100 {
		t.Errorf("laptop measurements = %+v, want 1020 down and 2100 up in the first hour", measurements)
	}
}

// Hour buckets are UTC instants whatever the server's time zone; casting to
// TIMESTAMP used to shift them by the UTC offset.
func TestHourBucketsIgnoreServerTimeZone(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`SET TimeZone = 'Asia/Kolkata'`); err != nil {
		t.Fatal(err)
	}
	seed(t, db)

	want := seedAt.Truncate(time.Hour)
	for _, compacted := range []bool{false, true} {
		if compacted {
			if _, err := Compact(db, seedAt.Add(3*time.Hour), 0); err != nil {
				t.Fatal(err)
			}
		}

		measurements, err := GetTrafficMeasurements(db, FlowsFilter{})
		if err != nil {
			t.Fatal(err)
		}
		if len(measurements) == 0 || !measurements[0].Date.Equal(want) {
			t.Errorf("compacted=%v: first bucket = %v, want %v", compacted, measurements, want)
		}

		from := want
		to := want.Add(time.Hour)
		stats, err := GetStats(db, FlowsFilter{From: &from, To: &to})
		if err != nil {
			t.Fatal(err)
		}
		if stats.TotalBytes == nil || *stats.TotalBytes != 3120 {
			t.Errorf("compacted=%v: bytes in [%v, %v) = %v, want 3120", compacted, from, to, stats.TotalBytes)
		}
	}
}

type snapshot struct {
	Flows        []FlowRecord
	Clients      []ClientRecord
	Stats        StatsRecord
	Measurements []TrafficMeasurementRecord
	Pairs        []EndpointPairTrafficRecord
}

func take(t *testing.T, db *sql.DB) snapshot {
	t.Helper()
	var s snapshot
	var err error
	if s.Flows, err = GetFlows(db, FlowsFilter{}); err != nil {
		t.Fatal(err)
	}
	if s.Clients, err = GetClients(db, FlowsFilter{}); err != nil {
		t.Fatal(err)
	}
	if s.Stats, err = GetStats(db, FlowsFilter{}); err != nil {
		t.Fatal(err)
	}
	if s.Measurements, err = GetTrafficMeasurements(db, FlowsFilter{}); err != nil {
		t.Fatal(err)
	}
	if s.Pairs, err = GetEndpointPairTrafficByHour(db, FlowsFilter{}); err != nil {
		t.Fatal(err)
	}
	return s
}

func count(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCompactKeepsResults(t *testing.T) {
	db := openTestDB(t)
	seed(t, db)
	// More collection rounds in the first hour, and one in the current hour.
	insert(t, db,
		row{mustMAC(t, laptopMAC), nil, mustIP("192.168.1.23"), mustIP("142.250.186.78"), 6, 443, 300, 30, seedAt.Add(time.Minute)},
		row{mustMAC(t, laptopMAC), nil, mustIP("192.168.1.23"), mustIP("142.250.186.78"), 6, 443, 200, 20, seedAt.Add(2 * time.Minute)},
		row{mustMAC(t, laptopMAC), nil, mustIP("192.168.1.23"), mustIP("1.1.1.1"), 17, 53, 90, 70, seedAt.Add(2 * time.Hour)},
	)
	now := seedAt.Add(2*time.Hour + 10*time.Minute)

	before := take(t, db)
	// Sum totals don't depend on row order within equal keys; compare sorted results.
	result, err := Compact(db, now, 0)
	if err != nil {
		t.Fatal(err)
	}
	after := take(t, db)

	if !reflect.DeepEqual(before.Stats, after.Stats) || !reflect.DeepEqual(before.Clients, after.Clients) ||
		!reflect.DeepEqual(before.Measurements, after.Measurements) || !reflect.DeepEqual(before.Flows, after.Flows) ||
		len(before.Pairs) != len(after.Pairs) {
		t.Errorf("results changed by compaction:\nbefore %+v\nafter  %+v", before, after)
	}

	// Five rows from complete hours become three (HTTPS x 2 hours, SMB); the
	// current hour's DNS row stays per minute.
	if result.Removed != 5 || result.RolledUp != 3 || result.Expired != 0 {
		t.Errorf("result = %+v, want 5 rows rolled up into 3", result)
	}
	if n := count(t, db, "flows"); n != 1 {
		t.Errorf("%d per-minute rows left, want the current hour's 1", n)
	}

	// Compacting again changes nothing.
	if again, err := Compact(db, now, 0); err != nil || again != (CompactResult{}) {
		t.Errorf("second compaction = %+v, %v; want no changes", again, err)
	}
	if !reflect.DeepEqual(take(t, db).Stats, before.Stats) {
		t.Error("second compaction changed the totals")
	}
}

func TestCompactRetention(t *testing.T) {
	db := openTestDB(t)
	seed(t, db)
	now := seedAt.Add(10 * 24 * time.Hour)

	if _, err := Compact(db, now, 0); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, "flows_hourly"); n != 3 {
		t.Fatalf("%d hourly rows, want 3", n)
	}

	result, err := Compact(db, now, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if result.Expired != 3 || count(t, db, "flows_hourly") != 0 {
		t.Errorf("result = %+v, want all 3 rows older than 7 days expired", result)
	}
}

// A database error must reach the caller instead of stopping the process.
func TestQueriesReturnErrors(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`DROP VIEW all_flows`); err != nil {
		t.Fatal(err)
	}

	if _, err := GetFlows(db, FlowsFilter{}); err == nil {
		t.Error("GetFlows: want an error")
	}
	if _, err := GetStats(db, FlowsFilter{}); err == nil {
		t.Error("GetStats: want an error")
	}
	if _, err := GetClients(db, FlowsFilter{}); err == nil {
		t.Error("GetClients: want an error")
	}
	if _, err := GetTrafficMeasurements(db, FlowsFilter{}); err == nil {
		t.Error("GetTrafficMeasurements: want an error")
	}
	if _, err := GetEndpointPairTraffic(db, FlowsFilter{}); err == nil {
		t.Error("GetEndpointPairTraffic: want an error")
	}
	if _, err := GetEndpointPairTrafficByHour(db, FlowsFilter{}); err == nil {
		t.Error("GetEndpointPairTrafficByHour: want an error")
	}
}
