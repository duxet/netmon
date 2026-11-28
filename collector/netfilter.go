//go:build linux

package collector

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/duxet/netmon/common"
	"github.com/duxet/netmon/live"
	"github.com/go-co-op/gocron/v2"
	"github.com/ti-mo/conntrack"
	"github.com/ti-mo/netfilter"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"strings"
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

	// maxClosedConnections bounds the closed connections kept for the next
	// round. They grow while rounds can't read conntrack, past what a busy
	// router closes in a minute; connections closing beyond it are left out.
	maxClosedConnections = 1 << 20

	// maxPendingFlows bounds the flows whose traffic is kept while writing
	// to the database fails, for when it works again. Traffic of other flows
	// is left out.
	maxPendingFlows = 1 << 16

	// accountingSysctl tells whether conntrack counts bytes and packets.
	accountingSysctl = "/proc/sys/net/netfilter/nf_conntrack_acct"
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

// buildSnapshots sums the traffic of closed and still open connections since
// the last dump into one snapshot per flow. Flows without new traffic are
// left out, as are those ignore, if set, says to ignore.
func buildSnapshots(
	closed closedSet,
	current []tracked,
	previous baseline,
	macOf func(netip.Addr) *common.MACAddress,
	ignore func(src, dst netip.Addr) bool,
) map[FlowSnapshotKey]*FlowSnapshot {
	local := func(addr netip.Addr) bool { return macOf(addr) != nil }
	snapshots := map[FlowSnapshotKey]*FlowSnapshot{}

	add := func(c connection, traffic carried) {
		if traffic.zero() {
			return
		}

		dst, port := c.destination(local)
		key := FlowSnapshotKey{srcIP: c.src, dstIP: dst, proto: c.proto, port: port}
		if ignore != nil && ignore(key.srcIP, key.dstIP) {
			return
		}

		snapshot, ok := snapshots[key]
		if !ok {
			snapshot = &FlowSnapshot{
				srcMAC: macOf(key.srcIP),
				dstMAC: macOf(key.dstIP),
			}
			snapshots[key] = snapshot
		}
		snapshot.inBytes += traffic.inBytes
		snapshot.inPackets += traffic.inPackets
		snapshot.outBytes += traffic.outBytes
		snapshot.outPackets += traffic.outPackets
	}

	alive := make(map[connID]struct{}, len(current))
	for _, t := range current {
		alive[t.id] = struct{}{}
	}

	// replaced are the connections in current that took the place of one
	// that closed since the last dump, and whose traffic all came since.
	replaced := map[connID]bool{}

	for id, c := range closed.first {
		old, seen := previous.get(id)
		_, stillOpen := alive[id]
		if _, found := previous.open[id]; found && stillOpen && !c.counters.covers(old) {
			// The last dump found another connection than this one: the
			// one still open, which took its place before that dump. Its
			// event came late, and what it carried before isn't known.
			add(c, closed.later[id])
			continue
		}
		add(c, c.counters.since(old, seen))
		// The connections that took its place came after the last dump.
		add(c, closed.later[id])
		replaced[id] = true
	}

	for _, t := range current {
		if replaced[t.id] {
			add(t.connection, t.counters)
			continue
		}
		old, seen := previous.get(t.id)
		add(t.connection, t.counters.since(old, seen))
	}

	return snapshots
}

// add adds the traffic of other to s.
func (s *FlowSnapshot) add(other *FlowSnapshot) {
	s.inBytes += other.inBytes
	s.inPackets += other.inPackets
	s.outBytes += other.outBytes
	s.outPackets += other.outPackets
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

// baseline holds what the connections had carried at the last recorded dump,
// which the traffic of the next one is counted from.
type baseline struct {
	// open are the connections the last recorded dump found.
	open map[connID]carried

	// vanished are the connections the dump before found and the last one
	// didn't, without being among the connections destroyed in between. They
	// were destroyed all the same, but the event saying so hadn't been read
	// yet, so it should come with the next round. Without them, the
	// connection would count as new then, and its traffic up to the dump
	// before would be recorded again.
	vanished map[connID]carried
}

// get returns what the connection id had carried, if the baseline has it.
func (b baseline) get(id connID) (carried, bool) {
	if c, ok := b.open[id]; ok {
		return c, true
	}
	c, ok := b.vanished[id]
	return c, ok
}

// next returns the baseline after a dump that found current, with closed the
// connections destroyed since the last one. Connections that had vanished
// already are dropped: their event should have been among closed, so it was
// lost.
func (b baseline) next(current []tracked, closed map[connID]connection) baseline {
	next := baseline{
		open:     make(map[connID]carried, len(current)),
		vanished: map[connID]carried{},
	}
	for _, t := range current {
		next.open[t.id] = t.counters
	}

	for id, c := range b.open {
		_, open := next.open[id]
		_, destroyed := closed[id]
		if !open && !destroyed {
			next.vanished[id] = c
		}
	}

	return next
}

// closedSet holds the connections conntrack destroyed since the last round.
type closedSet struct {
	// first holds each destroyed connection by ID, the first one if
	// several had the same.
	first map[connID]connection
	// later sums up the traffic of connections that took the place of one
	// in first and were destroyed too. Each one's place was taken after it
	// closed, so only the one in first can be one the last dump found.
	later map[connID]carried
	// dropped counts the connections left out as there were more than
	// limit.
	dropped int
	limit   int
}

func newClosedSet(limit int) closedSet {
	return closedSet{first: map[connID]connection{}, later: map[connID]carried{}, limit: limit}
}

// keep adds a destroyed connection, unless there are too many.
func (s *closedSet) keep(id connID, c connection) {
	if _, ok := s.first[id]; ok {
		later := s.later[id]
		later.add(c.counters)
		s.later[id] = later
		return
	}
	if len(s.first) >= s.limit {
		s.dropped++
		return
	}
	s.first[id] = c
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
		slog.Warn("Failed to enlarge the buffer for Netfilter events", "err", err)
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
	dump func() ([]tracked, error)
	// neighbors returns the MAC addresses of the hosts of the local network.
	neighbors func() (map[netip.Addr]live.MAC, error)
	// listen starts listening for the connections conntrack destroys.
	listen func() (*destroyListener, error)
	// accounting tells whether conntrack counts traffic.
	accounting func() (bool, error)

	// mu guards closed, the connections destroyed since the last round,
	// which the listener adds to.
	mu     sync.Mutex
	closed closedSet

	// roundMu makes rounds take turns, and guards baseline and pending.
	roundMu  sync.Mutex
	baseline baseline
	// pending is the traffic of rounds that couldn't be written to the
	// database, which the next round writes along with its own, of up to
	// pendingLimit flows.
	pending      map[FlowSnapshotKey]*FlowSnapshot
	pendingLimit int
}

func newNetfilterCollector(db *sql.DB, meter *live.Meter, opts Options) *netfilterCollector {
	sampler := newLiveSampler(meter, &netfilterSource{})
	sampler.ignore = opts.ignored

	return &netfilterCollector{
		db:         db,
		opts:       opts,
		sampler:    sampler,
		dump:       dumpConnections,
		neighbors:  readNeighbors,
		listen:     listenDestroyed,
		accounting: readAccounting,
		closed:     newClosedSet(maxClosedConnections),

		pendingLimit: maxPendingFlows,
	}
}

// readAccounting tells whether conntrack counts the bytes and packets of
// connections, which the kernel only does with accounting on.
func readAccounting() (bool, error) {
	data, err := os.ReadFile(accountingSysctl)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(data)) != "0", nil
}

// destroyed keeps a connection conntrack destroyed for the next round.
func (n *netfilterCollector) destroyed(flow conntrack.Flow) {
	t := track(flow)

	n.mu.Lock()
	n.closed.keep(t.id, t.connection)
	n.mu.Unlock()

	n.sampler.destroyed(t)
}

// takeClosed returns the connections destroyed since it was last called.
func (n *netfilterCollector) takeClosed() closedSet {
	n.mu.Lock()
	defer n.mu.Unlock()

	closed := n.closed
	n.closed = newClosedSet(closed.limit)
	return closed
}

// putBack hands connections takeClosed returned back for the next round,
// when this one couldn't record them. They closed before the ones destroyed
// since, which can only have taken their place.
func (n *netfilterCollector) putBack(closed closedSet) {
	n.mu.Lock()
	defer n.mu.Unlock()

	since := n.closed
	n.closed = closed
	n.closed.dropped = since.dropped
	for id, c := range since.first {
		later := since.later[id]
		if _, ok := n.closed.first[id]; ok {
			later.add(c.counters)
			later.add(n.closed.later[id])
			n.closed.later[id] = later
			continue
		}
		if len(n.closed.first) >= n.closed.limit {
			n.closed.dropped++
			continue
		}
		n.closed.first[id] = c
		if !later.zero() {
			n.closed.later[id] = later
		}
	}
}

// withPending adds the traffic of rounds that couldn't be written to this
// round's snapshots. It keeps the traffic of up to pendingLimit flows, and
// leaves out the rest.
func (n *netfilterCollector) withPending(snapshots map[FlowSnapshotKey]*FlowSnapshot) map[FlowSnapshotKey]*FlowSnapshot {
	if len(n.pending) == 0 {
		return snapshots
	}

	dropped := 0
	for key, snapshot := range snapshots {
		if pending, ok := n.pending[key]; ok {
			pending.add(snapshot)
			continue
		}
		if len(n.pending) >= n.pendingLimit {
			dropped++
			continue
		}
		n.pending[key] = snapshot
	}

	if dropped > 0 {
		slog.Warn("Writing to the database keeps failing, left out the traffic of some flows", "flows", dropped)
		n.opts.Health.droppedFlows(dropped)
	}
	return n.pending
}

// record writes the traffic since the last round to the database, and
// returns how many flows it wrote. When that fails, the next round records
// it.
func (n *netfilterCollector) record() (int, error) {
	n.roundMu.Lock()
	defer n.roundMu.Unlock()

	// The destroyed connections are taken before the dump, so that the
	// connections the dump misses are either among them or will have their
	// event in a later round, which the baseline is kept for.
	closed := n.takeClosed()
	if closed.dropped > 0 {
		slog.Warn("Too many connections closed while conntrack couldn't be read, left out the traffic of some", "connections", closed.dropped)
		n.opts.Health.droppedConnections(closed.dropped)
		closed.dropped = 0
	}

	current, err := n.dump()
	if err != nil {
		n.putBack(closed)
		return 0, err
	}

	// One look at the neighbor table finds the MAC addresses of every flow.
	macs, err := n.neighbors()
	if err != nil {
		n.putBack(closed)
		return 0, fmt.Errorf("read neighbor table: %w", err)
	}

	snapshots := buildSnapshots(closed, current, n.baseline, macLookup(macs), n.opts.ignored)
	n.baseline = n.baseline.next(current, closed.first)

	// The traffic is counted now: if it can't be written, it is kept as it
	// is, rather than the connections, and comes along with the next round.
	snapshots = n.withPending(snapshots)
	if err := insertSnapshots(n.db, snapshots); err != nil {
		n.pending = snapshots
		return 0, err
	}
	n.pending = nil

	for _, snapshot := range snapshots {
		var srcMAC, dstMAC net.HardwareAddr
		if snapshot.srcMAC != nil {
			srcMAC = snapshot.srcMAC.HardwareAddr
		}
		if snapshot.dstMAC != nil {
			dstMAC = snapshot.dstMAC.HardwareAddr
		}
		n.opts.Totals.AddFlow(srcMAC, dstMAC, snapshot.inBytes, snapshot.outBytes)
	}

	return len(snapshots), nil
}

// round records the traffic since the last round, and reports it.
func (n *netfilterCollector) round() {
	n.checkAccounting()

	rows, err := n.record()
	if err != nil {
		slog.Error("Failed to process flows", "err", err)
		n.opts.Health.roundFailed(err)
		return
	}
	n.opts.flushed(time.Now(), rows)
}

// checkAccounting warns when conntrack doesn't count traffic, so that none is
// recorded. Where the setting can't be read, it says nothing.
func (n *netfilterCollector) checkAccounting() {
	on, err := n.accounting()
	if err == nil && !on {
		n.opts.Health.warn("accounting", "Connection tracking doesn't count traffic, so netmon records none: "+
			"turn accounting on with sysctl -w net.netfilter.nf_conntrack_acct=1")
		return
	}
	n.opts.Health.warn("accounting", "")
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
		n.opts.Health.eventsOverflowed()
		if now := time.Now(); now.Sub(lastReport) >= listenFailureReport {
			slog.Warn("Lost Netfilter events about connections closing, so some of their traffic isn't recorded; listening again", "failures", failures, "err", err)
			lastReport, failures = now, 0
		}

		retry := minListenRetry
		for {
			if l, err = n.listen(); err == nil {
				break
			}
			slog.Error("Failed to listen for Netfilter events", "retry_in", retry, "err", err)

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
	slog.Info("Listening for Netfilter events")

	sampleCtx, stopSampling := context.WithCancel(context.Background())
	go n.sampler.run(sampleCtx)

	// Rather than only at the first round, say right away when no traffic
	// will be counted.
	n.checkAccounting()
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
		if errors.Is(err, os.ErrPermission) {
			err = fmt.Errorf("reading connection tracking needs root or CAP_NET_ADMIN: %w", err)
		}
		return nil, err
	}

	c, err := n.start(l)
	if err != nil {
		return nil, err
	}
	opts.Health.Started()
	return c, nil
}
