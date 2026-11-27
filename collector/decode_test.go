package collector

import (
	"net"
	"net/netip"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
)

var (
	clientHW = net.HardwareAddr{0x02, 0x00, 0x00, 0x00, 0x00, 0x0a}
	gwHW     = net.HardwareAddr{0x02, 0x00, 0x00, 0x00, 0x00, 0x01}
)

func serialize(t *testing.T, ls ...gopacket.SerializableLayer) []byte {
	t.Helper()

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}

	if err := gopacket.SerializeLayers(buf, opts, ls...); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

func ethernet(src, dst net.HardwareAddr, typ layers.EthernetType) *layers.Ethernet {
	return &layers.Ethernet{SrcMAC: src, DstMAC: dst, EthernetType: typ}
}

func ipv4(src, dst string, proto layers.IPProtocol) *layers.IPv4 {
	return &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: proto,
		SrcIP:    net.ParseIP(src).To4(),
		DstIP:    net.ParseIP(dst).To4(),
	}
}

func decode(data []byte, link gopacket.Decoder, wireLen int) (packetInfo, bool) {
	// The same options the capture loop uses.
	pkt := gopacket.NewPacket(data, link, gopacket.DecodeOptions{Lazy: true, NoCopy: true})
	return decodePacket(pkt, wireLen)
}

func tcpOver4(t *testing.T, syn, ack bool, payload int) []byte {
	ip := ipv4("192.168.1.10", "93.184.216.34", layers.IPProtocolTCP)
	tcp := &layers.TCP{SrcPort: 51234, DstPort: 443, SYN: syn, ACK: ack}
	if err := tcp.SetNetworkLayerForChecksum(ip); err != nil {
		t.Fatal(err)
	}

	return serialize(t, ethernet(clientHW, gwHW, layers.EthernetTypeIPv4), ip, tcp, gopacket.Payload(make([]byte, payload)))
}

func TestDecodeTCP(t *testing.T) {
	data := tcpOver4(t, true, false, 100)

	p, ok := decode(data, layers.LinkTypeEthernet, len(data))
	if !ok {
		t.Fatal("packet was not decoded")
	}

	if p.src.ip != netip.MustParseAddr("192.168.1.10") || p.dst.ip != netip.MustParseAddr("93.184.216.34") {
		t.Errorf("unexpected addresses %v -> %v", p.src.ip, p.dst.ip)
	}
	if !p.src.ip.Is4() {
		t.Error("IPv4 addresses must be stored in their 4 byte form")
	}
	if p.proto != 6 || p.src.port != 51234 || p.dst.port != 443 {
		t.Errorf("unexpected proto/ports %d %d->%d", p.proto, p.src.port, p.dst.port)
	}
	// 20 bytes IP header + 20 bytes TCP header + 100 bytes of payload, but not
	// the Ethernet header.
	if p.length != 140 {
		t.Errorf("length = %d, want 140", p.length)
	}
	if p.hint != hintSrcIsClient {
		t.Errorf("a SYN means the sender is the client, got hint %d", p.hint)
	}
	if p.src.mac != newMACAddr(clientHW) || p.dst.mac != newMACAddr(gwHW) {
		t.Errorf("unexpected MACs %v -> %v", p.src.mac, p.dst.mac)
	}
}

func TestDecodeTCPHints(t *testing.T) {
	for _, tc := range []struct {
		name     string
		syn, ack bool
		want     initiatorHint
	}{
		{"syn", true, false, hintSrcIsClient},
		{"syn-ack", true, true, hintDstIsClient},
		{"ack", false, true, hintNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := tcpOver4(t, tc.syn, tc.ack, 0)

			p, ok := decode(data, layers.LinkTypeEthernet, len(data))
			if !ok || p.hint != tc.want {
				t.Errorf("ok=%v hint=%d, want hint %d", ok, p.hint, tc.want)
			}
		})
	}
}

func TestDecodeUDPOverIPv6(t *testing.T) {
	ip := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolUDP,
		HopLimit:   64,
		SrcIP:      net.ParseIP("fe80::1"),
		DstIP:      net.ParseIP("2001:db8::53"),
	}
	udp := &layers.UDP{SrcPort: 40000, DstPort: 53}
	if err := udp.SetNetworkLayerForChecksum(ip); err != nil {
		t.Fatal(err)
	}
	data := serialize(t, ethernet(clientHW, gwHW, layers.EthernetTypeIPv6), ip, udp, gopacket.Payload(make([]byte, 30)))

	p, ok := decode(data, layers.LinkTypeEthernet, len(data))
	if !ok {
		t.Fatal("packet was not decoded")
	}

	if p.proto != 17 || p.src.port != 40000 || p.dst.port != 53 {
		t.Errorf("unexpected proto/ports %d %d->%d", p.proto, p.src.port, p.dst.port)
	}
	if p.dst.ip != netip.MustParseAddr("2001:db8::53") {
		t.Errorf("unexpected destination %v", p.dst.ip)
	}
	// 40 bytes IPv6 header + 8 bytes UDP header + 30 bytes of payload.
	if p.length != 78 {
		t.Errorf("length = %d, want 78", p.length)
	}
	if p.hint != hintNone {
		t.Errorf("UDP says nothing about who is the client, got hint %d", p.hint)
	}
}

func TestDecodeICMPEcho(t *testing.T) {
	for _, tc := range []struct {
		name string
		typ  uint8
		want initiatorHint
	}{
		{"request", layers.ICMPv4TypeEchoRequest, hintSrcIsClient},
		{"reply", layers.ICMPv4TypeEchoReply, hintDstIsClient},
		{"other", layers.ICMPv4TypeDestinationUnreachable, hintNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			icmp := &layers.ICMPv4{TypeCode: layers.CreateICMPv4TypeCode(tc.typ, 0)}
			data := serialize(t, ethernet(clientHW, gwHW, layers.EthernetTypeIPv4), ipv4("192.168.1.10", "8.8.8.8", layers.IPProtocolICMPv4), icmp)

			p, ok := decode(data, layers.LinkTypeEthernet, len(data))
			if !ok {
				t.Fatal("packet was not decoded")
			}

			if p.proto != 1 || p.src.port != 0 || p.dst.port != 0 {
				t.Errorf("unexpected proto/ports %d %d->%d", p.proto, p.src.port, p.dst.port)
			}
			if p.hint != tc.want {
				t.Errorf("hint = %d, want %d", p.hint, tc.want)
			}
		})
	}
}

func TestDecodeICMPv6Echo(t *testing.T) {
	ip := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   64,
		SrcIP:      net.ParseIP("2001:db8::10"),
		DstIP:      net.ParseIP("2001:db8::1"),
	}
	icmp := &layers.ICMPv6{TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0)}
	if err := icmp.SetNetworkLayerForChecksum(ip); err != nil {
		t.Fatal(err)
	}
	data := serialize(t, ethernet(clientHW, gwHW, layers.EthernetTypeIPv6), ip, icmp, &layers.ICMPv6Echo{})

	p, ok := decode(data, layers.LinkTypeEthernet, len(data))
	if !ok {
		t.Fatal("packet was not decoded")
	}

	if p.proto != 58 || p.hint != hintSrcIsClient {
		t.Errorf("proto = %d, hint = %d", p.proto, p.hint)
	}
}

func TestDecodeIgnoresNonIP(t *testing.T) {
	arp := &layers.ARP{
		AddrType:          layers.LinkTypeEthernet,
		Protocol:          layers.EthernetTypeIPv4,
		HwAddressSize:     6,
		ProtAddressSize:   4,
		Operation:         layers.ARPRequest,
		SourceHwAddress:   clientHW,
		SourceProtAddress: net.ParseIP("192.168.1.10").To4(),
		DstHwAddress:      make([]byte, 6),
		DstProtAddress:    net.ParseIP("192.168.1.1").To4(),
	}
	bcast := net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	data := serialize(t, ethernet(clientHW, bcast, layers.EthernetTypeARP), arp)

	if _, ok := decode(data, layers.LinkTypeEthernet, len(data)); ok {
		t.Error("ARP must be ignored")
	}
}

// The capture only keeps the first bytes of each packet, but the traffic has to
// be accounted for in full.
func TestDecodeTruncatedCapture(t *testing.T) {
	data := tcpOver4(t, false, true, 1400)
	wireLen := len(data)

	// Keep Ethernet + IP + TCP headers only, as a small snaplen would.
	p, ok := decode(data[:14+20+20], layers.LinkTypeEthernet, wireLen)
	if !ok {
		t.Fatal("packet was not decoded")
	}

	if p.length != 1440 {
		t.Errorf("length = %d, want 1440", p.length)
	}
}

// With segmentation offload the stack leaves the IPv4 total length empty.
func TestDecodeZeroIPv4Length(t *testing.T) {
	data := tcpOver4(t, false, true, 4000)
	wireLen := len(data)

	data[14+2], data[14+3] = 0, 0

	// Truncated, too, as that's what the capture would give us.
	p, ok := decode(data[:14+20+20], layers.LinkTypeEthernet, wireLen)
	if !ok {
		t.Fatal("packet was not decoded")
	}

	if want := uint64(wireLen - 14); p.length != want {
		t.Errorf("length = %d, want %d", p.length, want)
	}
}

// Tunnels and the loopback interface have no link layer addresses.
func TestDecodeWithoutLinkLayerAddresses(t *testing.T) {
	ip := ipv4("10.8.0.2", "10.8.0.1", layers.IPProtocolUDP)
	udp := &layers.UDP{SrcPort: 1000, DstPort: 2000}
	if err := udp.SetNetworkLayerForChecksum(ip); err != nil {
		t.Fatal(err)
	}
	data := serialize(t, ip, udp, gopacket.Payload([]byte("hello")))

	p, ok := decode(data, layers.LinkTypeRaw, len(data))
	if !ok {
		t.Fatal("packet was not decoded")
	}

	if p.src.mac.valid || p.dst.mac.valid {
		t.Error("there are no MAC addresses in a raw IP packet")
	}
	if p.length != 20+8+5 {
		t.Errorf("length = %d, want 33", p.length)
	}
}

func TestDNSResponse(t *testing.T) {
	ip := ipv4("1.1.1.1", "192.168.1.23", layers.IPProtocolUDP)
	for _, test := range []struct {
		src, dst layers.UDPPort
		want     bool
	}{
		{53, 40000, true},
		{40000, 53, false},
		{443, 40000, false},
	} {
		udp := &layers.UDP{SrcPort: test.src, DstPort: test.dst}
		if err := udp.SetNetworkLayerForChecksum(ip); err != nil {
			t.Fatal(err)
		}
		data := serialize(t, ethernet(gwHW, clientHW, layers.EthernetTypeIPv4), ip, udp, gopacket.Payload([]byte("answer")))
		packet := gopacket.NewPacket(data, layers.LinkTypeEthernet, gopacket.Default)
		if got := dnsResponse(packet); (got != nil) != test.want || (test.want && string(got) != "answer") {
			t.Errorf("%d -> %d: payload %q, want one: %v", test.src, test.dst, got, test.want)
		}
	}
}
