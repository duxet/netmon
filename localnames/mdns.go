package localnames

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"golang.org/x/net/ipv4"
)

const (
	// mdnsTTL is how long an address announced over mDNS is taken to be the
	// device's. Records live for a few minutes, but devices announce
	// themselves seldom, and keep their addresses for longer.
	mdnsTTL = 24 * time.Hour
	// mdnsQueryEvery is how often devices are asked to announce their
	// services, which they answer with their names and addresses.
	mdnsQueryEvery = 15 * time.Minute
)

var mdnsGroup = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

// mdnsServices are the kinds of services asked for, the ones phones,
// computers, TVs, speakers, printers and smart home devices announce.
var mdnsServices = []string{
	"_device-info._tcp.local",
	"_companion-link._tcp.local",
	"_airplay._tcp.local",
	"_raop._tcp.local",
	"_googlecast._tcp.local",
	"_spotify-connect._tcp.local",
	"_hap._tcp.local",
	"_ipp._tcp.local",
	"_printer._tcp.local",
	"_smb._tcp.local",
	"_workstation._tcp.local",
	"_ssh._tcp.local",
	"_http._tcp.local",
	"_matter._tcp.local",
	"_esphomelib._tcp.local",
}

// ListenMDNS learns the names devices announce over multicast DNS, on every
// interface with a private IPv4 address, until ctx is done. With query, it
// asks them to now and then, rather than only listening to what they announce
// by themselves. It returns an error when it can't listen, such as when
// another program has port 5353 to itself.
func ListenMDNS(ctx context.Context, names *Names, query bool) error {
	conn, err := net.ListenMulticastUDP("udp4", nil, mdnsGroup)
	if err != nil {
		return fmt.Errorf("listen for mDNS: %w", err)
	}
	packets := ipv4.NewPacketConn(conn)
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	go func() {
		message, err := mdnsQuery()
		if err != nil {
			slog.Error("Can't make an mDNS query", "err", err)
			return
		}
		ticker := time.NewTicker(mdnsQueryEvery)
		defer ticker.Stop()
		for {
			// Interfaces come and go, such as a VLAN set up later, so they
			// are joined again every time; one that is already is an error,
			// which doesn't matter.
			for _, ifi := range lanInterfaces() {
				_ = packets.JoinGroup(&ifi, mdnsGroup)
				if query {
					if err := packets.SetMulticastInterface(&ifi); err == nil {
						if _, err := packets.WriteTo(message, nil, mdnsGroup); err != nil {
							slog.Debug("Can't send an mDNS query", "interface", ifi.Name, "err", err)
						}
					}
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	buf := make([]byte, 9000)
	for {
		n, _, _, err := packets.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("read mDNS: %w", err)
		}
		names.ObserveMDNS(buf[:n], time.Now())
	}
}

// lanInterfaces returns the interfaces that are up, can multicast and have a
// private IPv4 address.
func lanInterfaces() []net.Interface {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var lan []net.Interface
	for _, ifi := range interfaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			prefix, err := netip.ParsePrefix(addr.String())
			if err == nil && prefix.Addr().Is4() && prefix.Addr().IsPrivate() {
				lan = append(lan, ifi)
				break
			}
		}
	}
	return lan
}

// mdnsQuery returns a query for the PTR records of mdnsServices, which is
// answered over multicast, so that everyone listening learns the answers.
func mdnsQuery() ([]byte, error) {
	dns := layers.DNS{}
	for _, service := range mdnsServices {
		dns.Questions = append(dns.Questions, layers.DNSQuestion{
			Name: []byte(service), Type: layers.DNSTypePTR, Class: layers.DNSClassIN,
		})
	}
	dns.QDCount = uint16(len(dns.Questions))
	buf := gopacket.NewSerializeBuffer()
	if err := dns.SerializeTo(buf, gopacket.SerializeOptions{FixLengths: true}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ObserveMDNS learns names from an mDNS response: the address records of
// names in .local, in its answers and additional records alike, as a
// response to a query for services carries the addresses of the hosts that
// offer them.
func (n *Names) ObserveMDNS(payload []byte, at time.Time) {
	if n == nil {
		return
	}
	var dns layers.DNS
	if err := dns.DecodeFromBytes(payload, gopacket.NilDecodeFeedback); err != nil || !dns.QR {
		return
	}
	for _, record := range append(dns.Answers, dns.Additionals...) {
		if record.Type != layers.DNSTypeA && record.Type != layers.DNSTypeAAAA {
			continue
		}
		// A TTL of 0 says goodbye: the record is withdrawn, but the name is
		// still the device's.
		name := mdnsName(string(record.Name))
		addr, ok := netip.AddrFromSlice(record.IP)
		if name == "" || !ok || record.TTL == 0 {
			continue
		}
		n.Learn(Observation{Addr: addr, Name: name, Source: MDNS, Until: at.Add(mdnsTTL)})
	}
}

// mdnsName returns a host's name in .local, without the dot at the end, or
// empty for one that names no host: outside .local, or a random identifier,
// as Chromecasts and some printers go by.
func mdnsName(name string) string {
	name = strings.TrimSuffix(name, ".")
	host, ok := strings.CutSuffix(name, ".local")
	if !ok || host == "" || strings.Contains(host, ".") || isIdentifier(host) {
		return ""
	}
	return name
}

// isIdentifier reports whether a name is a UUID or a long string of hex
// digits, with or without dashes.
func isIdentifier(name string) bool {
	digits := 0
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
			digits++
		case r == '-':
		default:
			return false
		}
	}
	return digits >= 24
}
