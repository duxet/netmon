//go:build linux

package collector

import (
	"net/netip"

	"github.com/ti-mo/conntrack"
)

// connID tells the connections conntrack tracks apart. Conntrack's own ID
// doesn't quite: the kernel derives it from where it keeps a connection and
// the connection's addresses and ports, so a connection between the same ports
// that takes the place of one that just ended, as when a client reuses its
// port, gets the same ID, and now and then two connections get the same one by
// chance. The ports tell nearly all of the latter apart, and the time conntrack
// started tracking a connection the former, where conntrack records it
// (nf_conntrack_timestamp).
type connID struct {
	id    uint32
	ports uint32
	start int64
}

// carried is what a connection carried so far, as conntrack counts: in is
// what the responder sent, out what the originator sent.
type carried struct {
	inBytes, inPackets, outBytes, outPackets uint64
}

func (c carried) zero() bool {
	return c == carried{}
}

func (c *carried) add(o carried) {
	c.inBytes += o.inBytes
	c.inPackets += o.inPackets
	c.outBytes += o.outBytes
	c.outPackets += o.outPackets
}

// covers reports whether c is at least previous in every count, as a
// connection's counters are later on.
func (c carried) covers(previous carried) bool {
	return c.inBytes >= previous.inBytes && c.inPackets >= previous.inPackets &&
		c.outBytes >= previous.outBytes && c.outPackets >= previous.outPackets
}

// since returns what a connection carried since it had carried previous, if
// seen. Smaller counters than then mean another connection took the place of
// that one, and carried all of its traffic since.
func (c carried) since(previous carried, seen bool) carried {
	if !seen || !c.covers(previous) {
		return c
	}
	return carried{
		inBytes:    c.inBytes - previous.inBytes,
		inPackets:  c.inPackets - previous.inPackets,
		outBytes:   c.outBytes - previous.outBytes,
		outPackets: c.outPackets - previous.outPackets,
	}
}

// connection is what netmon keeps of a connection: whom it is between, and
// what it carried. It takes a fifth of the memory of conntrack.Flow, which adds
// up on a busy router with a hundred thousand connections.
type connection struct {
	// src is the originator, which sent the connection to dst and port.
	src, dst netip.Addr
	// from and fromPort are where the replies come from, which is where the
	// connection really went: unlike dst and port, after destination NAT.
	from     netip.Addr
	port     uint16
	fromPort uint16
	proto    uint8
	counters carried
}

// destination returns the address and port a connection goes to. That is
// where its originator sent it, unless conntrack translated the destination
// to another host of the local network, as for a port forwarded to a server
// at home: the traffic is then that host's. local tells which addresses
// belong to such hosts. Translations to the machine netmon runs on, such as
// redirecting DNS queries to the router's own resolver, keep the address the
// originator sent to, as a machine isn't in its own neighbor table.
func (c connection) destination(local func(netip.Addr) bool) (netip.Addr, uint16) {
	if c.from.IsValid() && (c.from != c.dst || c.fromPort != c.port) && local(c.from) {
		return c.from, c.fromPort
	}
	return c.dst, c.port
}

// tracked is a connection conntrack tracks, with what tells it apart.
type tracked struct {
	id connID
	connection
}

// track keeps what netmon needs of flow.
func track(flow conntrack.Flow) tracked {
	orig, reply := flow.TupleOrig, flow.TupleReply

	id := connID{id: flow.ID, ports: uint32(orig.Proto.SourcePort)<<16 | uint32(orig.Proto.DestinationPort)}
	if !flow.Timestamp.Start.IsZero() {
		id.start = flow.Timestamp.Start.UnixNano()
	}

	return tracked{
		id: id,
		connection: connection{
			src:      orig.IP.SourceAddress,
			dst:      orig.IP.DestinationAddress,
			from:     reply.IP.SourceAddress,
			port:     orig.Proto.DestinationPort,
			fromPort: reply.Proto.SourcePort,
			proto:    orig.Proto.Protocol,
			counters: carried{
				inBytes:    flow.CountersReply.Bytes,
				inPackets:  flow.CountersReply.Packets,
				outBytes:   flow.CountersOrig.Bytes,
				outPackets: flow.CountersOrig.Packets,
			},
		},
	}
}

// trackAll keeps what netmon needs of the connections of a dump. Dying
// connections are left out: they are being destroyed, and are recorded from
// the event saying so.
func trackAll(flows []conntrack.Flow) []tracked {
	all := make([]tracked, 0, len(flows))
	for _, flow := range flows {
		if flow.Status.Dying() {
			continue
		}
		all = append(all, track(flow))
	}
	return all
}

// unique leaves out the connections a dump returned more than once, which it
// can while connections come and go during it, and returns how many it left
// out.
func unique(connections []tracked) ([]tracked, int) {
	seen := make(map[connID]struct{}, len(connections))
	kept := connections[:0]
	for _, c := range connections {
		if _, ok := seen[c.id]; ok {
			continue
		}
		seen[c.id] = struct{}{}
		kept = append(kept, c)
	}
	return kept, len(connections) - len(kept)
}
