package collector

import (
	"encoding/binary"
	"net"
	"net/netip"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
)

const ipv6HeaderLength = 40

// decodePacket extracts what the flow table needs from a captured packet. It
// returns false for packets that are not IP, such as ARP.
//
// wireLen is the length of the packet on the wire, which may be more than what
// was captured. It is only used for packets that don't state their own length.
func decodePacket(pkt gopacket.Packet, wireLen int) (packetInfo, bool) {
	var p packetInfo

	switch ip := pkt.NetworkLayer().(type) {
	case *layers.IPv4:
		src, srcOK := netip.AddrFromSlice(ip.SrcIP)
		dst, dstOK := netip.AddrFromSlice(ip.DstIP)
		if !srcOK || !dstOK {
			return p, false
		}

		p.src.ip, p.dst.ip = src, dst
		p.proto = uint8(ip.Protocol)

		// With TCP segmentation offload the stack hands over packets that are
		// bigger than the MTU and leaves the total length field empty. gopacket
		// papers over that by using the length of what it was given, but that
		// is wrong when the packet was truncated by the capture.
		if len(ip.Contents) >= 4 && binary.BigEndian.Uint16(ip.Contents[2:4]) == 0 {
			p.length = uint64(wireLength(pkt, wireLen))
		} else {
			p.length = uint64(ip.Length)
		}
	case *layers.IPv6:
		src, srcOK := netip.AddrFromSlice(ip.SrcIP)
		dst, dstOK := netip.AddrFromSlice(ip.DstIP)
		if !srcOK || !dstOK {
			return p, false
		}

		p.src.ip, p.dst.ip = src, dst
		p.proto = uint8(ip.NextHeader)

		if ip.Length == 0 {
			p.length = uint64(wireLength(pkt, wireLen))
		} else {
			p.length = uint64(ip.Length) + ipv6HeaderLength
		}
	default:
		return p, false
	}

	// Only the MAC addresses of the Ethernet frame are meaningful. Other kinds
	// of interfaces (tunnels, loopback) have none.
	if eth, ok := pkt.LinkLayer().(*layers.Ethernet); ok {
		p.src.mac = newMACAddr(eth.SrcMAC)
		p.dst.mac = newMACAddr(eth.DstMAC)
	}

	// Fragments other than the first one carry no transport header, so they end
	// up being accounted to port 0 of their protocol.
	switch t := pkt.TransportLayer().(type) {
	case *layers.TCP:
		p.proto = uint8(layers.IPProtocolTCP)
		p.src.port, p.dst.port = uint16(t.SrcPort), uint16(t.DstPort)

		if t.SYN && t.ACK {
			p.hint = hintDstIsClient
		} else if t.SYN {
			p.hint = hintSrcIsClient
		}
	case *layers.UDP:
		p.proto = uint8(layers.IPProtocolUDP)
		p.src.port, p.dst.port = uint16(t.SrcPort), uint16(t.DstPort)
	default:
		p.hint = icmpHint(pkt, &p)
	}

	return p, true
}

// icmpHint recognises ICMP packets, which have no ports, and tells which side
// started the exchange if it was a ping.
// dnsResponse returns the payload of a packet from a DNS server, port 53 over
// UDP, or nil for other packets.
func dnsResponse(pkt gopacket.Packet) []byte {
	udp, ok := pkt.Layer(layers.LayerTypeUDP).(*layers.UDP)
	if !ok || udp.SrcPort != 53 || len(udp.Payload) == 0 {
		return nil
	}
	return udp.Payload
}

func icmpHint(pkt gopacket.Packet, p *packetInfo) initiatorHint {
	if l, ok := pkt.Layer(layers.LayerTypeICMPv4).(*layers.ICMPv4); ok {
		p.proto = uint8(layers.IPProtocolICMPv4)

		switch l.TypeCode.Type() {
		case layers.ICMPv4TypeEchoRequest:
			return hintSrcIsClient
		case layers.ICMPv4TypeEchoReply:
			return hintDstIsClient
		}

		return hintNone
	}

	if l, ok := pkt.Layer(layers.LayerTypeICMPv6).(*layers.ICMPv6); ok {
		p.proto = uint8(layers.IPProtocolICMPv6)

		switch l.TypeCode.Type() {
		case layers.ICMPv6TypeEchoRequest:
			return hintSrcIsClient
		case layers.ICMPv6TypeEchoReply:
			return hintDstIsClient
		}
	}

	return hintNone
}

// wireLength returns the size of the IP packet given the size of the whole
// frame.
func wireLength(pkt gopacket.Packet, wireLen int) int {
	if link := pkt.LinkLayer(); link != nil {
		wireLen -= len(link.LayerContents())
	}

	return max(wireLen, 0)
}

func newMACAddr(hw net.HardwareAddr) macAddr {
	var m macAddr

	if len(hw) == len(m.addr) {
		copy(m.addr[:], hw)
		m.valid = true
	}

	return m
}
