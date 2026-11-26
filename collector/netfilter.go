//go:build linux

package collector

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/duxet/netmon/common"
	"github.com/duxet/netmon/live"
	"github.com/go-co-op/gocron/v2"
	"github.com/ti-mo/conntrack"
	"github.com/ti-mo/netfilter"
	"github.com/vishvananda/netlink"
	"log"
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

var closedFlows = map[uint32]conntrack.Flow{}
var continuedFlows = map[uint32]conntrack.Flow{}
var lock = sync.RWMutex{}

func queryMacAddress(ipAddress netip.Addr) (*common.MACAddress, error) {
	var family int

	log.Printf("Looking for MAC address of: %s\n", ipAddress.String())

	switch {
	case ipAddress.Is6():
		family = netlink.FAMILY_V6
	default:
		family = netlink.FAMILY_V4
	}

	neighbors, err := netlink.NeighListExecute(netlink.Ndmsg{
		Family: uint8(family),
	})
	if err != nil {
		fmt.Println(err)
		return nil, err
	}

	for _, neighbor := range neighbors {
		if bytes.Equal(neighbor.IP, ipAddress.AsSlice()) {
			log.Printf("%s MAC address is %s", ipAddress, neighbor.HardwareAddr)
			return &common.MACAddress{HardwareAddr: neighbor.HardwareAddr}, nil
		}
	}

	return nil, errors.New("no neighbor found with specified ip address")
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

	snapshots := map[FlowSnapshotKey]*FlowSnapshot{}

	for _, flow := range allFlows {
		old, seen := previous[flow.ID]
		inBytes, inPackets, outBytes, outPackets := flowDelta(flow, old, seen)
		if inBytes == 0 && inPackets == 0 && outBytes == 0 && outPackets == 0 {
			continue
		}

		key := FlowSnapshotKey{
			srcIP: flow.TupleOrig.IP.SourceAddress,
			dstIP: flow.TupleOrig.IP.DestinationAddress,
			proto: flow.TupleOrig.Proto.Protocol,
			port:  flow.TupleOrig.Proto.DestinationPort,
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

func dumpFlows(db *sql.DB, ignore func(src, dst netip.Addr) bool) error {
	lock.Lock()
	defer lock.Unlock()

	c, err := conntrack.Dial(nil)
	if err != nil {
		return err
	}
	defer c.Close()

	currentFlows, err := c.Dump(nil)
	if err != nil {
		return err
	}

	snapshots := buildSnapshots(closedFlows, currentFlows, continuedFlows, func(addr netip.Addr) *common.MACAddress {
		mac, _ := queryMacAddress(addr)
		return mac
	}, ignore)

	if err := insertSnapshots(db, snapshots); err != nil {
		// Keep the closed flows and the previous baseline: the next dump
		// then records this minute's traffic too, without counting twice.
		return err
	}

	// The open connections are the baseline for the next dump's deltas.
	closedFlows = map[uint32]conntrack.Flow{}
	continuedFlows = make(map[uint32]conntrack.Flow, len(currentFlows))
	for _, flow := range currentFlows {
		continuedFlows[flow.ID] = flow
	}

	return nil
}

// CollectTraffic records traffic to the database every minute, and, while the
// meter is being watched, measures it every second for the live view.
func CollectTraffic(db *sql.DB, meter *live.Meter, opts Options) (*Collector, error) {
	shutdownChan := make(chan bool, 1)
	sampler := newLiveSampler(meter, &netfilterSource{})
	sampler.ignore = opts.ignored

	s, err := gocron.NewScheduler()
	if err != nil {
		log.Println("Failed to start scheduler:", err)
	}

	_, _ = s.NewJob(
		gocron.CronJob("*/1 * * * *", false),
		gocron.NewTask(func() {
			if err := dumpFlows(db, opts.ignored); err != nil {
				log.Println("Failed to process flows:", err)
				return
			}
			opts.flushed(time.Now())
		}),
	)

	s.Start()

	c, err := conntrack.Dial(nil)
	if err != nil {
		return nil, err
	}

	evChan := make(chan conntrack.Event)
	errChan, err := c.Listen(evChan, 1, []netfilter.NetlinkGroup{
		netfilter.GroupCTDestroy,
	})
	if err != nil {
		return nil, err
	}

	go func() {
		err, ok := <-errChan
		if !ok {
			log.Println("Error while listening for Netfilter events", err)
			return
		}
	}()

	log.Println("Listening for Netfilter events...")

	go func() {
		for {
			ev := <-evChan
			lock.Lock()
			closedFlows[ev.Flow.ID] = *ev.Flow
			lock.Unlock()

			sampler.destroyed(*ev.Flow)
		}
	}()

	ctx, stopSampling := context.WithCancel(context.Background())
	go sampler.run(ctx)

	go func() {
		<-shutdownChan
		stopSampling()
		c.Close()
	}()

	return &Collector{shutdownChan: shutdownChan}, nil
}
