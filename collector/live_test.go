package collector

import (
	"testing"
	"time"

	"github.com/duxet/netmon/live"
)

var liveStart = time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)

// newLiveTable returns a table reporting to a meter whose first interval has
// started, so that the next Tick makes a sample.
func newLiveTable() (*flowTable, *live.Meter) {
	meter := live.NewMeter()
	meter.Tick(liveStart)

	table := newTestTable()
	table.meter = meter

	return table, meter
}

// endInterval ends the interval a seconds after liveStart and returns the
// devices that had traffic in it.
func endInterval(t *testing.T, meter *live.Meter, seconds int) map[live.MAC]live.DeviceRate {
	t.Helper()

	at := liveStart.Add(time.Duration(seconds) * time.Second)
	meter.Tick(at)

	samples := meter.Samples(at.Add(-time.Millisecond))
	if len(samples) != 1 {
		t.Fatalf("got %d samples ending at %s, want 1", len(samples), at)
	}

	devices := make(map[live.MAC]live.DeviceRate)
	for _, d := range samples[0].Devices {
		devices[d.MAC] = d
	}

	return devices
}

func TestLiveCountsBothEndsOfFlowsBetweenLocalHosts(t *testing.T) {
	table, meter := newLiveTable()

	// host opens a connection to peer and sends 1000 bytes; peer replies with 4000.
	table.Add(withHint(pkt(host, 50000, peer, 8080, 1000), hintSrcIsClient))
	table.Add(pkt(peer, 8080, host, 50000, 4000))

	devices := endInterval(t, meter, 1)
	if len(devices) != 2 {
		t.Fatalf("devices = %+v, want host and peer", devices)
	}

	if got := devices[live.MAC(hostMAC.addr)]; got.Download != 4000 || got.Upload != 1000 || got.IP != host {
		t.Errorf("host = %+v, want 4000 down and 1000 up at %s", got, host)
	}

	// What one end uploads the other downloads.
	if got := devices[live.MAC(peerMAC.addr)]; got.Download != 1000 || got.Upload != 4000 || got.IP != peer {
		t.Errorf("peer = %+v, want 1000 down and 4000 up at %s", got, peer)
	}
}

func TestLiveCountsOnlyTheLocalEndOfInternetFlows(t *testing.T) {
	table, meter := newLiveTable()

	table.Add(withHint(pkt(host, 50000, server, 443, 500), hintSrcIsClient))
	table.Add(pkt(server, 443, host, 50000, 9000))
	table.Add(pkt(server, 443, host, 50000, 9000))

	devices := endInterval(t, meter, 2)

	// The server is no device, and the gateway's address that its frames carry
	// must not make the gateway one.
	if len(devices) != 1 {
		t.Fatalf("devices = %+v, want only the local host", devices)
	}

	if got := devices[live.MAC(hostMAC.addr)]; got.Download != 9000 || got.Upload != 250 {
		t.Errorf("host = %v down / %v up per second, want 9000/250 (18000 and 500 bytes over 2s)", got.Download, got.Upload)
	}
}

func TestLiveCountsDownloadsOfFlowsTheCaptureSawMidway(t *testing.T) {
	table, meter := newLiveTable()

	// The opening of the connection was missed, so the table may think the
	// server is the client. The host still downloaded what the server sent.
	table.Add(pkt(server, 50000, host, 443, 3000))
	table.Add(pkt(host, 443, server, 50000, 200))

	devices := endInterval(t, meter, 1)

	if got := devices[live.MAC(hostMAC.addr)]; got.Download != 3000 || got.Upload != 200 {
		t.Errorf("host = %v down / %v up, want 3000/200", got.Download, got.Upload)
	}
}

func TestLiveOnlyCountsDevicesWithAKnownMAC(t *testing.T) {
	meter := live.NewMeter()
	meter.Tick(liveStart)

	// No local prefixes, so no host gets a MAC address, as when the networks
	// of the interface could not be looked up.
	table := newFlowTable()
	table.meter = meter

	table.Add(pkt(host, 50000, server, 443, 500))

	if devices := endInterval(t, meter, 1); len(devices) != 0 {
		t.Errorf("devices = %+v, want none", devices)
	}
}

func TestLiveIsOptional(t *testing.T) {
	table := newTestTable()

	table.Add(pkt(host, 50000, server, 443, 500)) // must not panic

	if rows := flushRows(t, table); len(rows) != 1 {
		t.Errorf("got %d rows, want the traffic to be recorded as usual", len(rows))
	}
}

func TestLiveDoesNotChangeWhatIsStored(t *testing.T) {
	with, _ := newLiveTable()
	without := newTestTable()

	for _, table := range []*flowTable{with, without} {
		table.Add(withHint(pkt(host, 50000, server, 443, 500), hintSrcIsClient))
		table.Add(pkt(server, 443, host, 50000, 9000))
	}

	a, b := flushRows(t, with), flushRows(t, without)
	if len(a) != 1 || len(b) != 1 || a[0] != b[0] {
		t.Errorf("rows differ with a meter: %+v vs %+v", a, b)
	}
}
