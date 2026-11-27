package domains

import (
	"net/netip"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
)

// ObserveDNS learns names from a DNS response, the payload of a UDP packet
// from port 53, as captured: every address answers for the name it was
// asked under, and for the name of its own record, as at the end of a chain
// of CNAMEs.
func (n *Names) ObserveDNS(payload []byte, at time.Time) {
	if n == nil {
		return
	}
	var dns layers.DNS
	if err := dns.DecodeFromBytes(payload, gopacket.NilDecodeFeedback); err != nil || !dns.QR || dns.ResponseCode != layers.DNSResponseCodeNoErr {
		return
	}
	var asked string
	if len(dns.Questions) > 0 {
		asked = string(dns.Questions[0].Name)
	}
	for _, answer := range dns.Answers {
		if answer.Type != layers.DNSTypeA && answer.Type != layers.DNSTypeAAAA {
			continue
		}
		addr, ok := netip.AddrFromSlice(answer.IP)
		if !ok {
			continue
		}
		n.Observe(addr, string(answer.Name), at)
		if asked != "" {
			n.Observe(addr, asked, at)
		}
	}
}
