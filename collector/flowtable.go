package collector

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"

	"github.com/duxet/netmon/live"
)

// This file is the platform independent half of the packet capture
// collector (see pcap_darwin.go). It turns a stream of observed packets into
// the same kind of records the Netfilter collector writes to the flows table:
// one row per (client IP, server IP, protocol, server port) and interval,
// holding the traffic exchanged in that interval.

// defaultMaxFlows bounds the memory used while tracking flows, so that
// something like a port scan can't exhaust it. It is on the same order as
// Linux's default conntrack table size.
const defaultMaxFlows = 1 << 18

type macAddr struct {
	addr  [6]byte
	valid bool
}

// unicast reports whether the address can identify a single host, i.e. it is
// neither the broadcast address nor a multicast one.
func (m macAddr) unicast() bool {
	return m.valid && m.addr[0]&1 == 0
}

// hardwareAddr returns the address, or nil when there is none.
func (m macAddr) hardwareAddr() net.HardwareAddr {
	if !m.valid {
		return nil
	}
	return net.HardwareAddr(m.addr[:])
}

// dbValue is the value to store in a BLOB column: the address, or NULL.
func (m macAddr) dbValue() any {
	if !m.valid {
		return nil
	}

	return m.addr[:]
}

type endpoint struct {
	ip   netip.Addr
	port uint16
}

type packetEndpoint struct {
	endpoint

	// mac is the link layer address the packet was sent from (or to). It
	// belongs to the host only when that host is on the local network, as
	// otherwise it is just the address of the next hop.
	mac macAddr
}

// initiatorHint tells which side of a flow opened it, if the packet shows it.
type initiatorHint uint8

const (
	hintNone        initiatorHint = iota
	hintSrcIsClient               // opens a flow: TCP SYN, ICMP echo request
	hintDstIsClient               // answers one: TCP SYN-ACK, ICMP echo reply
)

// packetInfo is everything the flow table needs to know about a packet.
type packetInfo struct {
	src, dst packetEndpoint
	proto    uint8
	length   uint64 // size of the IP packet, headers included
	hint     initiatorHint
}

type counters struct {
	bytes, packets uint64
}

func (c *counters) add(other counters) {
	c.bytes += other.bytes
	c.packets += other.packets
}

// flowKey identifies a flow regardless of the direction of the packet, so
// that both sides of a conversation end up in the same entry.
type flowKey struct {
	a, b  endpoint
	proto uint8
}

func newFlowKey(x, y endpoint, proto uint8) flowKey {
	if c := x.ip.Compare(y.ip); c > 0 || (c == 0 && x.port > y.port) {
		x, y = y, x
	}

	return flowKey{x, y, proto}
}

type flowSide struct {
	endpoint
	mac macAddr
}

type flowEntry struct {
	client, server flowSide
	out            counters // client -> server
	in             counters // server -> client
	active         bool     // saw traffic since the last flush
}

// flowRow is the traffic of one client talking to one service during an
// interval. It mirrors a row of the flows table.
type flowRow struct {
	srcMAC, dstMAC macAddr
	srcIP, dstIP   netip.Addr
	proto          uint8
	port           uint16
	out            counters // src -> dst
	in             counters // dst -> src
}

type rowKey struct {
	src, dst netip.Addr
	proto    uint8
	port     uint16
}

// flowTable aggregates packets into flows. It is safe for concurrent use.
type flowTable struct {
	mu       sync.Mutex
	flows    map[flowKey]*flowEntry
	local    []netip.Prefix
	maxFlows int
	dropped  uint64

	// meter, if set, is told about the traffic of the local devices as it
	// happens, for the live view.
	meter *live.Meter

	// ignore, if set, tells which packets to leave out, by their source and
	// destination.
	ignore func(src, dst netip.Addr) bool
}

func newFlowTable() *flowTable {
	return &flowTable{
		flows:    make(map[flowKey]*flowEntry),
		maxFlows: defaultMaxFlows,
	}
}

// SetLocalPrefixes sets the networks directly attached to the capture
// interface. Only hosts on them have their MAC address recorded.
func (t *flowTable) SetLocalPrefixes(prefixes []netip.Prefix) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.local = prefixes
}

// Add accounts for one packet.
func (t *flowTable) Add(p packetInfo) {
	if t.ignore != nil && t.ignore(p.src.ip, p.dst.ip) {
		return
	}

	key := newFlowKey(p.src.endpoint, p.dst.endpoint, p.proto)

	t.mu.Lock()
	defer t.mu.Unlock()

	e, ok := t.flows[key]
	if !ok {
		if len(t.flows) >= t.maxFlows {
			t.dropped++
			return
		}

		e = newFlowEntry(p)
		t.flows[key] = e
	}

	e.active = true

	from, to, c := &e.client, &e.server, &e.out
	if p.src.endpoint != e.client.endpoint {
		from, to, c = &e.server, &e.client, &e.in
	}

	c.bytes += p.length
	c.packets++

	t.learnMAC(from, p.src.mac)
	t.learnMAC(to, p.dst.mac)

	t.recordLive(from, to, p.length)
}

// recordLive tells the live view about a packet, for each of its ends that is a
// device on the local network. Only those have a MAC address, which is what
// devices are told apart by.
func (t *flowTable) recordLive(from, to *flowSide, length uint64) {
	if t.meter == nil {
		return
	}

	if from.mac.valid {
		t.meter.Record(live.MAC(from.mac.addr), from.ip, 0, length)
	}

	if to.mac.valid {
		t.meter.Record(live.MAC(to.mac.addr), to.ip, length, 0)
	}
}

// learnMAC remembers the MAC address of a host, but only if it is on the
// local network. Everything else is reached through the gateway, and
// attributing the gateway's address to each of those hosts would make it look
// like a client of the whole internet.
func (t *flowTable) learnMAC(side *flowSide, mac macAddr) {
	if side.mac.valid || !mac.unicast() {
		return
	}

	for _, prefix := range t.local {
		if prefix.Contains(side.ip) {
			side.mac = mac
			return
		}
	}
}

// newFlowEntry decides which side of a flow is the client. Netfilter knows
// that, since it saw the connection being opened. A capture only knows it if
// it happens to see the opening packet, so otherwise we guess, as the side
// with the higher port is far more likely to be the one with an ephemeral
// port than the one running a service. The guess is made once per entry:
// both directions of a flow are then reported consistently.
func newFlowEntry(p packetInfo) *flowEntry {
	srcIsClient := true

	switch {
	case p.hint == hintSrcIsClient:
	case p.hint == hintDstIsClient:
		srcIsClient = false
	case p.src.port != p.dst.port:
		srcIsClient = p.src.port > p.dst.port
	}

	client, server := p.src, p.dst
	if !srcIsClient {
		client, server = p.dst, p.src
	}

	return &flowEntry{
		client: flowSide{endpoint: client.endpoint},
		server: flowSide{endpoint: server.endpoint},
	}
}

// Flush returns the traffic seen since the previous call, merging all the
// flows between the same hosts to the same service (a client usually opens
// many connections to one server, each from a different port) into one row,
// like the Netfilter collector does. It also returns how many packets of new
// flows had to be ignored because the table was full.
//
// Flows that did not see any traffic since the previous call are forgotten.
// The rest keep their entry, so that they stay oriented the same way.
func (t *flowTable) Flush() (rows []flowRow, dropped uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	merged := make(map[rowKey]*flowRow)

	for key, e := range t.flows {
		if !e.active {
			delete(t.flows, key)
			continue
		}

		rk := rowKey{e.client.ip, e.server.ip, key.proto, e.server.port}

		row, ok := merged[rk]
		if !ok {
			row = &flowRow{
				srcIP: e.client.ip,
				dstIP: e.server.ip,
				proto: key.proto,
				port:  e.server.port,
			}
			merged[rk] = row
		}

		row.out.add(e.out)
		row.in.add(e.in)

		if !row.srcMAC.valid {
			row.srcMAC = e.client.mac
		}

		if !row.dstMAC.valid {
			row.dstMAC = e.server.mac
		}

		e.out, e.in, e.active = counters{}, counters{}, false
	}

	rows = make([]flowRow, 0, len(merged))
	for _, row := range merged {
		rows = append(rows, *row)
	}

	dropped, t.dropped = t.dropped, 0

	return rows, dropped
}

// flushTo writes the traffic collected since the previous flush to the
// database, adds it to totals, and returns how many flows it wrote.
func (t *flowTable) flushTo(db *sql.DB, totals *Totals) (int, error) {
	rows, dropped := t.Flush()

	if dropped > 0 {
		slog.Warn("Flow table is full, ignored the packets of new flows", "packets", dropped)
	}

	if err := insertFlows(db, rows); err != nil {
		return 0, err
	}
	for _, r := range rows {
		totals.AddFlow(r.srcMAC.hardwareAddr(), r.dstMAC.hardwareAddr(), r.in.bytes, r.out.bytes)
	}
	return len(rows), nil
}

func insertFlows(db *sql.DB, rows []flowRow) error {
	if len(rows) == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO flows (src_mac, dst_mac, src_ip, dst_ip, ip_proto, port, in_bytes, in_packets, out_bytes, out_packets, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, current_timestamp)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, r := range rows {
		_, err := stmt.Exec(
			r.srcMAC.dbValue(), r.dstMAC.dbValue(),
			r.srcIP.AsSlice(), r.dstIP.AsSlice(),
			r.proto, r.port,
			r.in.bytes, r.in.packets, r.out.bytes, r.out.packets,
		)
		if err != nil {
			return fmt.Errorf("inserting flow: %w", err)
		}
	}

	return tx.Commit()
}
