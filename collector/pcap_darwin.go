//go:build darwin

package collector

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"

	"github.com/duxet/netmon/live"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/pcap"
)

// macOS has no Netfilter, so traffic is observed by capturing packets from a
// network interface with libpcap (which ships with macOS) and aggregating
// them into flows ourselves. The upside is that it is not limited to the
// traffic of the machine it runs on: if the interface sees other hosts'
// traffic (the Mac routes or bridges it, or plugs into a mirrored port) it
// is accounted for as well.
//
// Opening a BPF device requires root, so netmon has to be started with sudo.

const (
	// Only the headers are needed to tell flows apart.
	snapLen = 256

	// How often a blocked read wakes up to check for shutdown. The kernel may
	// also hold packets back for this long before handing them over, which
	// doesn't matter as flows are only written out every flushInterval.
	readTimeout = 500 * time.Millisecond

	flushInterval = time.Minute

	// How long to wait before trying to capture again after a failure, such as
	// the interface going away when the Mac sleeps or switches network.
	reopenDelay = 5 * time.Second
)

type pcapCollector struct {
	db    *sql.DB
	opts  Options
	table *flowTable
	iface atomic.Value // string, the interface being captured on
}

// CollectTraffic records traffic to the database every minute, and tells the
// meter about it as it happens for the live view.
func CollectTraffic(db *sql.DB, meter *live.Meter, opts Options) (*Collector, error) {
	c := &pcapCollector{db: db, opts: opts, table: newFlowTable()}
	c.table.meter = meter
	c.table.ignore = opts.ignored

	// Open it before returning, so that problems such as missing permissions
	// are reported to the caller.
	handle, err := c.open()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	shutdownChan := make(chan bool, 1)
	captureDone := make(chan struct{})
	done := make(chan struct{})

	// Packets are already being counted, so unlike with Netfilter there is
	// nothing to save by measuring only while the live view is watched.
	go meter.Run(ctx, live.Interval)

	go func() {
		defer close(captureDone)
		c.captureLoop(ctx, handle)
	}()

	go func() {
		defer close(done)

		ticker := time.NewTicker(flushInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				c.refreshLocalPrefixes()
				c.flush()
			case <-shutdownChan:
				// Stop capturing before the final flush, so that it picks up
				// everything.
				cancel()
				<-captureDone
				c.flush()
				return
			}
		}
	}()

	return &Collector{shutdownChan: shutdownChan, done: done}, nil
}

func (c *pcapCollector) flush() {
	if err := c.table.flushTo(c.db); err != nil {
		log.Println("Failed to store flows:", err)
		return
	}
	c.opts.flushed(time.Now())
}

func (c *pcapCollector) interfaceName() string {
	name, _ := c.iface.Load().(string)
	return name
}

func (c *pcapCollector) refreshLocalPrefixes() {
	name := c.interfaceName()
	if name == "" {
		return
	}

	// The address of the interface changes when moving to another network. If
	// it can't be read for a moment, keep going with the previous one.
	if prefixes, err := localPrefixes(name); err == nil {
		c.table.SetLocalPrefixes(prefixes)
	}
}

// open starts a capture on the interface.
func (c *pcapCollector) open() (*pcap.Handle, error) {
	name, err := captureInterface(c.opts.Interface)
	if err != nil {
		return nil, err
	}

	// Promiscuous mode is what makes the traffic of other hosts visible when
	// the interface is connected to a mirrored port or a bridge.
	handle, err := pcap.OpenLive(name, snapLen, true, readTimeout)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "permission") {
			err = fmt.Errorf("%w (capturing packets requires root, run netmon with sudo)", err)
		}

		return nil, fmt.Errorf("failed to capture on %s: %w", name, err)
	}

	if err := handle.SetBPFFilter("ip or ip6"); err != nil {
		handle.Close()
		return nil, fmt.Errorf("failed to filter packets on %s: %w", name, err)
	}

	c.iface.Store(name)

	prefixes, err := localPrefixes(name)
	if err != nil {
		log.Printf("Failed to look up the networks of %s, hosts will have no MAC address: %v", name, err)
	}
	c.table.SetLocalPrefixes(prefixes)

	log.Printf("Capturing packets on %s...", name)

	return handle, nil
}

// captureInterface is the interface asked for, or else the one the default
// route goes through.
func captureInterface(requested string) (string, error) {
	if requested != "" {
		return requested, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	name, err := defaultRouteInterface(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to find the interface to capture on, set interface (or NETMON_INTERFACE) to choose one: %w", err)
	}

	return name, nil
}

// captureLoop captures until the context is cancelled, starting over whenever
// the capture fails.
func (c *pcapCollector) captureLoop(ctx context.Context, handle *pcap.Handle) {
	for {
		err := c.capture(ctx, handle)
		if ctx.Err() != nil {
			return
		}

		log.Printf("Capture on %s stopped: %v", c.interfaceName(), err)

		if handle = c.reopen(ctx); handle == nil {
			return
		}
	}
}

// reopen retries until the capture can be started again, and returns nil if
// the context is cancelled before that.
func (c *pcapCollector) reopen(ctx context.Context) *pcap.Handle {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(reopenDelay):
		}

		handle, err := c.open()
		if err == nil {
			return handle
		}

		log.Println("Failed to restart capture:", err)
	}
}

// capture reads packets from the handle, which it closes, until the context
// is cancelled (returning nil) or reading fails.
func (c *pcapCollector) capture(ctx context.Context, handle *pcap.Handle) error {
	defer handle.Close()

	linkType := handle.LinkType()
	options := gopacket.DecodeOptions{Lazy: true, NoCopy: true}

	for {
		data, info, err := handle.ZeroCopyReadPacketData()
		if ctx.Err() != nil {
			return nil
		}

		if err == pcap.NextErrorTimeoutExpired {
			continue
		}

		if err != nil {
			return err
		}

		// data is only valid until the next read, which is fine as the packet
		// is not kept around.
		packet := gopacket.NewPacket(data, linkType, options)

		if p, ok := decodePacket(packet, info.Length); ok {
			c.table.Add(p)
		}
	}
}
