// Package live measures the bandwidth devices use right now.
//
// The collectors write what they observe to the database once a minute, which
// is too coarse to show what is happening at the moment. A Meter is fed by the
// collectors as traffic is observed and turns it into a rate per device every
// second, keeping the last couple of minutes of them for the web UI.
package live

import (
	"context"
	"net"
	"net/netip"
	"slices"
	"sync"
	"time"
)

const (
	// Interval is how often collectors close a sample.
	Interval = time.Second

	// History is how far back samples are kept.
	History = 2 * time.Minute

	// IdleAfter is how long after the last Watch the meter counts as unwatched.
	// Collectors that are costly to sample only do so while it is watched.
	IdleAfter = 10 * time.Second
)

// MAC is the link layer address of a device. Devices are told apart by it,
// like the rest of netmon does.
type MAC [6]byte

func (m MAC) String() string {
	return net.HardwareAddr(m[:]).String()
}

// Rate is traffic per second, seen from the device's side: Download is what it
// received and Upload what it sent, whichever side opened the connection.
type Rate struct {
	Download float64 // bytes per second
	Upload   float64 // bytes per second
}

// DeviceRate is the rate of one device during a sample.
type DeviceRate struct {
	MAC MAC
	// IP is an address the device used during the sample, preferably an IPv4
	// one. It is invalid when the collector did not tell.
	IP netip.Addr
	Rate
}

// Sample is the traffic of one interval. Devices that were idle in it are not
// listed, so a sample without devices is a second in which nothing happened.
type Sample struct {
	// Time is when the interval ended, to the millisecond.
	Time    time.Time
	Devices []DeviceRate
}

type counts struct {
	download, upload uint64
	ip               netip.Addr
}

// see remembers an address of the device, preferring IPv4 ones as they are the
// ones people recognize, and otherwise keeping the first so the choice does not
// flicker between samples.
func (c *counts) see(ip netip.Addr) {
	ip = ip.Unmap()
	if !ip.IsValid() {
		return
	}

	if !c.ip.IsValid() || (c.ip.Is6() && ip.Is4()) {
		c.ip = ip
	}
}

// A Meter turns the traffic collectors record into per-device rates. It is
// safe for concurrent use. Whoever feeds it also decides when an interval ends
// by calling Tick, which keeps the Meter free of clocks and timers.
type Meter struct {
	mu      sync.Mutex
	pending map[MAC]*counts // traffic since the last Tick
	last    time.Time       // when the last interval ended; zero before the first Tick
	samples []Sample        // oldest first
	watched time.Time
}

func NewMeter() *Meter {
	return &Meter{pending: make(map[MAC]*counts)}
}

// Record adds traffic of a device: download is what it received and upload what
// it sent. The address is optional.
func (m *Meter) Record(mac MAC, ip netip.Addr, download, upload uint64) {
	if download == 0 && upload == 0 {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.pending[mac]
	if !ok {
		c = &counts{}
		m.pending[mac] = c
	}

	c.download += download
	c.upload += upload
	c.see(ip)
}

// Tick ends the interval that started at the previous Tick, and stores the
// rates of the traffic recorded in it as a sample.
//
// The first Tick, and the first one after Reset, only marks the start of an
// interval: there is no telling how long ago what was recorded before it
// happened.
func (m *Meter) Tick(now time.Time) {
	now = now.Truncate(time.Millisecond)

	m.mu.Lock()
	defer m.mu.Unlock()

	pending := m.pending
	m.pending = make(map[MAC]*counts, len(pending))

	elapsed := now.Sub(m.last).Seconds()
	first := m.last.IsZero()
	m.last = now

	// A clock that stood still or went backwards leaves nothing to divide by.
	if first || elapsed <= 0 {
		return
	}

	devices := make([]DeviceRate, 0, len(pending))
	for mac, c := range pending {
		devices = append(devices, DeviceRate{
			MAC: mac,
			IP:  c.ip,
			Rate: Rate{
				Download: float64(c.download) / elapsed,
				Upload:   float64(c.upload) / elapsed,
			},
		})
	}
	slices.SortFunc(devices, func(a, b DeviceRate) int { return slices.Compare(a.MAC[:], b.MAC[:]) })

	m.samples = append(m.samples, Sample{Time: now, Devices: devices})

	keep := 0
	for keep < len(m.samples) && now.Sub(m.samples[keep].Time) > History {
		keep++
	}
	m.samples = m.samples[keep:]
}

// Samples returns the samples that ended after since, oldest first. The zero
// time returns everything remembered.
func (m *Meter) Samples(since time.Time) []Sample {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Samples are sorted by time, and new ones are what is asked for most.
	i, _ := slices.BinarySearchFunc(m.samples, since, func(s Sample, t time.Time) int {
		if s.Time.After(t) {
			return 1
		}
		return -1
	})

	// The devices of a sample are never modified, so they can be shared.
	return slices.Clone(m.samples[i:])
}

// Reset forgets everything, for collectors that stopped measuring for a while
// and so can't vouch for what the meter knows. Its next Tick starts over.
func (m *Meter) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.pending = make(map[MAC]*counts)
	m.last = time.Time{}
	m.samples = nil
}

// Watch tells the meter someone is looking at it at the given time.
func (m *Meter) Watch(now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.watched = now
}

// Watched reports whether Watch was called recently enough for someone to
// still be looking.
func (m *Meter) Watched(now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	return !m.watched.IsZero() && now.Sub(m.watched) < IdleAfter
}

// Run ticks the meter every interval until the context is done. It is for
// collectors that record continuously.
func (m *Meter) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	m.Tick(time.Now())

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			m.Tick(now)
		}
	}
}
