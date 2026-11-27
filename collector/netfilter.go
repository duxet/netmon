//go:build linux

package collector

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/duxet/netmon/common"
	"github.com/duxet/netmon/live"
	"github.com/go-co-op/gocron/v2"
	"github.com/ti-mo/conntrack"
	"github.com/ti-mo/netfilter"
	"log"
	"maps"
	"net"
	"net/netip"
	"sync"
	"time"
)

// FlowSnapshotKey groups connections into flows. It holds addresses by
// value so that connections with the same endpoints compare equal.
type FlowSnapshotKey struct {
	srcIP netip.Addr
	dstIP netip.Addr
	proto uint8
	port  uint16
}

type FlowSnapshot struct {
	srcMAC     *common.MACAddress
	dstMAC     *common.MACAddress
	inBytes    uint64
	inPackets  uint64
	outBytes   uint64
	outPackets uint64
}

const (
	// eventReadBuffer is the size of the socket buffer in which the kernel
	// queues the events about closed connections until netmon reads them.
	// Events that don't fit are lost, and with them the last traffic of
	// their connections.
	eventReadBuffer = 2 << 20

	// eventQueue is how many events can wait between being received and
	// being recorded, for bursts of connections closing.
	eventQueue = 1024

	// minListenRetry and maxListenRetry bound how long to wait before trying
	// to listen for events again after it failed.
	minListenRetry = time.Second
	maxListenRetry = time.Minute

	// listenFailureReport is how often failures to receive events are
	// reported at most: they tend to come in bursts.
	listenFailureReport = time.Minute
)

// macLookup returns a function that finds the MAC address of a host of the
// local network in macs, the neighbor table, and returns nil for addresses
// that aren't in it.
func macLookup(macs map[netip.Addr]live.MAC) func(netip.Addr) *common.MACAddress {
	return func(addr netip.Addr) *common.MACAddress {
		mac, ok := macs[addr.Unmap()]
		if !ok {
			return nil
		}
		return &common.MACAddress{HardwareAddr: net.HardwareAddr(mac[:])}
	}
}

// destination returns the address and port a connection goes to. That is
// where its originator sent it, unless conntrack translated the destination
// to another host of the local network, as for a port forwarded to a server
// at home: the traffic is then that host's. local tells which addresses
// belong to such hosts. Translations to the machine netmon runs on, such as
// redirecting DNS queries to the router's own resolver, keep the address the
// originator sent to, as a machine isn't in its own neighbor table.
func destination(flow conntrack.Flow, local func(netip.Addr) bool) (netip.Addr, uint16) {
	dst, port := flow.TupleOrig.IP.DestinationAddress, flow.TupleOrig.Proto.DestinationPort

	// Replies come from where the connection really went: without
	// destination NAT, that is where it was sent to.
	to, toPort := flow.TupleReply.IP.SourceAddress, flow.TupleReply.Proto.SourcePort
	if to.IsValid() && (to != dst || toPort != port) && local(to) {
		return to, toPort
	}

	return dst, port
}

// flowDelta returns the traffic a connection carried since previous, its
// state at the last recorded dump. Conntrack counters are cumulative.
func flowDelta(flow conntrack.Flow, previous conntrack.Flow, seen bool) (inBytes, inPackets, outBytes, outPackets uint64) {
	inBytes, inPackets = flow.CountersReply.Bytes, flow.CountersReply.Packets
	outBytes, outPackets = flow.CountersOrig.Bytes, flow.CountersOrig.Packets

	if !seen {
		return
	}

	// Smaller counters mean the ID now belongs to a new connection, whose
	// traffic all happened since the last dump.
	if inBytes < previous.CountersReply.Bytes || inPackets < previous.CountersReply.Packets ||
		outBytes < previous.CountersOrig.Bytes || outPackets < previous.CountersOrig.Packets {
		return
	}

	return inBytes - previous.CountersReply.Bytes,
		inPackets - previous.CountersReply.Packets,
		outBytes - previous.CountersOrig.Bytes,
		outPackets - previous.CountersOrig.Packets
}

// buildSnapshots sums the traffic of closed and still open connections since
// the last dump into one snapshot per flow. Flows without new traffic are
// left out, as are those ignore, if set, says to ignore.
func buildSnapshots(
	closed map[uint32]conntrack.Flow,
	current []conntrack.Flow,
	previous map[uint32]conntrack.Flow,
	macOf func(netip.Addr) *common.MACAddress,
	ignore func(src, dst netip.Addr) bool,
) map[FlowSnapshotKey]*FlowSnapshot {
	allFlows := make(map[uint32]conntrack.Flow, len(closed)+len(current))
	for id, flow := range closed {
		allFlows[id] = flow
	}
	for _, flow := range current {
		allFlows[flow.ID] = flow
	}

	local := func(addr netip.Addr) bool { return macOf(addr) != nil }
	snapshots := map[FlowSnapshotKey]*FlowSnapshot{}

	for _, flow := range allFlows {
		old, seen := previous[flow.ID]
		inBytes, inPackets, outBytes, outPackets := flowDelta(flow, old, seen)
		if inBytes == 0 && inPackets == 0 && outBytes == 0 && outPackets == 0 {
			continue
		}

		dst, port := destination(flow, local)
		key := FlowSnapshotKey{
			srcIP: flow.TupleOrig.IP.SourceAddress,
			dstIP: dst,
			proto: flow.TupleOrig.Proto.Protocol,
			port:  port,
		}
		if ignore != nil && ignore(key.srcIP, key.dstIP) {
			continue
		}

		snapshot, ok := snapshots[key]
		if !ok {
			snapshot = &FlowSnapshot{
				srcMAC: macOf(key.srcIP),
				dstMAC: macOf(key.dstIP),
			}
			snapshots[key] = snapshot
		}

		snapshot.inBytes += inBytes
		snapshot.inPackets += inPackets
		snapshot.outBytes += outBytes
		snapshot.outPackets += outPackets
	}

	return snapshots
}

// insertSnapshots stores all snapshots in one transaction, so a failure
// leaves nothing half written.
func insertSnapshots(db *sql.DB, snapshots map[FlowSnapshotKey]*FlowSnapshot) error {
	if len(snapshots) == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`INSERT INTO flows VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, current_timestamp)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for key, snapshot := range snapshots {
		var srcMAC, dstMAC *net.HardwareAddr
		if snapshot.srcMAC != nil {
			srcMAC = &snapshot.srcMAC.HardwareAddr
		}
		if snapshot.dstMAC != nil {
			dstMAC = &snapshot.dstMAC.HardwareAddr
		}

		if _, err := stmt.Exec(srcMAC, dstMAC, key.srcIP.AsSlice(), key.dstIP.AsSlice(), key.proto, key.port,
			snapshot.inBytes, snapshot.inPackets, snapshot.outBytes, snapshot.outPackets); err != nil {
			return fmt.Errorf("insert flow %s -> %s: %w", key.srcIP, key.dstIP, err)
		}
	}

	return tx.Commit()
}

// baseline holds the connections as of the last recorded dump, which the
// traffic of the next one is counted from.
type baseline struct {
	// open are the connections the last recorded dump found.
	open map[uint32]conntrack.Flow

	// vanished are the connections the dump before found and the last one
	// didn't, without being among the connections destroyed in between. They
	// were destroyed all the same, but the event saying so hadn't been read
	// yet, so it should come with the next round. Without them, the
	// connection would count as new then, and its traffic up to the dump
	// before would be recorded again.
	vanished map[uint32]conntrack.Flow
}

// flows returns every connection of the baseline, by ID.
func (b baseline) flows() map[uint32]conntrack.Flow {
	flows := make(map[uint32]conntrack.Flow, len(b.open)+len(b.vanished))
	maps.Copy(flows, b.vanished)
	maps.Copy(flows, b.open)
	return flows
}

// next returns the baseline after a dump that found current, with closed the
// connections destroyed since the last one. Connections that had vanished
// already are dropped: their event should have been among closed, so it was
// lost.
func (b baseline) next(current []conntrack.Flow, closed map[uint32]conntrack.Flow) baseline {
	next := baseline{
		open:     make(map[uint32]conntrack.Flow, len(current)),
		vanished: map[uint32]conntrack.Flow{},
	}
	for _, flow := range current {
		next.open[flow.ID] = flow
	}

	for id, flow := range b.open {
		_, open := next.open[id]
		_, destroyed := closed[id]
		if !open && !destroyed {
			next.vanished[id] = flow
		}
	}

	return next
}

// destroyListener receives the connections conntrack destroys, on a netlink
// socket of its own. Once receiving fails, it stops for good.
type destroyListener struct {
	events <-chan conntrack.Event
	errs   <-chan error
	// closeConn closes the socket, and waits for the worker receiving from
	// it to stop.
	closeConn func() error
}

// listenDestroyed starts listening for the connections conntrack destroys.
func listenDestroyed() (*destroyListener, error) {
	conn, err := conntrack.Dial(nil)
	if err != nil {
		return nil, err
	}

	if err := conn.SetReadBuffer(eventReadBuffer); err != nil {
		log.Println("Failed to enlarge the buffer for Netfilter events:", err)
	}

	events := make(chan conntrack.Event, eventQueue)
	errs, err := conn.Listen(events, 1, []netfilter.NetlinkGroup{netfilter.GroupCTDestroy})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	return &destroyListener{events: events, errs: errs, closeConn: conn.Close}, nil
}

// receive passes every destroyed connection to destroyed until ctx is done
// or receiving fails, then closes the socket. It returns why receiving
// failed, or nil when ctx is done.
func (l *destroyListener) receive(ctx context.Context, destroyed func(conntrack.Flow)) error {
	defer l.stop()

	handle := func(ev conntrack.Event) {
		if ev.Flow != nil {
			destroyed(*ev.Flow)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev := <-l.events:
			handle(ev)
		case err := <-l.errs:
			// The worker stops after an error, and the events it handed
			// over before still count.
			for {
				select {
				case ev := <-l.events:
					handle(ev)
				default:
					return err
				}
			}
		}
	}
}

// stop closes the socket. Closing waits for the worker to stop, which may be
// waiting to hand over an event or an error, so both are drained meanwhile.
func (l *destroyListener) stop() {
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		_ = l.closeConn()
	}()

	for {
		select {
		case <-closed:
			return
		case <-l.events:
		case <-l.errs:
		}
	}
}

// netfilterCollector records the traffic of the connections Netfilter
// tracks: once a minute, the traffic since the last round, of the
// connections still open and those destroyed meanwhile.
type netfilterCollector struct {
	db      *sql.DB
	opts    Options
	sampler *liveSampler

	// dump returns the connections conntrack tracks.
	dump func() ([]conntrack.Flow, error)
	// neighbors returns the MAC addresses of the hosts of the local network.
	neighbors func() (map[netip.Addr]live.MAC, error)
	// listen starts listening for the connections conntrack destroys.
	listen func() (*destroyListener, error)

	// mu guards closed, the connections destroyed since the last round,
	// which the listener adds to.
	mu     sync.Mutex
	closed map[uint32]conntrack.Flow

	// roundMu makes rounds take turns, and guards baseline.
	roundMu  sync.Mutex
	baseline baseline
}

func newNetfilterCollector(db *sql.DB, meter *live.Meter, opts Options) *netfilterCollector {
	sampler := newLiveSampler(meter, &netfilterSource{})
	sampler.ignore = opts.ignored

	return &netfilterCollector{
		db:        db,
		opts:      opts,
		sampler:   sampler,
		dump:      dumpConnections,
		neighbors: readNeighbors,
		listen:    listenDestroyed,
		closed:    map[uint32]conntrack.Flow{},
	}
}

// dumpConnections returns the connections conntrack tracks.
func dumpConnections() ([]conntrack.Flow, error) {
	c, err := conntrack.Dial(nil)
	if err != nil {
		return nil, err
	}
	defer c.Close()

	return c.Dump(nil)
}

// destroyed keeps a connection conntrack destroyed for the next round.
func (n *netfilterCollector) destroyed(flow conntrack.Flow) {
	n.mu.Lock()
	n.closed[flow.ID] = flow
	n.mu.Unlock()

	n.sampler.destroyed(flow)
}

// takeClosed returns the connections destroyed since it was last called.
func (n *netfilterCollector) takeClosed() map[uint32]conntrack.Flow {
	n.mu.Lock()
	defer n.mu.Unlock()

	closed := n.closed
	n.closed = map[uint32]conntrack.Flow{}
	return closed
}

// putBack hands connections takeClosed returned back for the next round,
// when this one couldn't record them. A connection destroyed since then with
// the same ID is a newer one, and stays.
func (n *netfilterCollector) putBack(closed map[uint32]conntrack.Flow) {
	n.mu.Lock()
	defer n.mu.Unlock()

	for id, flow := range closed {
		if _, ok := n.closed[id]; !ok {
			n.closed[id] = flow
		}
	}
}

// record writes the traffic since the last round to the database. When that
// fails, the next round records it.
func (n *netfilterCollector) record() error {
	n.roundMu.Lock()
	defer n.roundMu.Unlock()

	// The destroyed connections are taken before the dump, so that the
	// connections the dump misses are either among them or will have their
	// event in a later round, which the baseline is kept for.
	closed := n.takeClosed()

	current, err := n.dump()
	if err != nil {
		n.putBack(closed)
		return err
	}

	// One look at the neighbor table finds the MAC addresses of every flow.
	macs, err := n.neighbors()
	if err != nil {
		n.putBack(closed)
		return fmt.Errorf("read neighbor table: %w", err)
	}

	snapshots := buildSnapshots(closed, current, n.baseline.flows(), macLookup(macs), n.opts.ignored)

	if err := insertSnapshots(n.db, snapshots); err != nil {
		// Keep the previous baseline too: the next round then records this
		// one's traffic as well, without counting any twice.
		n.putBack(closed)
		return err
	}

	n.baseline = n.baseline.next(current, closed)

	return nil
}

// round records the traffic since the last round, and reports it.
func (n *netfilterCollector) round() {
	if err := n.record(); err != nil {
		log.Println("Failed to process flows:", err)
		return
	}
	n.opts.flushed(time.Now())
}

// listenUntilDone passes the connections conntrack destroys to n.destroyed,
// starting with l, until ctx is done. Whenever receiving fails, as it does
// when events come faster than they are read and the kernel has to drop some
// (ENOBUFS), it listens again.
func (n *netfilterCollector) listenUntilDone(ctx context.Context, l *destroyListener) {
	var lastReport time.Time
	failures := 0

	for {
		err := l.receive(ctx, n.destroyed)
		if ctx.Err() != nil {
			return
		}

		failures++
		if now := time.Now(); now.Sub(lastReport) >= listenFailureReport {
			log.Printf("Lost Netfilter events about connections closing, so some of their traffic isn't recorded (failures since last reported: %d); listening again: %v", failures, err)
			lastReport, failures = now, 0
		}

		retry := minListenRetry
		for {
			if l, err = n.listen(); err == nil {
				break
			}
			log.Printf("Failed to listen for Netfilter events, trying again in %s: %v", retry, err)

			select {
			case <-ctx.Done():
				return
			case <-time.After(retry):
			}
			retry = min(retry*2, maxListenRetry)
		}
	}
}

// start records traffic every minute, listening for destroyed connections
// with l and measuring the live view while it is watched, until the returned
// collector is shut down. Shutting down records the last round.
func (n *netfilterCollector) start(l *destroyListener) (*Collector, error) {
	s, err := gocron.NewScheduler()
	if err != nil {
		l.stop()
		return nil, fmt.Errorf("start scheduler: %w", err)
	}

	if _, err := s.NewJob(gocron.CronJob("*/1 * * * *", false), gocron.NewTask(n.round)); err != nil {
		_ = s.Shutdown()
		l.stop()
		return nil, fmt.Errorf("schedule recording flows: %w", err)
	}

	listenCtx, stopListening := context.WithCancel(context.Background())
	listening := make(chan struct{})
	go func() {
		defer close(listening)
		n.listenUntilDone(listenCtx, l)
	}()
	log.Println("Listening for Netfilter events...")

	sampleCtx, stopSampling := context.WithCancel(context.Background())
	go n.sampler.run(sampleCtx)

	s.Start()

	shutdownChan := make(chan bool, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-shutdownChan

		// Let a round in progress finish, then record what came after it,
		// while still listening for connections closing.
		_ = s.Shutdown()
		stopSampling()
		n.round()

		stopListening()
		<-listening
	}()

	return &Collector{shutdownChan: shutdownChan, done: done}, nil
}

// CollectTraffic records traffic to the database every minute, and, while the
// meter is being watched, measures it every second for the live view.
func CollectTraffic(db *sql.DB, meter *live.Meter, opts Options) (*Collector, error) {
	n := newNetfilterCollector(db, meter, opts)

	// Listening first tells whether netmon can use conntrack at all.
	l, err := n.listen()
	if err != nil {
		return nil, err
	}

	return n.start(l)
}
