package networks

import (
	"cmp"
	"net/netip"
	"slices"
)

// MaxSuggestions is the most suggestions Suggest returns.
const MaxSuggestions = 10

// Suggestion is a block of addresses devices use that no network has yet.
type Suggestion struct {
	Prefix netip.Prefix
	// Devices is the number of addresses given to Suggest that are in it.
	Devices int
}

// Suggest proposes blocks for the addresses of devices that belong to no
// network: the /24 of each IPv4 address and the /64 of each IPv6 one, as
// home networks usually are, most devices first. Loopback, link-local,
// multicast and unspecified addresses are left out, as they are no
// network's own.
func Suggest(addrs []netip.Addr, m *Matcher) []Suggestion {
	counts := map[netip.Prefix]int{}
	for _, addr := range addrs {
		if !addr.IsValid() || addr.Is4In6() || addr.Zone() != "" ||
			addr.IsLoopback() || addr.IsMulticast() || addr.IsUnspecified() ||
			addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() {
			continue
		}
		if m.Match(addr) != nil {
			continue
		}

		bits := 24
		if addr.Is6() {
			bits = 64
		}
		prefix, _ := addr.Prefix(bits)
		counts[prefix]++
	}

	suggestions := make([]Suggestion, 0, len(counts))
	for prefix, devices := range counts {
		suggestions = append(suggestions, Suggestion{Prefix: prefix, Devices: devices})
	}
	slices.SortFunc(suggestions, func(a, b Suggestion) int {
		return cmp.Or(
			cmp.Compare(b.Devices, a.Devices),
			// IPv4 first, then by address.
			cmp.Compare(a.Prefix.Addr().BitLen(), b.Prefix.Addr().BitLen()),
			a.Prefix.Addr().Compare(b.Prefix.Addr()),
		)
	})

	return suggestions[:min(len(suggestions), MaxSuggestions)]
}
