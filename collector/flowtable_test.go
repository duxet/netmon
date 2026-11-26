package collector

import (
	"net"
	"net/netip"
	"testing"

	"github.com/duxet/netmon/live"
)

var (
	lan    = netip.MustParsePrefix("192.168.1.0/24")
	host   = netip.MustParseAddr("192.168.1.10")
	peer   = netip.MustParseAddr("192.168.1.20")
	server = netip.MustParseAddr("93.184.216.34")

	hostMAC = macAddr{[6]byte{0x02, 0, 0, 0, 0, 0x0a}, true}
	peerMAC = macAddr{[6]byte{0x02, 0, 0, 0, 0, 0x14}, true}
	gwMAC   = macAddr{[6]byte{0x02, 0, 0, 0, 0, 0x01}, true}
)

// pkt describes a packet from src to dst with the MACs it would have on the
// wire as seen from a host on the LAN. Remote hosts are reached through the
// gateway.
func pkt(src netip.Addr, sport uint16, dst netip.Addr, dport uint16, length uint64) packetInfo {
	return packetInfo{
		src:    packetEndpoint{endpoint{src, sport}, macOf(src)},
		dst:    packetEndpoint{endpoint{dst, dport}, macOf(dst)},
		proto:  6,
		length: length,
	}
}

// macOf returns the address a frame carries for ip: the host's own address on
// the LAN, but the gateway's for anything else, as the gateway is the next hop
// when sending to it and the previous one when receiving from it.
func macOf(ip netip.Addr) macAddr {
	switch ip {
	case host:
		return hostMAC
	case peer:
		return peerMAC
	}

	return gwMAC
}

func withHint(p packetInfo, h initiatorHint) packetInfo {
	p.hint = h
	return p
}

func newTestTable() *flowTable {
	t := newFlowTable()
	t.SetLocalPrefixes([]netip.Prefix{lan})

	return t
}

func flushRows(t *testing.T, ft *flowTable) []flowRow {
	t.Helper()

	rows, _ := ft.Flush()

	return rows
}

func findRow(t *testing.T, rows []flowRow, src, dst netip.Addr, port uint16) flowRow {
	t.Helper()

	for _, r := range rows {
		if r.srcIP == src && r.dstIP == dst && r.port == port {
			return r
		}
	}

	t.Fatalf("no flow %v -> %v:%d among %+v", src, dst, port, rows)

	return flowRow{}
}

func TestFlowDirectionFollowsHandshake(t *testing.T) {
	ft := newTestTable()

	ft.Add(withHint(pkt(host, 51234, server, 443, 60), hintSrcIsClient))
	ft.Add(withHint(pkt(server, 443, host, 51234, 60), hintDstIsClient))
	ft.Add(pkt(host, 51234, server, 443, 500))
	ft.Add(pkt(server, 443, host, 51234, 1500))
	ft.Add(pkt(server, 443, host, 51234, 1500))

	rows := flushRows(t, ft)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1: %+v", len(rows), rows)
	}

	r := findRow(t, rows, host, server, 443)
	if r.out != (counters{560, 2}) {
		t.Errorf("out = %+v, want 560 bytes in 2 packets", r.out)
	}
	if r.in != (counters{3060, 3}) {
		t.Errorf("in = %+v, want 3060 bytes in 3 packets", r.in)
	}
	if r.proto != 6 {
		t.Errorf("proto = %d, want 6", r.proto)
	}
}

// A capture started in the middle of a connection never sees it being opened.
func TestFlowDirectionGuessedFromPorts(t *testing.T) {
	// The first packet seen is the server answering.
	ft := newTestTable()
	ft.Add(pkt(server, 443, host, 51234, 1500))
	ft.Add(pkt(host, 51234, server, 443, 100))

	r := findRow(t, flushRows(t, ft), host, server, 443)
	if r.out != (counters{100, 1}) || r.in != (counters{1500, 1}) {
		t.Errorf("out = %+v, in = %+v", r.out, r.in)
	}
}

// A handshake is proof of who the client is, whatever the ports look like. The
// ports here are such that the guess alone would get it the wrong way round.
func TestFlowDirectionHintBeatsPortGuess(t *testing.T) {
	// The first packet seen is the SYN-ACK, so the sender is the server.
	ft := newTestTable()
	ft.Add(withHint(pkt(peer, 60000, host, 50000, 60), hintDstIsClient))

	r := findRow(t, flushRows(t, ft), host, peer, 60000)
	if r.in != (counters{60, 1}) {
		t.Errorf("in = %+v, want the SYN-ACK to count as server -> client", r.in)
	}

	// And the SYN itself.
	ft = newTestTable()
	ft.Add(withHint(pkt(host, 40000, peer, 50000, 60), hintSrcIsClient))

	r = findRow(t, flushRows(t, ft), host, peer, 50000)
	if r.out != (counters{60, 1}) {
		t.Errorf("out = %+v, want the SYN to count as client -> server", r.out)
	}
}

func TestFlowDirectionWithEqualPortsIsFirstSender(t *testing.T) {
	ft := newTestTable()
	ft.Add(pkt(host, 5353, peer, 5353, 100))
	ft.Add(pkt(peer, 5353, host, 5353, 200))

	r := findRow(t, flushRows(t, ft), host, peer, 5353)
	if r.out.bytes != 100 || r.in.bytes != 200 {
		t.Errorf("out = %+v, in = %+v", r.out, r.in)
	}
}

// Netfilter reports one record per service, not per connection. Each connection
// comes from a different port.
func TestFlowsToSameServiceAreMerged(t *testing.T) {
	ft := newTestTable()

	for _, port := range []uint16{50001, 50002, 50003} {
		ft.Add(pkt(host, port, server, 443, 100))
		ft.Add(pkt(server, 443, host, port, 1000))
	}
	ft.Add(pkt(host, 50001, server, 80, 7))

	rows := flushRows(t, ft)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (ports 443 and 80): %+v", len(rows), rows)
	}

	r := findRow(t, rows, host, server, 443)
	if r.out != (counters{300, 3}) || r.in != (counters{3000, 3}) {
		t.Errorf("out = %+v, in = %+v", r.out, r.in)
	}
}

func TestFlowsOfDifferentProtocolsAreSeparate(t *testing.T) {
	ft := newTestTable()

	udp := pkt(host, 50000, server, 443, 100)
	udp.proto = 17
	ft.Add(pkt(host, 50000, server, 443, 100))
	ft.Add(udp)

	if rows := flushRows(t, ft); len(rows) != 2 {
		t.Errorf("got %d rows, want a TCP one and a UDP one", len(rows))
	}
}

// Only hosts on the local network have MAC addresses worth recording. For
// everything else the frame carries the gateway's, and recording it would
// turn the gateway into a "client" of every site on the internet.
func TestMACsAreOnlyRecordedForLocalHosts(t *testing.T) {
	ft := newTestTable()

	ft.Add(pkt(host, 50000, server, 443, 100))
	ft.Add(pkt(server, 443, host, 50000, 100))
	ft.Add(pkt(host, 50001, peer, 22, 100))
	ft.Add(pkt(peer, 22, host, 50001, 100))

	rows := flushRows(t, ft)

	toServer := findRow(t, rows, host, server, 443)
	if toServer.srcMAC != hostMAC {
		t.Errorf("srcMAC = %v, want the host's", toServer.srcMAC)
	}
	if toServer.dstMAC.valid {
		t.Errorf("dstMAC = %v, a remote host has no MAC address of its own", toServer.dstMAC)
	}

	toPeer := findRow(t, rows, host, peer, 22)
	if toPeer.srcMAC != hostMAC || toPeer.dstMAC != peerMAC {
		t.Errorf("srcMAC = %v, dstMAC = %v, want both hosts' own", toPeer.srcMAC, toPeer.dstMAC)
	}
}

func TestBroadcastAndMulticastMACsAreNotAttributedToHosts(t *testing.T) {
	ft := newTestTable()

	// A directed broadcast address is inside the LAN prefix, but the frame is
	// addressed to everybody.
	bcast := pkt(host, 50000, netip.MustParseAddr("192.168.1.255"), 9, 50)
	bcast.dst.mac = macAddr{[6]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, true}
	ft.Add(bcast)

	mcast := pkt(host, 5353, netip.MustParseAddr("224.0.0.251"), 5353, 50)
	mcast.proto = 17
	mcast.dst.mac = macAddr{[6]byte{0x01, 0x00, 0x5e, 0x00, 0x00, 0xfb}, true}
	ft.Add(mcast)

	for _, r := range flushRows(t, ft) {
		if r.dstMAC.valid {
			t.Errorf("flow to %v has dstMAC %v", r.dstIP, r.dstMAC)
		}
		if r.srcMAC != hostMAC {
			t.Errorf("flow to %v has srcMAC %v, want the host's", r.dstIP, r.srcMAC)
		}
	}
}

func TestNoMACsWithoutLocalPrefixes(t *testing.T) {
	ft := newFlowTable()
	ft.Add(pkt(host, 50000, peer, 22, 100))

	for _, r := range flushRows(t, ft) {
		if r.srcMAC.valid || r.dstMAC.valid {
			t.Errorf("unexpected MACs %v %v", r.srcMAC, r.dstMAC)
		}
	}
}

func TestFlushReportsOnlyNewTraffic(t *testing.T) {
	ft := newTestTable()

	ft.Add(pkt(host, 50000, server, 443, 100))
	if rows := flushRows(t, ft); len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}

	// Nothing happened since.
	if rows := flushRows(t, ft); len(rows) != 0 {
		t.Errorf("got %d rows, want none: %+v", len(rows), rows)
	}

	// More traffic is reported as the delta, not the total.
	ft.Add(pkt(host, 50000, server, 443, 30))
	r := findRow(t, flushRows(t, ft), host, server, 443)
	if r.out != (counters{30, 1}) {
		t.Errorf("out = %+v, want only the new 30 bytes", r.out)
	}
}

// A flow keeps being oriented the same way from one interval to the next, even
// though the first packet of the later interval says nothing about it.
func TestFlowKeepsItsDirectionBetweenFlushes(t *testing.T) {
	ft := newTestTable()

	// Ports that would be guessed the wrong way round if it weren't for the SYN.
	ft.Add(withHint(pkt(host, 40000, peer, 50000, 60), hintSrcIsClient))
	flushRows(t, ft)

	ft.Add(pkt(peer, 50000, host, 40000, 900))

	r := findRow(t, flushRows(t, ft), host, peer, 50000)
	if r.in != (counters{900, 1}) {
		t.Errorf("in = %+v, out = %+v, want the reply to still count as server -> client", r.in, r.out)
	}
}

// A flow that stays quiet for a whole interval is forgotten, which is what
// keeps memory bounded.
func TestIdleFlowsAreForgotten(t *testing.T) {
	ft := newTestTable()

	ft.Add(pkt(host, 50000, server, 443, 100))
	flushRows(t, ft) // reported, and kept around
	flushRows(t, ft) // idle for an interval

	if n := len(ft.flows); n != 0 {
		t.Errorf("%d flows still tracked", n)
	}
}

func TestFlowTableLimit(t *testing.T) {
	ft := newTestTable()
	ft.maxFlows = 2

	ft.Add(pkt(host, 50001, server, 443, 100))
	ft.Add(pkt(host, 50002, server, 443, 100))
	ft.Add(pkt(host, 50003, server, 443, 100)) // no room
	ft.Add(pkt(host, 50003, server, 443, 100)) // no room
	ft.Add(pkt(server, 443, host, 50001, 100)) // existing flows keep counting

	rows, dropped := ft.Flush()
	if dropped != 2 {
		t.Errorf("dropped = %d, want 2", dropped)
	}

	r := findRow(t, rows, host, server, 443)
	if r.out != (counters{200, 2}) || r.in != (counters{100, 1}) {
		t.Errorf("out = %+v, in = %+v", r.out, r.in)
	}

	if _, dropped := ft.Flush(); dropped != 0 {
		t.Errorf("the counter was not reset, dropped = %d", dropped)
	}
}

func (m macAddr) hw() net.HardwareAddr { return m.addr[:] }

// ignoreLAN ignores the traffic between two addresses of the LAN.
func ignoreLAN(src, dst netip.Addr) bool {
	return lan.Contains(src) && lan.Contains(dst)
}

func TestIgnoredPacketsAreLeftOut(t *testing.T) {
	table, meter := newLiveTable()
	table.ignore = ignoreLAN

	table.Add(withHint(pkt(host, 50000, peer, 445, 1000), hintSrcIsClient))
	table.Add(pkt(peer, 445, host, 50000, 4000))
	table.Add(withHint(pkt(host, 50001, server, 443, 300), hintSrcIsClient))
	table.Add(pkt(server, 443, host, 50001, 700))

	rows := flushRows(t, table)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want only the one to the server", rows)
	}
	if row := findRow(t, rows, host, server, 443); row.in.bytes != 700 || row.out.bytes != 300 {
		t.Errorf("row = %+v, want 300 out and 700 in", row)
	}

	devices := endInterval(t, meter, 1)
	if _, ok := devices[live.MAC(peerMAC.addr)]; ok || len(devices) != 1 {
		t.Errorf("live devices = %+v, want only the host's traffic to the server", devices)
	}
	if got := devices[live.MAC(hostMAC.addr)]; got.Download != 700 || got.Upload != 300 {
		t.Errorf("host = %+v, want 700 down and 300 up", got)
	}
}
