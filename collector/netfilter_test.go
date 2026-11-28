//go:build linux

package collector

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/netip"
	"strings"
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

func TestCarriedSince(t *testing.T) {
	first := track(conn(1, "192.168.1.23", "140.82.121.4", 50000, 443, 1000, 200)).counters

	if got := first.since(carried{}, false); got.inBytes != 1000 || got.outBytes != 200 {
		t.Errorf("new connection: %+v, want its full counters 1000/200", got)
	}

	later := track(conn(1, "192.168.1.23", "140.82.121.4", 50000, 443, 1500, 300)).counters
	if got := later.since(first, true); got != (carried{inBytes: 500, inPackets: 5, outBytes: 100, outPackets: 1}) {
		t.Errorf("continued connection: %+v, want 500/100 bytes 5/1 packets", got)
	}

	// The ID was reused by a new, smaller connection.
	reused := track(conn(1, "192.168.1.23", "1.1.1.1", 50001, 53, 80, 60)).counters
	if got := reused.since(later, true); got.inBytes != 80 || got.outBytes != 60 {
		t.Errorf("reused ID: %+v, want the new connection's counters 80/60", got)
	}
}

// closedOf returns a set of closed connections.
func closedOf(flows ...conntrack.Flow) closedSet {
	closed := newClosedSet(maxClosedConnections)
	for _, flow := range flows {
		t := track(flow)
		closed.keep(t.id, t.connection)
	}
	return closed
}

// baselineOf returns the baseline after a dump that found flows.
func baselineOf(flows ...conntrack.Flow) baseline {
	return baseline{}.next(trackAll(flows), nil)
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
	previous := baselineOf(idle)
	current := trackAll([]conntrack.Flow{
		// Two connections to the same service form one flow.
		conn(1, "192.168.1.23", "140.82.121.4", 50000, 443, 1000, 100),
		conn(2, "192.168.1.23", "140.82.121.4", 50001, 443, 3000, 300),
		idle,
	})
	closed := closedOf(conn(4, "192.168.1.23", "1.1.1.1", 50020, 53, 90, 70))

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
	previous := baseline{}
	var stored uint64

	for _, in := range []uint64{100, 250, 400} {
		current := trackAll([]conntrack.Flow{conn(7, "192.168.1.23", "23.246.18.164", 50100, 443, in, 0)})

		for _, snapshot := range buildSnapshots(closedOf(), current, previous, noMAC, nil) {
			stored += snapshot.inBytes
		}
		previous = previous.next(current, nil)
	}

	if stored != 400 {
		t.Errorf("stored %d bytes over three dumps, want the connection's 400", stored)
	}
}

func TestBuildSnapshotsLeavesOutIgnoredFlows(t *testing.T) {
	current := trackAll([]conntrack.Flow{
		conn(1, "192.168.1.23", "192.168.1.5", 50000, 445, 9000, 900),
		conn(2, "192.168.1.23", "140.82.121.4", 50001, 443, 3000, 300),
	})
	ignore := func(src, dst netip.Addr) bool {
		return src.IsPrivate() && dst.IsPrivate()
	}

	snapshots := buildSnapshots(closedOf(), current, baseline{}, noMAC, ignore)

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

	current := trackAll([]conntrack.Flow{
		// Someone on the internet connects to port 8443 of the router's WAN
		// address, which is forwarded to port 443 of a server at home.
		natConn(1, "203.0.113.7", "198.51.100.2", 51000, 8443, "192.168.1.10", 443, 5000, 800),
		// A DNS query to a public resolver, redirected to the router's own,
		// which is no neighbor of its own.
		natConn(2, "192.168.1.23", "8.8.8.8", 50000, 53, "192.168.1.1", 53, 90, 70),
		// An ordinary connection out through the router's NAT, whose replies
		// come from where it went.
		natConn(3, "192.168.1.23", "140.82.121.4", 50001, 443, "140.82.121.4", 443, 3000, 300),
	})

	snapshots := buildSnapshots(closedOf(), current, baseline{}, macOf, nil)

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

func (k *fakeKernel) dump() ([]tracked, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.err != nil {
		return nil, k.err
	}
	return trackAll(k.flows), nil
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
	n.accounting = func() (bool, error) { return true, nil }
	return n, kernel
}

func mustRecord(t *testing.T, n *netfilterCollector) {
	t.Helper()
	if _, err := n.record(); err != nil {
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

	b := baselineOf(download)
	b = b.next(nil, nil)
	if _, ok := b.vanished[track(download).id]; !ok {
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
	if _, err := n.record(); err == nil {
		t.Fatal("recording with a failing dump succeeded")
	}
	kernel.err = nil

	broken, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = broken.Close()
	n.db = broken
	if _, err := n.record(); err == nil {
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
			found := false
			for closed := range n.closed.first {
				found = found || closed.id == id
			}
			if !found {
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

func TestRecordBoundsTheClosedConnectionsItKeeps(t *testing.T) {
	db := newTestDB(t)
	n, kernel := newTestCollector(db)
	n.opts.Health = NewHealth()
	n.closed.limit = 3

	kernel.err = errors.New("dump failed")
	for id := range uint32(3) {
		n.destroyed(conn(id, laptopIP.String(), "1.1.1.1", uint16(50000+id), 53, 90, 70))
	}
	if _, err := n.record(); err == nil {
		t.Fatal("recording with a failing dump succeeded")
	}

	// Two more close while the dump fails, and one takes the place of a
	// connection it has already, which is no more of them.
	n.destroyed(conn(3, laptopIP.String(), "1.1.1.1", 50003, 53, 90, 70))
	n.destroyed(conn(4, laptopIP.String(), "1.1.1.1", 50004, 53, 90, 70))
	n.destroyed(conn(0, laptopIP.String(), "1.1.1.1", 50000, 53, 100, 80))
	if len(n.closed.first) != 3 || n.closed.dropped != 2 {
		t.Fatalf("kept %d closed connections and dropped %d, want 3 and 2", len(n.closed.first), n.closed.dropped)
	}

	kernel.err = nil
	mustRecord(t, n)
	if got := n.opts.Health.Status().DroppedConnections; got != 2 {
		t.Errorf("health counts %d dropped connections, want 2", got)
	}
	if got := downloaded(t, db); got != 3*90+100 {
		t.Errorf("recorded %d bytes downloaded, want %d: the kept connections' and the one in place of one", got, 3*90+100)
	}
}

// A busy router closes far more connections in a minute than are kept while
// rounds fail, and every one of them counts.
func TestRecordKeepsEveryConnectionClosedInARound(t *testing.T) {
	db := newTestDB(t)
	n, _ := newTestCollector(db)
	n.opts.Health = NewHealth()

	const closing = 100_000
	for id := range uint32(closing) {
		n.destroyed(conn(id, laptopIP.String(), "1.1.1.1", uint16(id), 53, 1, 1))
	}
	mustRecord(t, n)

	if got := downloaded(t, db); got != closing {
		t.Errorf("recorded %d bytes downloaded, want %d, one for each connection", got, closing)
	}
	if got := n.opts.Health.Status().DroppedConnections; got != 0 {
		t.Errorf("health counts %d dropped connections, want none", got)
	}
}

// The kernel gives a connection that takes the place of one that just ended
// between the same ports the same ID, as when a client reuses its port.
func TestRecordCountsConnectionsWithAReusedID(t *testing.T) {
	db := newTestDB(t)
	n, kernel := newTestCollector(db)

	first := conn(1, laptopIP.String(), "140.82.121.4", 50000, 443, 1000, 100)
	kernel.set(first)
	mustRecord(t, n)

	// It closes, another takes its place and closes too, and a third
	// takes theirs.
	n.destroyed(conn(1, laptopIP.String(), "140.82.121.4", 50000, 443, 1200, 120))
	n.destroyed(conn(1, laptopIP.String(), "140.82.121.4", 50000, 443, 700, 70))
	kernel.set(conn(1, laptopIP.String(), "140.82.121.4", 50000, 443, 1500, 150))
	mustRecord(t, n)

	if got := downloaded(t, db); got != 1200+700+1500 {
		t.Errorf("recorded %d bytes downloaded, want %d: all three connections'", got, 1200+700+1500)
	}

	// The third goes on, and only its new traffic counts.
	kernel.set(conn(1, laptopIP.String(), "140.82.121.4", 50000, 443, 1800, 180))
	mustRecord(t, n)
	if got := downloaded(t, db); got != 1200+700+1800 {
		t.Errorf("recorded %d bytes downloaded, want %d", got, 1200+700+1800)
	}
}

// Connections that share an ID by chance are told apart by their ports, and
// those between the same ports by when conntrack started tracking them, where
// it records that.
func TestRecordTellsConnectionsWithTheSameIDApart(t *testing.T) {
	db := newTestDB(t)
	n, kernel := newTestCollector(db)

	started := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	stamped := func(flow conntrack.Flow, start time.Time) conntrack.Flow {
		flow.Timestamp.Start = start
		return flow
	}

	web := conn(1, laptopIP.String(), "140.82.121.4", 50000, 443, 1000, 100)
	dns := conn(1, laptopIP.String(), "1.1.1.1", 50001, 53, 90, 70)
	video := stamped(conn(2, laptopIP.String(), "23.246.18.164", 50002, 443, 5000, 500), started)
	kernel.set(web, dns, video)
	mustRecord(t, n)
	if got := downloaded(t, db); got != 1000+90+5000 {
		t.Fatalf("recorded %d bytes downloaded, want %d", got, 1000+90+5000)
	}

	// The video's connection closes, and another between the same ports
	// replaces it, larger already. Their start times tell them apart.
	n.destroyed(stamped(conn(2, laptopIP.String(), "23.246.18.164", 50002, 443, 5200, 520), started))
	kernel.set(web, dns, stamped(conn(2, laptopIP.String(), "23.246.18.164", 50002, 443, 6000, 600), started.Add(time.Minute)))
	mustRecord(t, n)
	if got := downloaded(t, db); got != 1000+90+5200+6000 {
		t.Errorf("recorded %d bytes downloaded, want %d", got, 1000+90+5200+6000)
	}
}

// A connection being destroyed can still be in the dump, while the event
// saying so came already.
func TestRecordLeavesOutDyingConnections(t *testing.T) {
	db := newTestDB(t)
	n, kernel := newTestCollector(db)

	kernel.set(conn(1, laptopIP.String(), "140.82.121.4", 50000, 443, 1000, 100))
	mustRecord(t, n)

	final := conn(1, laptopIP.String(), "140.82.121.4", 50000, 443, 1200, 120)
	n.destroyed(final)
	final.Status = conntrack.StatusDying | conntrack.StatusAssured
	kernel.set(final)
	mustRecord(t, n)
	kernel.set()
	mustRecord(t, n)

	if got := downloaded(t, db); got != 1200 {
		t.Errorf("recorded %d bytes downloaded, want the connection's 1200", got)
	}
}

func TestRecordBoundsTheTrafficItCouldNotWrite(t *testing.T) {
	db := newTestDB(t)
	n, kernel := newTestCollector(db)
	n.opts.Health = NewHealth()
	n.pendingLimit = 2

	broken, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = broken.Close()
	n.db = broken

	// Two flows fail to be written, then a third comes along, which there
	// is no room for, and more traffic of the first.
	kernel.set(
		conn(1, laptopIP.String(), "140.82.121.4", 50000, 443, 1000, 100),
		conn(2, laptopIP.String(), "1.1.1.1", 50001, 53, 90, 70),
	)
	if _, err := n.record(); err == nil {
		t.Fatal("recording to a closed database succeeded")
	}
	kernel.set(
		conn(1, laptopIP.String(), "140.82.121.4", 50000, 443, 1500, 150),
		conn(3, laptopIP.String(), "23.246.18.164", 50002, 443, 5000, 500),
	)
	if _, err := n.record(); err == nil {
		t.Fatal("recording to a closed database succeeded")
	}
	if got := n.opts.Health.Status().DroppedFlows; got != 1 {
		t.Errorf("health counts %d dropped flows, want 1", got)
	}

	n.db = db
	kernel.set()
	mustRecord(t, n)
	if got := downloaded(t, db); got != 1500+90 {
		t.Errorf("recorded %d bytes downloaded, want %d: the two flows it kept", got, 1500+90)
	}
	if n.pending != nil {
		t.Errorf("still holds %d flows after writing them", len(n.pending))
	}
}

func TestRoundReportsToHealth(t *testing.T) {
	db := newTestDB(t)
	n, kernel := newTestCollector(db)
	n.opts.Health = NewHealth()

	kernel.set(conn(1, laptopIP.String(), "140.82.121.4", 50000, 443, 1000, 100))
	n.round()
	if s := n.opts.Health.Status(); s.Rounds != 1 || s.Rows != 1 || s.LastRecorded.IsZero() || s.Error != "" {
		t.Errorf("after a round: %+v", s)
	}

	kernel.err = errors.New("dump failed")
	n.round()
	if s := n.opts.Health.Status(); s.FailedRounds != 1 || s.Error != "dump failed" {
		t.Errorf("after a failed round: %+v", s)
	}
}

func TestRoundWarnsWhenConntrackDoesNotCount(t *testing.T) {
	n, _ := newTestCollector(newTestDB(t))
	n.opts.Health = NewHealth()

	n.accounting = func() (bool, error) { return false, nil }
	n.round()
	if w := n.opts.Health.Status().Warnings; len(w) != 1 || !strings.Contains(w[0], "nf_conntrack_acct=1") {
		t.Errorf("warnings with accounting off = %q, want how to turn it on", w)
	}

	n.accounting = func() (bool, error) { return true, nil }
	n.round()
	if w := n.opts.Health.Status().Warnings; len(w) != 0 {
		t.Errorf("warnings with accounting on = %q, want none", w)
	}

	// Without the setting, as in some containers, it can't tell.
	n.accounting = func() (bool, error) { return false, errors.New("no such file") }
	n.round()
	if w := n.opts.Health.Status().Warnings; len(w) != 0 {
		t.Errorf("warnings without the setting = %q, want none", w)
	}
}

func TestRecordAddsUpTheTrafficOfDevices(t *testing.T) {
	n, kernel := newTestCollector(newTestDB(t))
	n.opts.Totals = NewTotals()

	kernel.set(
		conn(1, laptopIP.String(), "140.82.121.4", 50000, 443, 1000, 100),
		conn(2, laptopIP.String(), phoneIP.String(), 50001, 8009, 300, 4000),
	)
	mustRecord(t, n)

	devices := n.opts.Totals.Devices()
	if got := devices[net.HardwareAddr(laptopMAC[:]).String()]; got != (DeviceTotal{Received: 1300, Sent: 4100}) {
		t.Errorf("laptop = %+v, want 1300 received and 4100 sent", got)
	}
	if got := devices[net.HardwareAddr(phoneMAC[:]).String()]; got != (DeviceTotal{Received: 4000, Sent: 300}) {
		t.Errorf("phone = %+v, want 4000 received and 300 sent", got)
	}
}

// A connection's event can come only after a dump that found the connection
// that took its place: that one's traffic isn't counted twice.
func TestRecordDoesNotCountAReplacementTwiceWhenTheEventComesLate(t *testing.T) {
	db := newTestDB(t)
	n, kernel := newTestCollector(db)

	kernel.set(conn(1, laptopIP.String(), "140.82.121.4", 50000, 443, 100, 10))
	mustRecord(t, n)

	// It closes, and another takes its place, which the next dump finds
	// before the event about the first comes.
	kernel.set(conn(1, laptopIP.String(), "140.82.121.4", 50000, 443, 3000, 300))
	mustRecord(t, n)
	n.destroyed(conn(1, laptopIP.String(), "140.82.121.4", 50000, 443, 150, 15))
	kernel.set(conn(1, laptopIP.String(), "140.82.121.4", 50000, 443, 3500, 350))
	mustRecord(t, n)

	// What the first carried after the first dump is lost, as the second
	// counted from the first's counters.
	if got := downloaded(t, db); got != 3500 {
		t.Errorf("recorded %d bytes downloaded, want 3500", got)
	}
}

func TestUniqueLeavesOutRepeatedConnections(t *testing.T) {
	web := track(conn(1, laptopIP.String(), "140.82.121.4", 50000, 443, 100, 10))
	dns := track(conn(2, laptopIP.String(), "1.1.1.1", 50001, 53, 90, 70))
	// The same ID by chance, between other ports.
	other := track(conn(1, laptopIP.String(), "23.246.18.164", 50002, 443, 5000, 500))

	got, repeated := unique([]tracked{web, dns, web, other})
	if repeated != 1 || len(got) != 3 || got[0].id != web.id || got[1].id != dns.id || got[2].id != other.id {
		t.Errorf("unique = %d connections, %d repeated, want web, dns and other, and 1", len(got), repeated)
	}
}
