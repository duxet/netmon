//go:build linux

package collector

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/duxet/netmon/live"
	"github.com/ti-mo/conntrack"
	"github.com/vishvananda/netlink"
)

var (
	laptopMAC = live.MAC{0xa4, 0x83, 0xe7, 0x1c, 0x2f, 0x90}
	phoneMAC  = live.MAC{0x00, 0x11, 0x32, 0x9b, 0xc4, 0x01}

	laptopIP = netip.MustParseAddr("192.168.1.23")
	phoneIP  = netip.MustParseAddr("192.168.1.40")

	localHosts = map[netip.Addr]live.MAC{laptopIP: laptopMAC, phoneIP: phoneMAC}

	t0 = time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
)

// fakeSource returns what the test says the kernel knows.
type fakeSource struct {
	mu       sync.Mutex
	flows    []conntrack.Flow
	macs     map[netip.Addr]live.MAC
	err      error
	reads    int
	closings int
}

func (f *fakeSource) set(flows ...conntrack.Flow) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.flows = flows
}

func (f *fakeSource) Connections() ([]tracked, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	return trackAll(f.flows), f.err
}

func (f *fakeSource) Neighbors() (map[netip.Addr]live.MAC, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.macs, nil
}

func (f *fakeSource) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closings++
}

func (f *fakeSource) readCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads
}

func newSampler() (*liveSampler, *live.Meter, *fakeSource) {
	meter := live.NewMeter()
	source := &fakeSource{macs: localHosts}
	return newLiveSampler(meter, source), meter, source
}

func mustSample(t *testing.T, s *liveSampler, at time.Time) {
	t.Helper()
	if err := s.sample(at); err != nil {
		t.Fatal(err)
	}
}

// latest returns the devices of the newest sample.
func latest(t *testing.T, meter *live.Meter) map[live.MAC]live.DeviceRate {
	t.Helper()

	samples := meter.Samples(time.Time{})
	if len(samples) == 0 {
		t.Fatal("no samples")
	}

	devices := make(map[live.MAC]live.DeviceRate)
	for _, d := range samples[len(samples)-1].Devices {
		devices[d.MAC] = d
	}

	return devices
}

func TestSamplerFirstSampleOnlyRecordsCounters(t *testing.T) {
	s, meter, source := newSampler()

	// A long lived connection that has carried gigabytes since it opened.
	source.set(conn(1, "192.168.1.23", "140.82.121.4", 50000, 443, 5_000_000_000, 200_000_000))
	mustSample(t, s, t0)

	if samples := meter.Samples(time.Time{}); len(samples) != 0 {
		t.Fatalf("the first sample made %+v, want only a baseline", samples)
	}

	// Which makes the next one what the connection did during that second.
	source.set(conn(1, "192.168.1.23", "140.82.121.4", 50000, 443, 5_000_001_000, 200_000_100))
	mustSample(t, s, t0.Add(time.Second))

	got := latest(t, meter)[laptopMAC]
	if got.Download != 1000 || got.Upload != 100 || got.IP != laptopIP {
		t.Errorf("laptop = %+v, want 1000 down and 100 up at %s", got, laptopIP)
	}
}

func TestSamplerLeavesOutIgnoredConnections(t *testing.T) {
	s, meter, source := newSampler()
	s.ignore = func(src, dst netip.Addr) bool { return src.IsPrivate() && dst.IsPrivate() }

	source.set(
		conn(1, "192.168.1.23", "140.82.121.4", 50000, 443, 0, 0),
		conn(2, "192.168.1.23", phoneIP.String(), 50001, 8009, 0, 0),
	)
	mustSample(t, s, t0)
	source.set(
		conn(1, "192.168.1.23", "140.82.121.4", 50000, 443, 1000, 100),
		conn(2, "192.168.1.23", phoneIP.String(), 50001, 8009, 50000, 5000),
	)
	mustSample(t, s, t0.Add(time.Second))

	devices := latest(t, meter)
	if _, ok := devices[phoneMAC]; ok {
		t.Errorf("phone = %+v, want its traffic with the laptop left out", devices[phoneMAC])
	}
	if got := devices[laptopMAC]; got.Download != 1000 || got.Upload != 100 {
		t.Errorf("laptop = %+v, want only its traffic to the internet, 1000 down and 100 up", got)
	}
}

func TestSamplerRatesAreOverTheTimeBetweenSamples(t *testing.T) {
	s, meter, source := newSampler()

	source.set(conn(1, "192.168.1.23", "140.82.121.4", 50000, 443, 0, 0))
	mustSample(t, s, t0)

	source.set(conn(1, "192.168.1.23", "140.82.121.4", 50000, 443, 6000, 2000))
	mustSample(t, s, t0.Add(2*time.Second))

	if got := latest(t, meter)[laptopMAC]; got.Download != 3000 || got.Upload != 1000 {
		t.Errorf("laptop = %v down / %v up, want 3000/1000 (6000 and 2000 bytes over 2s)", got.Download, got.Upload)
	}
}

func TestSamplerCountsNewConnectionsInFull(t *testing.T) {
	s, meter, source := newSampler()

	mustSample(t, s, t0)

	source.set(conn(7, "192.168.1.23", "1.1.1.1", 50001, 53, 300, 60))
	mustSample(t, s, t0.Add(time.Second))

	if got := latest(t, meter)[laptopMAC]; got.Download != 300 || got.Upload != 60 {
		t.Errorf("laptop = %v down / %v up, want 300/60", got.Download, got.Upload)
	}
}

func TestSamplerSeesTrafficBetweenLocalDevicesFromBothSides(t *testing.T) {
	s, meter, source := newSampler()

	mustSample(t, s, t0)

	// The laptop sent 800 bytes to the phone, which answered with 5000.
	source.set(conn(1, "192.168.1.23", "192.168.1.40", 50000, 8080, 5000, 800))
	mustSample(t, s, t0.Add(time.Second))

	devices := latest(t, meter)
	if got := devices[laptopMAC]; got.Download != 5000 || got.Upload != 800 {
		t.Errorf("laptop = %v down / %v up, want 5000/800", got.Download, got.Upload)
	}
	if got := devices[phoneMAC]; got.Download != 800 || got.Upload != 5000 || got.IP != phoneIP {
		t.Errorf("phone = %+v, want 800 down and 5000 up at %s", got, phoneIP)
	}
}

func TestSamplerIgnoresHostsWithoutAMAC(t *testing.T) {
	s, meter, source := newSampler()

	mustSample(t, s, t0)

	// The router's own traffic, and a device that is not in the neighbor table.
	source.set(
		conn(1, "192.168.1.1", "9.9.9.9", 40000, 53, 100, 100),
		conn(2, "192.168.1.99", "9.9.9.9", 40001, 53, 100, 100),
	)
	mustSample(t, s, t0.Add(time.Second))

	if devices := latest(t, meter); len(devices) != 0 {
		t.Errorf("devices = %+v, want none", devices)
	}
}

func TestSamplerIgnoresDevicesTalkingToThemselves(t *testing.T) {
	s, meter, source := newSampler()
	source.macs = map[netip.Addr]live.MAC{
		laptopIP:                            laptopMAC,
		netip.MustParseAddr("192.168.1.24"): laptopMAC, // a second address of the same device
	}

	mustSample(t, s, t0)

	source.set(conn(1, "192.168.1.23", "192.168.1.24", 50000, 8080, 5000, 800))
	mustSample(t, s, t0.Add(time.Second))

	if devices := latest(t, meter); len(devices) != 0 {
		t.Errorf("devices = %+v, want none: the traffic never left the device", devices)
	}
}

func TestSamplerAddsUpConnectionsOfADevice(t *testing.T) {
	s, meter, source := newSampler()

	mustSample(t, s, t0)

	source.set(
		conn(1, "192.168.1.23", "140.82.121.4", 50000, 443, 1000, 100),
		conn(2, "192.168.1.23", "151.101.1.69", 50001, 443, 2000, 200),
		conn(3, "192.168.1.40", "151.101.1.69", 50002, 443, 4000, 400),
	)
	mustSample(t, s, t0.Add(time.Second))

	devices := latest(t, meter)
	if got := devices[laptopMAC]; got.Download != 3000 || got.Upload != 300 {
		t.Errorf("laptop = %v down / %v up, want 3000/300", got.Download, got.Upload)
	}
	if got := devices[phoneMAC]; got.Download != 4000 || got.Upload != 400 {
		t.Errorf("phone = %v down / %v up, want 4000/400", got.Download, got.Upload)
	}
}

func TestSamplerCountsAReusedConnectionIDAsANewConnection(t *testing.T) {
	s, meter, source := newSampler()

	source.set(conn(1, "192.168.1.23", "140.82.121.4", 50000, 443, 9000, 900))
	mustSample(t, s, t0)

	source.set(conn(1, "192.168.1.23", "1.1.1.1", 50001, 53, 80, 60))
	mustSample(t, s, t0.Add(time.Second))

	if got := latest(t, meter)[laptopMAC]; got.Download != 80 || got.Upload != 60 {
		t.Errorf("laptop = %v down / %v up, want the new connection's 80/60", got.Download, got.Upload)
	}
}

func TestSamplerCountsTheLastTrafficOfClosedConnections(t *testing.T) {
	s, meter, source := newSampler()

	source.set(conn(1, "192.168.1.23", "140.82.121.4", 50000, 443, 1000, 100))
	mustSample(t, s, t0)

	// The connection carries 500 more bytes down and closes before the next
	// sample, which therefore doesn't find it any more.
	s.destroyed(track(conn(1, "192.168.1.23", "140.82.121.4", 50000, 443, 1500, 100)))
	source.set()
	mustSample(t, s, t0.Add(time.Second))

	if got := latest(t, meter)[laptopMAC]; got.Download != 500 || got.Upload != 0 {
		t.Errorf("laptop = %v down / %v up, want the last 500 bytes down", got.Download, got.Upload)
	}
}

func TestSamplerCountsConnectionsThatOpenedAndClosedBetweenSamples(t *testing.T) {
	s, meter, _ := newSampler()

	mustSample(t, s, t0)

	s.destroyed(track(conn(9, "192.168.1.23", "1.1.1.1", 50001, 53, 120, 60)))
	mustSample(t, s, t0.Add(time.Second))

	if got := latest(t, meter)[laptopMAC]; got.Download != 120 || got.Upload != 60 {
		t.Errorf("laptop = %v down / %v up, want 120/60", got.Download, got.Upload)
	}
}

func TestSamplerCountsAClosedConnectionOnlyOnce(t *testing.T) {
	s, meter, source := newSampler()

	conn1 := conn(1, "192.168.1.23", "140.82.121.4", 50000, 443, 1000, 100)
	source.set(conn1)
	mustSample(t, s, t0)

	// The sample lists the connection one last time, then it is closed.
	grown := conn(1, "192.168.1.23", "140.82.121.4", 50000, 443, 1200, 100)
	source.set(grown)
	mustSample(t, s, t0.Add(time.Second))
	s.destroyed(track(conn(1, "192.168.1.23", "140.82.121.4", 50000, 443, 1250, 100)))
	source.set()
	mustSample(t, s, t0.Add(2*time.Second))

	samples := meter.Samples(time.Time{})
	if len(samples) != 2 {
		t.Fatalf("got %d samples, want 2", len(samples))
	}

	var down float64
	for _, sample := range samples {
		for _, d := range sample.Devices {
			down += d.Download
		}
	}
	if down != 250 {
		t.Errorf("downloaded %v bytes in total, want 250 (200 in the first second, 50 in the second)", down)
	}
}

func TestSamplerIgnoresClosedConnectionsWhileIdle(t *testing.T) {
	s, meter, source := newSampler()

	s.destroyed(track(conn(1, "192.168.1.23", "140.82.121.4", 50000, 443, 1000, 100)))
	mustSample(t, s, t0)
	source.set()
	mustSample(t, s, t0.Add(time.Second))

	if devices := latest(t, meter); len(devices) != 0 {
		t.Errorf("devices = %+v, want a connection closed before sampling began to be ignored", devices)
	}
}

func TestSamplerStartsOverAfterBeingIdle(t *testing.T) {
	s, meter, source := newSampler()

	source.set(conn(1, "192.168.1.23", "140.82.121.4", 50000, 443, 1000, 100))
	mustSample(t, s, t0)
	source.set(conn(1, "192.168.1.23", "140.82.121.4", 50000, 443, 2000, 200))
	mustSample(t, s, t0.Add(time.Second))

	s.idle()
	if source.closings == 0 {
		t.Error("idle did not release the source")
	}

	// A minute later the connection has carried a lot more. None of it can be
	// told to have happened in the last second.
	source.set(conn(1, "192.168.1.23", "140.82.121.4", 50000, 443, 90_000_000, 9_000_000))
	mustSample(t, s, t0.Add(time.Minute))

	if samples := meter.Samples(time.Time{}); len(samples) != 0 {
		t.Errorf("samples = %+v, want the old ones gone and no spike", samples)
	}

	source.set(conn(1, "192.168.1.23", "140.82.121.4", 50000, 443, 90_000_500, 9_000_050))
	mustSample(t, s, t0.Add(time.Minute+time.Second))

	if got := latest(t, meter)[laptopMAC]; got.Download != 500 || got.Upload != 50 {
		t.Errorf("laptop = %v down / %v up, want 500/50", got.Download, got.Upload)
	}
}

func TestSamplerKeepsItsBaselineAfterAFailure(t *testing.T) {
	s, meter, source := newSampler()

	source.set(conn(1, "192.168.1.23", "140.82.121.4", 50000, 443, 1000, 100))
	mustSample(t, s, t0)

	source.err = errors.New("netlink: operation not permitted")
	if err := s.sample(t0.Add(time.Second)); err == nil {
		t.Fatal("sample did not fail")
	}

	// The traffic of the second that failed is added to the next one, and the
	// rate is over both seconds.
	source.err = nil
	source.set(conn(1, "192.168.1.23", "140.82.121.4", 50000, 443, 3000, 300))
	mustSample(t, s, t0.Add(2*time.Second))

	if got := latest(t, meter)[laptopMAC]; got.Download != 1000 || got.Upload != 100 {
		t.Errorf("laptop = %v down / %v up per second, want 1000/100 (2000 and 200 bytes over 2s)", got.Download, got.Upload)
	}
}

func TestSamplerRunsOnlyWhileWatched(t *testing.T) {
	s, meter, source := newSampler()
	s.interval = 2 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.run(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	if reads := source.readCount(); reads != 0 {
		t.Errorf("read the kernel %d times while nobody was watching", reads)
	}

	source.set(conn(1, "192.168.1.23", "140.82.121.4", 50000, 443, 0, 0))
	meter.Watch(time.Now())

	deadline := time.After(5 * time.Second)
	for source.readCount() < 3 {
		select {
		case <-deadline:
			t.Fatal("did not sample while watched")
		case <-time.After(time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after the context was cancelled")
	}

	source.mu.Lock()
	closings := source.closings
	source.mu.Unlock()
	if closings == 0 {
		t.Error("the source was not released on shutdown")
	}
}

func TestNeighborMACs(t *testing.T) {
	macs := neighborMACs([]netlink.Neigh{
		// IPv4 addresses come in a 4 byte form, and in a 16 byte one.
		{IP: net.ParseIP("192.168.1.23").To4(), HardwareAddr: net.HardwareAddr(laptopMAC[:])},
		{IP: net.ParseIP("192.168.1.40"), HardwareAddr: net.HardwareAddr(phoneMAC[:])},

		{IP: net.ParseIP("fe80::1"), HardwareAddr: net.HardwareAddr(laptopMAC[:])},

		// What is in the table of a real machine, but is not a device.
		{IP: net.ParseIP("0.0.0.0"), HardwareAddr: net.HardwareAddr{0, 0, 0, 0, 0, 0}},
		{IP: net.ParseIP("192.168.1.77")},
		{IP: net.ParseIP("192.168.1.78"), HardwareAddr: net.HardwareAddr{0, 0, 0, 0, 0, 0}},
		{IP: net.ParseIP("224.0.0.251"), HardwareAddr: net.HardwareAddr{0x01, 0x00, 0x5e, 0x00, 0x00, 0xfb}},
		{IP: net.ParseIP("192.168.1.79"), HardwareAddr: net.HardwareAddr{0xa4, 0x83}},
		{IP: nil, HardwareAddr: net.HardwareAddr(laptopMAC[:])},
	})

	want := map[netip.Addr]live.MAC{
		laptopIP:                       laptopMAC,
		phoneIP:                        phoneMAC,
		netip.MustParseAddr("fe80::1"): laptopMAC,
	}

	if len(macs) != len(want) {
		t.Errorf("macs = %v, want %v", macs, want)
	}
	for addr, mac := range want {
		if macs[addr] != mac {
			t.Errorf("%s = %v, want %v", addr, macs[addr], mac)
		}
	}
}

func TestSamplerCountsPortForwardsForTheServer(t *testing.T) {
	s, meter, source := newSampler()

	mustSample(t, s, t0)

	// Someone on the internet downloads 5000 bytes from the laptop, through
	// port 8443 of the router's WAN address, forwarded to the laptop's 443.
	source.set(natConn(1, "203.0.113.7", "198.51.100.2", 51000, 8443, laptopIP.String(), 443, 5000, 800))
	mustSample(t, s, t0.Add(time.Second))

	if got := latest(t, meter)[laptopMAC]; got.Download != 800 || got.Upload != 5000 || got.IP != laptopIP {
		t.Errorf("laptop = %+v, want 800 down and 5000 up at %s", got, laptopIP)
	}
}
