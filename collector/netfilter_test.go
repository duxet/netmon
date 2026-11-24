//go:build linux

package collector

import (
	"net"
	"net/netip"
	"testing"

	"github.com/duxet/netmon/common"
	"github.com/duxet/netmon/storage"
	"github.com/ti-mo/conntrack"
)

func conn(id uint32, src, dst string, sport, dport uint16, in, out uint64) conntrack.Flow {
	return conntrack.Flow{
		ID: id,
		TupleOrig: conntrack.Tuple{
			IP: conntrack.IPTuple{
				SourceAddress:      netip.MustParseAddr(src),
				DestinationAddress: netip.MustParseAddr(dst),
			},
			Proto: conntrack.ProtoTuple{Protocol: 6, SourcePort: sport, DestinationPort: dport},
		},
		CountersReply: conntrack.Counter{Bytes: in, Packets: in / 100},
		CountersOrig:  conntrack.Counter{Bytes: out, Packets: out / 100},
	}
}

func noMAC(netip.Addr) *common.MACAddress { return nil }

func TestFlowDelta(t *testing.T) {
	first := conn(1, "192.168.1.23", "140.82.121.4", 50000, 443, 1000, 200)

	if in, _, out, _ := flowDelta(first, conntrack.Flow{}, false); in != 1000 || out != 200 {
		t.Errorf("new connection: in %d out %d, want its full counters 1000/200", in, out)
	}

	later := conn(1, "192.168.1.23", "140.82.121.4", 50000, 443, 1500, 300)
	if in, inP, out, outP := flowDelta(later, first, true); in != 500 || out != 100 || inP != 5 || outP != 1 {
		t.Errorf("continued connection: %d/%d bytes %d/%d packets, want 500/100 bytes 5/1 packets", in, out, inP, outP)
	}

	// The ID was reused by a new, smaller connection.
	reused := conn(1, "192.168.1.23", "1.1.1.1", 50001, 53, 80, 60)
	if in, _, out, _ := flowDelta(reused, later, true); in != 80 || out != 60 {
		t.Errorf("reused ID: in %d out %d, want the new connection's counters 80/60", in, out)
	}
}

func TestBuildSnapshotsGroupsAndSkipsIdleFlows(t *testing.T) {
	lookups := map[netip.Addr]int{}
	macOf := func(addr netip.Addr) *common.MACAddress {
		lookups[addr]++
		if addr.IsPrivate() {
			return &common.MACAddress{HardwareAddr: net.HardwareAddr{0xa4, 0x83, 0xe7, 0x1c, 0x2f, 0x90}}
		}
		return nil
	}

	idle := conn(3, "192.168.1.23", "17.253.53.207", 50010, 5223, 400, 40)
	previous := map[uint32]conntrack.Flow{idle.ID: idle}
	current := []conntrack.Flow{
		// Two connections to the same service form one flow.
		conn(1, "192.168.1.23", "140.82.121.4", 50000, 443, 1000, 100),
		conn(2, "192.168.1.23", "140.82.121.4", 50001, 443, 3000, 300),
		idle,
	}
	closed := map[uint32]conntrack.Flow{
		4: conn(4, "192.168.1.23", "1.1.1.1", 50020, 53, 90, 70),
	}

	snapshots := buildSnapshots(closed, current, previous, macOf)

	if len(snapshots) != 2 {
		t.Fatalf("got %d snapshots, want 2 (HTTPS and DNS; the idle connection is skipped): %+v", len(snapshots), snapshots)
	}

	https := snapshots[FlowSnapshotKey{
		srcIP: netip.MustParseAddr("192.168.1.23"),
		dstIP: netip.MustParseAddr("140.82.121.4"),
		proto: 6,
		port:  443,
	}]
	if https == nil || https.inBytes != 4000 || https.outBytes != 400 {
		t.Fatalf("HTTPS snapshot = %+v, want both connections summed to 4000/400", https)
	}
	if https.srcMAC == nil || https.dstMAC != nil {
		t.Errorf("HTTPS MACs = %v -> %v, want the client's MAC and none for the remote host", https.srcMAC, https.dstMAC)
	}

	if n := lookups[netip.MustParseAddr("140.82.121.4")]; n != 1 {
		t.Errorf("remote MAC looked up %d times, want once per flow", n)
	}
}

// Over several dumps, a long-lived connection must add up to its real total
// rather than having its cumulative counters stored again every minute.
func TestRepeatedDumpsCountTrafficOnce(t *testing.T) {
	previous := map[uint32]conntrack.Flow{}
	var stored uint64

	for _, in := range []uint64{100, 250, 400} {
		current := []conntrack.Flow{conn(7, "192.168.1.23", "23.246.18.164", 50100, 443, in, 0)}

		for _, snapshot := range buildSnapshots(nil, current, previous, noMAC) {
			stored += snapshot.inBytes
		}

		// What dumpFlows does after a successful insert.
		previous = map[uint32]conntrack.Flow{}
		for _, flow := range current {
			previous[flow.ID] = flow
		}
	}

	if stored != 400 {
		t.Errorf("stored %d bytes over three dumps, want the connection's 400", stored)
	}
}

func TestInsertSnapshots(t *testing.T) {
	db := newTestDB(t)

	mac := &common.MACAddress{HardwareAddr: net.HardwareAddr{0xa4, 0x83, 0xe7, 0x1c, 0x2f, 0x90}}
	snapshots := map[FlowSnapshotKey]*FlowSnapshot{
		{srcIP: netip.MustParseAddr("192.168.1.23"), dstIP: netip.MustParseAddr("140.82.121.4"), proto: 6, port: 443}: {
			srcMAC: mac, inBytes: 4000, inPackets: 40, outBytes: 400, outPackets: 4,
		},
		{srcIP: netip.MustParseAddr("2001:db8::10"), dstIP: netip.MustParseAddr("2001:db8:1::53"), proto: 17, port: 53}: {
			inBytes: 90, inPackets: 1, outBytes: 70, outPackets: 1,
		},
	}

	if err := insertSnapshots(db, snapshots); err != nil {
		t.Fatal(err)
	}
	if err := insertSnapshots(db, nil); err != nil {
		t.Fatalf("inserting nothing: %v", err)
	}

	flows := getFlows(t, db, storage.FlowsFilter{})
	if len(flows) != 2 {
		t.Fatalf("got %d flows, want 2", len(flows))
	}
	if flows[0].InBytes != 4000 || flows[0].SourceMACAddress.String() != mac.String() {
		t.Errorf("first flow = %+v, want the HTTPS flow with the client's MAC", flows[0])
	}
}
