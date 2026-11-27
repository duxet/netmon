package api

import (
	"database/sql"
	"fmt"
	"math/rand/v2"
	"net"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/duxet/netmon/common"
	"github.com/duxet/netmon/networks"
	"github.com/duxet/netmon/storage"
	migrate "github.com/rubenv/sql-migrate"
)

// The database pages and sums flows as orienting and paging them in Go did,
// for every filter, sort and page, ties and mixed address families included.
func TestFlowsInSQLMatchGo(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrate.Exec(db, "sqlite3", &migrate.FileMigrationSource{Dir: "../migrations"}, migrate.Up); err != nil {
		t.Fatal(err)
	}

	r := rand.New(rand.NewPCG(1, 2))
	macs := []net.HardwareAddr{
		{0xa4, 0x83, 0xe7, 0x1c, 0x2f, 0x90}, {0x3c, 0x22, 0xfb, 0x8a, 0x01, 0x7e}, {0x00, 0x11, 0x32, 0x9b, 0xc4, 0x01}, nil,
	}
	addrs := []string{"192.168.1.23", "192.168.1.41", "192.168.1.10", "10.0.20.5", "140.82.121.4", "200.1.1.1",
		"1.1.1.1", "2001:db8::10", "2a00:1450::200e", "fd00::5", "::ffff:8.8.8.8"}
	// Few distinct amounts, so that many flows tie.
	amounts := []uint64{0, 100, 100, 5000}
	now := time.Now()
	for range 300 {
		src, dst := addrs[r.IntN(len(addrs))], addrs[r.IntN(len(addrs))]
		var srcMAC, dstMAC []byte
		if m := macs[r.IntN(len(macs))]; m != nil {
			srcMAC = m
		}
		if m := macs[r.IntN(len(macs))]; m != nil && r.IntN(2) == 0 {
			dstMAC = m
		}
		_, err := db.Exec(`INSERT INTO flows VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			srcMAC, dstMAC, netip.MustParseAddr(src).AsSlice(), netip.MustParseAddr(dst).AsSlice(),
			[]uint8{6, 17}[r.IntN(2)], []uint16{443, 53, 80}[r.IntN(3)],
			amounts[r.IntN(len(amounts))], r.IntN(3), amounts[r.IntN(len(amounts))], r.IntN(3),
			now.Add(-time.Duration(r.IntN(5))*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
	}

	ip, _ := common.ParseIPAddress("192.168.1.23")
	mac, _ := common.ParseMACAddress("a4:83:e7:1c:2f:90")
	matcher := networks.NewMatcher([]networks.Network{
		{ID: 1, Prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24"), netip.MustParsePrefix("2001:db8::/32")}},
		{ID: 2, Prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.1.10/32")}},
	})
	home, _ := matcher.Scope(1)
	since := now.Add(-3 * time.Hour)

	filters := map[string]storage.FlowsFilter{
		"everything": {},
		"mac":        {MAC: mac},
		"ip":         {IP: ip},
		"network":    {Network: &home},
		"recent":     {MAC: mac, From: &since},
	}
	for name, filter := range filters {
		records, err := storage.GetFlows(db, filter)
		if err != nil {
			t.Fatal(err)
		}
		oriented := orientFlows(records, filter)

		for _, sort := range []string{"total", "download", "upload", "packets"} {
			for _, asc := range []bool{false, true} {
				for _, page := range [][2]int{{0, 0}, {7, 0}, {7, 14}, {50, 3}, {10, 10000}} {
					want, wantTotal := pageFlows(oriented, FlowsPage{Sort: sort, Asc: asc, Limit: page[0], Offset: page[1]}, hasSubject(filter))
					got, total, err := storage.GetFlowsPage(db, filter, storage.FlowOrder{Sort: sort, Asc: asc, Limit: page[0], Offset: page[1]})
					if err != nil {
						t.Fatal(err)
					}
					what := fmt.Sprintf("%s by %s (asc %v), limit %d offset %d", name, sort, asc, page[0], page[1])
					if total != wantTotal {
						t.Errorf("%s: total %d, want %d", what, total, wantTotal)
					}
					if g, w := describe(orientFlows(got, filter)), describe(want); !reflect.DeepEqual(g, w) {
						t.Errorf("%s:\n got %v\nwant %v", what, g, w)
					}
				}
			}
		}

		peers, err := storage.GetPeerTraffic(db, filter)
		if err != nil {
			t.Fatal(err)
		}
		services, err := storage.GetServiceTraffic(db, filter)
		if err != nil {
			t.Fatal(err)
		}
		summary := summarizeFlows(peers, services, 5, nil, nil)
		if want := goSummary(oriented, 5); !reflect.DeepEqual(summary, want) {
			t.Errorf("%s summary:\n got %+v\nwant %+v", name, summary, want)
		}
	}
}

func describe(flows []orientedFlow) []string {
	result := []string{}
	for _, f := range flows {
		r := f.record
		result = append(result, fmt.Sprintf("%s|%s>%s|%s %d/%d %d/%d", r.SourceMACAddress, r.SourceIPAddress, r.DestinationIPAddress,
			r.DestinationMACAddress, r.IPProto, r.Port, f.download, f.upload))
	}
	return result
}

// goSummary is how summaries were made from every flow before the database
// grouped them.
func goSummary(flows []orientedFlow, top int) FlowSummaryDTO {
	summary := FlowSummaryDTO{Connections: uint64(len(flows))}
	var byPeer, byService []groupTraffic
	peers := map[string]bool{}
	for _, f := range flows {
		summary.Download += f.download
		summary.Upload += f.upload
		summary.Packets += f.packets
		if f.peer.Addr != nil {
			peers[f.peer.Unmap().String()] = true
			byPeer = append(byPeer, groupTraffic{f.peer.Unmap().String(), f.download, f.upload, 1})
		}
		byService = append(byService, groupTraffic{fmt.Sprintf("%d/%d", f.record.IPProto, f.record.Port), f.download, f.upload, 1})
	}
	summary.Peers = uint64(len(peers))
	summary.TopPeers = topGroups(byPeer, top)
	summary.Services = topGroups(byService, top)
	summary.Countries = topGroups(nil, top)
	summary.ASNs = topGroups(nil, top)
	return summary
}
