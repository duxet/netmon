package collector

import (
	"database/sql"
	"net/netip"
	"testing"

	"github.com/duxet/netmon/common"
	"github.com/duxet/netmon/storage"
	_ "github.com/marcboeker/go-duckdb"
	migrate "github.com/rubenv/sql-migrate"
)

// newTestDB returns an in-memory database with the schema the application uses.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	// All connections of the pool must see the same in-memory database.
	db.SetMaxOpenConns(1)

	_, err = migrate.Exec(db, "sqlite3", migrate.FileMigrationSource{Dir: "../migrations"}, migrate.Up)
	if err != nil {
		t.Fatal(err)
	}

	return db
}

// What the collector writes has to be what the rest of the application reads:
// the same queries that serve the API are run on the stored rows.
func TestFlushedFlowsAreReadableByTheApplication(t *testing.T) {
	db := newTestDB(t)
	ft := newTestTable()

	// The Mac downloads from a remote server, and talks to a machine on the LAN.
	ft.Add(withHint(pkt(host, 51234, server, 443, 60), hintSrcIsClient))
	ft.Add(withHint(pkt(server, 443, host, 51234, 60), hintDstIsClient))
	ft.Add(pkt(host, 51234, server, 443, 500))
	ft.Add(pkt(server, 443, host, 51234, 9000))
	ft.Add(pkt(host, 50000, peer, 22, 200))
	ft.Add(pkt(peer, 22, host, 50000, 300))

	// Somebody on the internet connects to the Mac.
	remote := netip.MustParseAddr("203.0.113.9")
	ft.Add(withHint(pkt(remote, 44444, host, 22, 60), hintSrcIsClient))
	ft.Add(withHint(pkt(host, 22, remote, 44444, 60), hintDstIsClient))

	if err := ft.flushTo(db); err != nil {
		t.Fatal(err)
	}

	flows := storage.GetFlows(db, storage.FlowsFilter{})
	if len(flows) != 3 {
		t.Fatalf("got %d flows, want 3: %+v", len(flows), flows)
	}

	// Hosts without a MAC address must be stored as NULL, which is what the
	// queries behind the client list rely on, and not as an empty value.
	var nullSrc, nullDst, emptyMACs int
	err := db.QueryRow(`SELECT
			count(*) FILTER (WHERE src_mac IS NULL),
			count(*) FILTER (WHERE dst_mac IS NULL),
			count(*) FILTER (WHERE octet_length(src_mac) != 6 OR octet_length(dst_mac) != 6)
		FROM flows`).Scan(&nullSrc, &nullDst, &emptyMACs)
	if err != nil {
		t.Fatal(err)
	}
	if nullSrc != 1 || nullDst != 1 || emptyMACs != 0 {
		t.Errorf("NULL source MACs = %d (want 1), NULL destination MACs = %d (want 1), malformed MACs = %d (want 0)", nullSrc, nullDst, emptyMACs)
	}

	// Biggest first.
	web := flows[0]
	if web.SourceIPAddress.Addr.String() != "192.168.1.10" || web.DestinationIPAddress.Addr.String() != "93.184.216.34" {
		t.Errorf("unexpected endpoints %v -> %v", web.SourceIPAddress.Addr, web.DestinationIPAddress.Addr)
	}
	if web.IPProto != 6 || web.Port != 443 {
		t.Errorf("proto = %d, port = %d", web.IPProto, web.Port)
	}
	if web.OutBytes != 560 || web.OutPackets != 2 || web.InBytes != 9060 || web.InPackets != 2 {
		t.Errorf("traffic = %+v", web)
	}
	if got := web.SourceMACAddress.String(); got != hostMAC.hw().String() {
		t.Errorf("source MAC = %q, want the host's", got)
	}
	if len(web.DestinationMACAddress.HardwareAddr) != 0 {
		t.Errorf("destination MAC = %v, the remote server has none", web.DestinationMACAddress)
	}

	ssh := flows[1]
	if got := ssh.DestinationMACAddress.String(); got != peerMAC.hw().String() {
		t.Errorf("destination MAC = %q, want the LAN peer's", got)
	}

	// The inbound connection is stored from the point of view of whoever opened it.
	inbound := flows[2]
	if inbound.SourceIPAddress.Addr.String() != "203.0.113.9" || inbound.DestinationIPAddress.Addr.String() != "192.168.1.10" || inbound.Port != 22 {
		t.Errorf("unexpected inbound flow %v -> %v:%d", inbound.SourceIPAddress.Addr, inbound.DestinationIPAddress.Addr, inbound.Port)
	}
	if got := inbound.DestinationMACAddress.String(); got != hostMAC.hw().String() {
		t.Errorf("destination MAC = %q, want the host's", got)
	}

	// The remote server must not show up as a client, nor the gateway.
	clients := storage.GetClients(db)
	if len(clients) != 1 {
		t.Fatalf("got %d clients, want only the Mac: %+v", len(clients), clients)
	}
	if got := clients[0].MACAddress.String(); got != hostMAC.hw().String() {
		t.Errorf("client MAC = %q, want the host's", got)
	}
	if clients[0].IPAddress.Addr.String() != "192.168.1.10" {
		t.Errorf("client IP = %v", clients[0].IPAddress.Addr)
	}

	// The per-client queries of the UI find the flows by MAC and by IP.
	if n := len(storage.GetFlows(db, storage.FlowsFilter{MAC: &common.MACAddress{HardwareAddr: hostMAC.hw()}})); n != 3 {
		t.Errorf("%d flows found by MAC, want 3", n)
	}
	ip := common.IPAddress{Addr: &host}
	if n := len(storage.GetFlows(db, storage.FlowsFilter{IP: &ip})); n != 3 {
		t.Errorf("%d flows found by IP, want 3", n)
	}

	stats := storage.GetStats(db)
	if stats.TotalClients != 1 || stats.TotalBytes == nil || *stats.TotalBytes != 560+9060+200+300+60+60 {
		t.Errorf("stats = %+v", stats)
	}
}

func TestFlushingNothingWritesNothing(t *testing.T) {
	db := newTestDB(t)

	if err := newTestTable().flushTo(db); err != nil {
		t.Fatal(err)
	}

	if n := len(storage.GetFlows(db, storage.FlowsFilter{})); n != 0 {
		t.Errorf("%d flows stored", n)
	}
}

func TestIPv6FlowsAreStored(t *testing.T) {
	db := newTestDB(t)
	ft := newFlowTable()

	src, dst := netip.MustParseAddr("2001:db8::10"), netip.MustParseAddr("2001:db8:1::53")
	ft.Add(pkt(src, 40000, dst, 53, 80))

	if err := ft.flushTo(db); err != nil {
		t.Fatal(err)
	}

	flows := storage.GetFlows(db, storage.FlowsFilter{})
	if len(flows) != 1 || *flows[0].SourceIPAddress.Addr != src || *flows[0].DestinationIPAddress.Addr != dst {
		t.Errorf("unexpected flows %+v", flows)
	}
}
