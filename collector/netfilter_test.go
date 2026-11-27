//go:build linux

package collector

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/duxet/netmon/common"
	"github.com/duxet/netmon/live"
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

// natConn is a connection whose destination conntrack translated to to and
// toPort, where the replies then come from.
func natConn(id uint32, src, dst string, sport, dport uint16, to string, toPort uint16, in, out uint64) conntrack.Flow {
	flow := conn(id, src, dst, sport, dport, in, out)
	flow.TupleReply = conntrack.Tuple{
		IP: conntrack.IPTuple{
			SourceAddress:      netip.MustParseAddr(to),
			DestinationAddress: netip.MustParseAddr(src),
		},
		Proto: conntrack.ProtoTuple{Protocol: 6, SourcePort: toPort, DestinationPort: sport},
	}
	return flow
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

	snapshots := buildSnapshots(closed, current, previous, macOf, nil)

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

		for _, snapshot := range buildSnapshots(nil, current, previous, noMAC, nil) {
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

func TestBuildSnapshotsLeavesOutIgnoredFlows(t *testing.T) {
	current := []conntrack.Flow{
		conn(1, "192.168.1.23", "192.168.1.5", 50000, 445, 9000, 900),
		conn(2, "192.168.1.23", "140.82.121.4", 50001, 443, 3000, 300),
	}
	ignore := func(src, dst netip.Addr) bool {
		return src.IsPrivate() && dst.IsPrivate()
	}

	snapshots := buildSnapshots(nil, current, map[uint32]conntrack.Flow{}, noMAC, ignore)

	if len(snapshots) != 1 {
		t.Fatalf("got %d snapshots, want only the one to the internet: %+v", len(snapshots), snapshots)
	}
	for key := range snapshots {
		if key.dstIP != netip.MustParseAddr("140.82.121.4") {
			t.Errorf("snapshot of %s -> %s, want the one to the internet", key.srcIP, key.dstIP)
		}
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

func TestMACLookup(t *testing.T) {
	macOf := macLookup(map[netip.Addr]live.MAC{laptopIP: laptopMAC})

	if mac := macOf(laptopIP); mac == nil || mac.String() != net.HardwareAddr(laptopMAC[:]).String() {
		t.Errorf("laptop's MAC = %v, want %s", mac, net.HardwareAddr(laptopMAC[:]))
	}
	if mac := macOf(netip.AddrFrom16(laptopIP.As16())); mac == nil {
		t.Error("laptop's IPv4-mapped address has no MAC, want the laptop's")
	}
	if mac := macOf(netip.MustParseAddr("140.82.121.4")); mac != nil {
		t.Errorf("remote host's MAC = %v, want none", mac)
	}
	if mac := macLookup(nil)(laptopIP); mac != nil {
		t.Errorf("MAC without a neighbor table = %v, want none", mac)
	}
}

func TestBuildSnapshotsFollowsPortForwards(t *testing.T) {
	serverMAC := &common.MACAddress{HardwareAddr: net.HardwareAddr{0x00, 0x11, 0x32, 0x9b, 0xc4, 0x10}}
	macOf := func(addr netip.Addr) *common.MACAddress {
		if addr == netip.MustParseAddr("192.168.1.10") {
			return serverMAC
		}
		return nil
	}

	current := []conntrack.Flow{
		// Someone on the internet connects to port 8443 of the router's WAN
		// address, which is forwarded to port 443 of a server at home.
		natConn(1, "203.0.113.7", "198.51.100.2", 51000, 8443, "192.168.1.10", 443, 5000, 800),
		// A DNS query to a public resolver, redirected to the router's own,
		// which is no neighbor of its own.
		natConn(2, "192.168.1.23", "8.8.8.8", 50000, 53, "192.168.1.1", 53, 90, 70),
		// An ordinary connection out through the router's NAT, whose replies
		// come from where it went.
		natConn(3, "192.168.1.23", "140.82.121.4", 50001, 443, "140.82.121.4", 443, 3000, 300),
	}

	snapshots := buildSnapshots(nil, current, map[uint32]conntrack.Flow{}, macOf, nil)

	want := map[FlowSnapshotKey]bool{
		{srcIP: netip.MustParseAddr("203.0.113.7"), dstIP: netip.MustParseAddr("192.168.1.10"), proto: 6, port: 443}:  true,
		{srcIP: netip.MustParseAddr("192.168.1.23"), dstIP: netip.MustParseAddr("8.8.8.8"), proto: 6, port: 53}:       true,
		{srcIP: netip.MustParseAddr("192.168.1.23"), dstIP: netip.MustParseAddr("140.82.121.4"), proto: 6, port: 443}: true,
	}
	if len(snapshots) != len(want) {
		t.Fatalf("got %d snapshots, want %d: %+v", len(snapshots), len(want), snapshots)
	}
	for key, snapshot := range snapshots {
		if !want[key] {
			t.Errorf("unexpected snapshot of %s -> %s port %d", key.srcIP, key.dstIP, key.port)
		}
		if key.dstIP == netip.MustParseAddr("192.168.1.10") && snapshot.dstMAC != serverMAC {
			t.Errorf("forwarded connection's destination MAC = %v, want the server's", snapshot.dstMAC)
		}
	}
}

// fakeKernel holds what a test says conntrack and the neighbor table know.
type fakeKernel struct {
	mu            sync.Mutex
	flows         []conntrack.Flow
	macs          map[netip.Addr]live.MAC
	err           error
	neighborReads int
}

func (k *fakeKernel) set(flows ...conntrack.Flow) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.flows = flows
}

func (k *fakeKernel) dump() ([]conntrack.Flow, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.err != nil {
		return nil, k.err
	}
	return append([]conntrack.Flow(nil), k.flows...), nil
}

func (k *fakeKernel) neighbors() (map[netip.Addr]live.MAC, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.neighborReads++
	return k.macs, nil
}

func newTestCollector(db *sql.DB) (*netfilterCollector, *fakeKernel) {
	n := newNetfilterCollector(db, live.NewMeter(), Options{})
	kernel := &fakeKernel{macs: localHosts}
	n.dump, n.neighbors = kernel.dump, kernel.neighbors
	n.listen = func() (*destroyListener, error) { return nil, errors.New("no conntrack in tests") }
	return n, kernel
}

func mustRecord(t *testing.T, n *netfilterCollector) {
	t.Helper()
	if err := n.record(); err != nil {
		t.Fatal(err)
	}
}

// downloaded returns how much the flows in the database downloaded in all.
func downloaded(t *testing.T, db *sql.DB) uint64 {
	t.Helper()
	var total uint64
	for _, flow := range getFlows(t, db, storage.FlowsFilter{}) {
		total += flow.InBytes
	}
	return total
}

func TestRecordReadsTheNeighborTableOncePerRound(t *testing.T) {
	db := newTestDB(t)
	n, kernel := newTestCollector(db)

	var flows []conntrack.Flow
	for i := range 50 {
		flows = append(flows, conn(uint32(i), laptopIP.String(), "140.82.121.4", uint16(50000+i), uint16(1000+i), 100, 10))
	}
	kernel.set(flows...)
	mustRecord(t, n)

	if kernel.neighborReads != 1 {
		t.Errorf("read the neighbor table %d times for 50 flows, want once", kernel.neighborReads)
	}
	for _, flow := range getFlows(t, db, storage.FlowsFilter{}) {
		if flow.SourceMACAddress.String() != net.HardwareAddr(laptopMAC[:]).String() {
			t.Fatalf("flow from the laptop has MAC %q, want the laptop's", flow.SourceMACAddress)
		}
	}
}

// A connection that closes just before a dump, whose event is only read after
// it, must not have its traffic before the dump before recorded again.
func TestRecordCountsConnectionsWhoseEventComesLate(t *testing.T) {
	db := newTestDB(t)
	n, kernel := newTestCollector(db)

	kernel.set(conn(1, laptopIP.String(), "140.82.121.4", 50000, 443, 1000, 100))
	mustRecord(t, n)

	// The download is over, so the next dump misses it, but the event saying
	// so only comes after that dump.
	kernel.set()
	mustRecord(t, n)
	n.destroyed(conn(1, laptopIP.String(), "140.82.121.4", 50000, 443, 1500, 150))
	mustRecord(t, n)

	if got := downloaded(t, db); got != 1500 {
		t.Errorf("recorded %d bytes downloaded, want the connection's 1500", got)
	}
	if len(n.baseline.vanished) != 0 {
		t.Errorf("baseline still holds %d vanished connections, want none once their event came", len(n.baseline.vanished))
	}
}

func TestBaselineForgetsConnectionsWhoseEventWasLost(t *testing.T) {
	download := conn(1, laptopIP.String(), "140.82.121.4", 50000, 443, 1000, 100)

	b := baseline{}.next([]conntrack.Flow{download}, nil)
	b = b.next(nil, nil)
	if _, ok := b.vanished[download.ID]; !ok {
		t.Fatal("a connection missing from the dump isn't kept until its event comes")
	}

	b = b.next(nil, nil)
	if len(b.vanished) != 0 || len(b.open) != 0 {
		t.Errorf("baseline = %+v, want it empty a round after the event should have come", b)
	}
}

func TestRecordKeepsTrafficItCouldNotWrite(t *testing.T) {
	db := newTestDB(t)
	n, kernel := newTestCollector(db)

	kernel.set(conn(1, laptopIP.String(), "140.82.121.4", 50000, 443, 1000, 100))
	n.destroyed(conn(2, laptopIP.String(), "1.1.1.1", 50001, 53, 90, 70))

	kernel.err = errors.New("dump failed")
	if err := n.record(); err == nil {
		t.Fatal("recording with a failing dump succeeded")
	}
	kernel.err = nil

	broken, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = broken.Close()
	n.db = broken
	if err := n.record(); err == nil {
		t.Fatal("recording to a closed database succeeded")
	}
	n.db = db

	kernel.set(conn(1, laptopIP.String(), "140.82.121.4", 50000, 443, 1600, 160))
	mustRecord(t, n)

	if got := downloaded(t, db); got != 1600+90 {
		t.Errorf("recorded %d bytes downloaded, want %d: all of the open and the closed connection's", got, 1600+90)
	}
}

func destroyEvent(flow conntrack.Flow) conntrack.Event {
	return conntrack.Event{Flow: &flow}
}

func TestReceiveHandlesEventsBeforeAnError(t *testing.T) {
	// Which of the events and the error is taken first is up to select.
	for range 50 {
		events := make(chan conntrack.Event, 2)
		events <- destroyEvent(conn(1, laptopIP.String(), "1.1.1.1", 50000, 53, 90, 70))
		events <- destroyEvent(conn(2, laptopIP.String(), "1.1.1.1", 50001, 53, 90, 70))
		errs := make(chan error, 1)
		errs <- errors.New("no buffer space available")

		closings := 0
		l := &destroyListener{events: events, errs: errs, closeConn: func() error { closings++; return nil }}

		var got []uint32
		err := l.receive(context.Background(), func(flow conntrack.Flow) { got = append(got, flow.ID) })

		if err == nil {
			t.Fatal("receive returned no error, want the worker's")
		}
		if len(got) != 2 {
			t.Fatalf("got the connections %v, want both that closed before the error", got)
		}
		if closings != 1 {
			t.Fatalf("closed the socket %d times, want once", closings)
		}
	}
}

// Closing conntrack's socket waits for its worker, which can be waiting to
// hand over an event that nobody takes any more.
func TestStopDoesNotWaitForeverOnTheWorker(t *testing.T) {
	events := make(chan conntrack.Event)
	quit := make(chan struct{})
	var worker sync.WaitGroup
	worker.Add(1)
	go func() {
		defer worker.Done()
		for {
			events <- destroyEvent(conn(1, laptopIP.String(), "1.1.1.1", 50000, 53, 90, 70))
			select {
			case <-quit:
				return
			default:
			}
		}
	}()

	l := &destroyListener{events: events, errs: make(chan error), closeConn: func() error {
		close(quit)
		worker.Wait()
		return nil
	}}

	stopped := make(chan struct{})
	go func() {
		l.stop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("stop is still waiting for the worker")
	}
}

// waitForClosed waits until n holds the connections destroyed with the given IDs.
func waitForClosed(t *testing.T, n *netfilterCollector, ids ...uint32) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		n.mu.Lock()
		missing := 0
		for _, id := range ids {
			if _, ok := n.closed[id]; !ok {
				missing++
			}
		}
		n.mu.Unlock()

		if missing == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of the connections %v closing were never received", missing, ids)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestListenUntilDoneListensAgainAfterAnError(t *testing.T) {
	n, _ := newTestCollector(nil)

	firstEvents := make(chan conntrack.Event, 1)
	firstEvents <- destroyEvent(conn(1, laptopIP.String(), "1.1.1.1", 50000, 53, 90, 70))
	firstErrs := make(chan error, 1)
	firstErrs <- errors.New("no buffer space available")
	first := &destroyListener{events: firstEvents, errs: firstErrs, closeConn: func() error { return nil }}

	second := make(chan conntrack.Event, 1)
	second <- destroyEvent(conn(2, laptopIP.String(), "1.1.1.1", 50001, 53, 90, 70))
	listens := 0
	n.listen = func() (*destroyListener, error) {
		listens++
		return &destroyListener{events: second, errs: make(chan error), closeConn: func() error { return nil }}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		n.listenUntilDone(ctx, first)
	}()

	waitForClosed(t, n, 1, 2)
	cancel()
	<-done

	if listens != 1 {
		t.Errorf("listened again %d times, want once", listens)
	}
}

func TestShutdownRecordsTheLastRound(t *testing.T) {
	db := newTestDB(t)
	n, _ := newTestCollector(db)
	flushed := make(chan struct{}, 1)
	n.opts.OnFlush = func(time.Time) {
		select {
		case flushed <- struct{}{}:
		default:
		}
	}

	events := make(chan conntrack.Event, 1)
	coll, err := n.start(&destroyListener{events: events, errs: make(chan error), closeConn: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}

	// A connection that opened and closed since the last round.
	events <- destroyEvent(conn(1, laptopIP.String(), "1.1.1.1", 50000, 53, 90, 70))
	waitForClosed(t, n, 1)
	coll.Shutdown()

	if got := downloaded(t, db); got != 90 {
		t.Errorf("recorded %d bytes downloaded after shutting down, want the closed connection's 90", got)
	}
	select {
	case <-flushed:
	default:
		t.Error("OnFlush wasn't called for the last round")
	}
}
