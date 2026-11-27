//go:build linux

package collector

import (
	"context"
	"log"
	"net/netip"
	"sync"
	"time"

	"github.com/duxet/netmon/live"
	"github.com/ti-mo/conntrack"
	"github.com/vishvananda/netlink"
)

// The live view needs the traffic of the last second, while the flows table is
// written once a minute. The Netfilter collector therefore also measures
// conntrack's byte counters every second, but only while someone is looking at
// the live view: dumping every connection that often is not free on a small
// router, and nobody needs the numbers when nobody asks for them.

// liveSource is where the live sampler gets its data from.
type liveSource interface {
	// Flows returns the connections being tracked, with their counters.
	Flows() ([]conntrack.Flow, error)

	// Neighbors returns the MAC addresses of the hosts on the local network.
	Neighbors() (map[netip.Addr]live.MAC, error)

	// Close releases what the source holds. It may be used again afterwards.
	Close()
}

type liveSampler struct {
	meter    *live.Meter
	source   liveSource
	interval time.Duration

	// ignore, if set, tells which connections to leave out, by the addresses
	// of their originator and responder.
	ignore func(src, dst netip.Addr) bool

	mu sync.Mutex

	// sampling is false while nobody is watching. The counters of the
	// connections are only known from the sample that follows it, so the
	// first sample only records them.
	sampling bool

	// previous holds the connections as of the last sample, to work out how
	// much they carried since then.
	previous map[uint32]conntrack.Flow

	// macs are the local hosts found at the last sample.
	macs map[netip.Addr]live.MAC
}

func newLiveSampler(meter *live.Meter, source liveSource) *liveSampler {
	return &liveSampler{meter: meter, source: source, interval: live.Interval}
}

// run samples every interval for as long as the meter is watched, until the
// context is done.
func (s *liveSampler) run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	defer s.idle()

	failing := false

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if !s.meter.Watched(now) {
				s.idle()
				continue
			}

			// A problem usually lasts, so it is reported once rather than
			// every second.
			err := s.sample(now)
			if err != nil && !failing {
				log.Println("Failed to sample live traffic:", err)
			}
			failing = err != nil
		}
	}
}

// idle stops sampling until the meter is watched again.
func (s *liveSampler) idle() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sampling = false
	s.previous = nil
	s.macs = nil
	s.source.Close()
}

// sample adds the traffic since the previous sample to the meter and ends its
// interval.
func (s *liveSampler) sample(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	flows, err := s.source.Flows()
	if err != nil {
		return err
	}

	macs, err := s.source.Neighbors()
	if err != nil {
		return err
	}

	previous := s.previous
	baseline := !s.sampling

	if baseline {
		// Whatever the meter knows is from before the sampler stopped.
		s.meter.Reset()
	}

	s.sampling = true
	s.macs = macs
	s.previous = make(map[uint32]conntrack.Flow, len(flows))

	for _, flow := range flows {
		s.previous[flow.ID] = flow

		if !baseline {
			old, seen := previous[flow.ID]
			s.record(flow, old, seen)
		}
	}

	s.meter.Tick(now)

	return nil
}

// destroyed accounts for the last traffic of a connection that has been closed,
// which the next sample won't find any more. Without it, connections that last
// less than a second, such as most DNS lookups, would never be seen.
func (s *liveSampler) destroyed(flow conntrack.Flow) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.sampling {
		return
	}

	old, seen := s.previous[flow.ID]
	delete(s.previous, flow.ID)

	s.record(flow, old, seen)
}

// record adds what a connection carried since old, its state at the previous
// sample, to the devices it is between. The caller holds s.mu.
func (s *liveSampler) record(flow conntrack.Flow, old conntrack.Flow, seen bool) {
	// Conntrack calls what the originator sent "orig" and the replies
	// "reply", so the originator downloaded the latter.
	reply, _, orig, _ := flowDelta(flow, old, seen)
	if reply == 0 && orig == 0 {
		return
	}

	src := flow.TupleOrig.IP.SourceAddress
	dst, _ := destination(flow, func(addr netip.Addr) bool {
		_, ok := s.macs[addr.Unmap()]
		return ok
	})
	if s.ignore != nil && s.ignore(src, dst) {
		return
	}

	srcMAC, srcLocal := s.macs[src.Unmap()]
	dstMAC, dstLocal := s.macs[dst.Unmap()]

	// A device talking to itself, through another of its addresses, does not
	// use the network.
	if srcLocal && dstLocal && srcMAC == dstMAC {
		return
	}

	if srcLocal {
		s.meter.Record(srcMAC, src, reply, orig)
	}
	if dstLocal {
		s.meter.Record(dstMAC, dst, orig, reply)
	}
}

// netfilterSource reads from the kernel.
type netfilterSource struct {
	conn *conntrack.Conn
}

func (n *netfilterSource) Flows() ([]conntrack.Flow, error) {
	// One socket is kept for as long as the live view is watched, instead of
	// a new one every second.
	if n.conn == nil {
		conn, err := conntrack.Dial(nil)
		if err != nil {
			return nil, err
		}
		n.conn = conn
	}

	flows, err := n.conn.Dump(nil)
	if err != nil {
		n.Close()
		return nil, err
	}

	return flows, nil
}

func (n *netfilterSource) Neighbors() (map[netip.Addr]live.MAC, error) {
	return readNeighbors()
}

// readNeighbors returns the MAC addresses of the hosts in the neighbor table,
// IPv4 and IPv6, in one query rather than one per connection.
func readNeighbors() (map[netip.Addr]live.MAC, error) {
	neighbors, err := netlink.NeighList(0, netlink.FAMILY_ALL)
	if err != nil {
		return nil, err
	}

	return neighborMACs(neighbors), nil
}

// neighborMACs maps the addresses in the neighbor table to the MAC address of
// the host they belong to, leaving out the entries that do not belong to one.
func neighborMACs(neighbors []netlink.Neigh) map[netip.Addr]live.MAC {
	macs := make(map[netip.Addr]live.MAC, len(neighbors))

	for _, neighbor := range neighbors {
		// Hosts that did not answer are listed without an address, or with an
		// all zero one, and group addresses are no single host's.
		if len(neighbor.HardwareAddr) != len(live.MAC{}) || neighbor.HardwareAddr[0]&1 != 0 {
			continue
		}

		mac := live.MAC(neighbor.HardwareAddr)
		if mac == (live.MAC{}) {
			continue
		}

		addr, ok := netip.AddrFromSlice(neighbor.IP)
		if !ok || addr.IsUnspecified() {
			continue
		}

		macs[addr.Unmap()] = mac
	}

	return macs
}

func (n *netfilterSource) Close() {
	if n.conn != nil {
		_ = n.conn.Close()
		n.conn = nil
	}
}
