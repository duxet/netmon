// Package networks groups the addresses of the local network into networks,
// such as VLANs or subnets, by the blocks of addresses (CIDR prefixes) they
// use. An address belongs to the network with the most specific block that
// holds it, as a router picks a route, so a network can sit inside another
// one's block: with 10.0.0.0/8 for "Home" and 10.0.20.0/24 for "IoT", the
// addresses of the IoT subnet belong to IoT and all others to Home.
package networks

import (
	"cmp"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

const (
	// MaxPrefixes is the most blocks of addresses a network can have.
	MaxPrefixes = 32
	// MinVLAN and MaxVLAN bound a VLAN ID (IEEE 802.1Q reserves 0 and 4095).
	MinVLAN = 1
	MaxVLAN = 4094
)

// Colors are the colors a network can be shown in, in the order new
// networks get them.
var Colors = []string{"blue", "orange", "aqua", "yellow", "magenta", "green", "violet", "red"}

// IsColor reports whether c is one of Colors.
func IsColor(c string) bool {
	return slices.Contains(Colors, c)
}

// NextColor returns the first color none of the networks has, or else the
// one after the last network's, so that new networks look different.
func NextColor(networks []Network) string {
	for _, color := range Colors {
		if !slices.ContainsFunc(networks, func(n Network) bool { return n.Color == color }) {
			return color
		}
	}
	return Colors[len(networks)%len(Colors)]
}

// Network is a group of the local network's addresses, such as a VLAN.
type Network struct {
	ID   int64
	Name string
	// VLAN is the network's 802.1Q VLAN ID, if it has one. It is only shown:
	// addresses are matched by Prefixes alone.
	VLAN *int
	// Color is one of Colors.
	Color string
	// Prefixes are the blocks of addresses the network uses, with their host
	// bits cleared.
	Prefixes []netip.Prefix
}

// ParsePrefix reads a block of addresses in CIDR notation, such as
// 192.168.20.0/24 or fd00:20::/64, or a single address, which is a block of
// one. Host bits are cleared, so 192.168.20.1/24 is 192.168.20.0/24.
func ParsePrefix(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	var prefix netip.Prefix
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("%q is not a block of addresses, such as 192.168.20.0/24", s)
		}
		prefix = p
	} else {
		addr, err := netip.ParseAddr(s)
		if err != nil || addr.Zone() != "" {
			return netip.Prefix{}, fmt.Errorf("%q is not a block of addresses, such as 192.168.20.0/24", s)
		}
		prefix = netip.PrefixFrom(addr, addr.BitLen())
	}

	// Flows store IPv4 addresses as such, never mapped into IPv6, so a
	// mapped block would never match.
	if prefix.Addr().Is4In6() {
		return netip.Prefix{}, fmt.Errorf("%q is an IPv4 block written as IPv6; write it as IPv4", s)
	}
	return prefix.Masked(), nil
}

// ParsePrefixes reads one to MaxPrefixes blocks of addresses with
// ParsePrefix, dropping repeats.
func ParsePrefixes(values []string) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		prefix, err := ParsePrefix(value)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(prefixes, prefix) {
			prefixes = append(prefixes, prefix)
		}
	}

	switch {
	case len(prefixes) == 0:
		return nil, errors.New("a network needs at least one block of addresses, such as 192.168.20.0/24")
	case len(prefixes) > MaxPrefixes:
		return nil, fmt.Errorf("a network can have at most %d blocks of addresses", MaxPrefixes)
	}
	return prefixes, nil
}

// Conflict returns a block of n that one of the other networks has too, and
// that network. Two networks can't have the same block, as an address in it
// would belong to both; blocks inside one another are fine.
func Conflict(n Network, others []Network) (netip.Prefix, *Network, bool) {
	for i := range others {
		if others[i].ID == n.ID {
			continue
		}
		for _, prefix := range n.Prefixes {
			if slices.Contains(others[i].Prefixes, prefix) {
				return prefix, &others[i], true
			}
		}
	}
	return netip.Prefix{}, nil, false
}

// Range is the addresses from First to Last, inclusive, of one family.
type Range struct {
	First, Last netip.Addr
}

// RangeOf returns the addresses of a block.
func RangeOf(prefix netip.Prefix) Range {
	prefix = prefix.Masked()
	first := prefix.Addr()

	bytes := first.AsSlice()
	for bit := prefix.Bits(); bit < first.BitLen(); bit++ {
		bytes[bit/8] |= 0x80 >> (bit % 8)
	}
	last, _ := netip.AddrFromSlice(bytes)

	return Range{First: first, Last: last}
}

// Contains reports whether addr is in the range. Addresses of the other
// family, IPv4 addresses mapped into IPv6 among them, never are.
func (r Range) Contains(addr netip.Addr) bool {
	return addr.BitLen() == r.First.BitLen() && addr.Compare(r.First) >= 0 && addr.Compare(r.Last) <= 0
}

// Block is a block of a network's addresses: Range without the more
// specific blocks of other networks in it, which hold their own addresses.
type Block struct {
	Range Range
	Holes []Range
}

// Contains reports whether addr is in the block.
func (b Block) Contains(addr netip.Addr) bool {
	if !b.Range.Contains(addr) {
		return false
	}
	for _, hole := range b.Holes {
		if hole.Contains(addr) {
			return false
		}
	}
	return true
}

// Scope is the addresses that belong to one network. Its blocks select the
// same addresses as Matcher.Match does for the network, so that traffic can
// be filtered by network in the database.
type Scope []Block

// Contains reports whether addr belongs to the network.
func (s Scope) Contains(addr netip.Addr) bool {
	return slices.ContainsFunc(s, func(b Block) bool { return b.Contains(addr) })
}

// Matcher finds the network an address belongs to.
type Matcher struct {
	networks []Network
	// entries are every network's blocks, the most specific first, and of
	// equally specific ones those of the network with the lowest ID.
	entries []entry
}

type entry struct {
	prefix  netip.Prefix
	network int
}

// NewMatcher returns a Matcher for the given networks.
func NewMatcher(networks []Network) *Matcher {
	m := &Matcher{networks: slices.Clone(networks)}
	for i, n := range m.networks {
		for _, prefix := range n.Prefixes {
			m.entries = append(m.entries, entry{prefix: prefix.Masked(), network: i})
		}
	}
	slices.SortStableFunc(m.entries, func(a, b entry) int {
		return cmp.Or(
			cmp.Compare(b.prefix.Bits(), a.prefix.Bits()),
			cmp.Compare(m.networks[a.network].ID, m.networks[b.network].ID),
		)
	})
	return m
}

// Networks returns the networks the matcher was made with.
func (m *Matcher) Networks() []Network {
	return m.networks
}

// Get returns the network with the given ID.
func (m *Matcher) Get(id int64) (*Network, bool) {
	for i := range m.networks {
		if m.networks[i].ID == id {
			return &m.networks[i], true
		}
	}
	return nil, false
}

// Match returns the network addr belongs to: the one with the most specific
// block holding it. It returns nil for addresses in no network's blocks.
func (m *Matcher) Match(addr netip.Addr) *Network {
	if m == nil || !addr.IsValid() {
		return nil
	}
	for _, e := range m.entries {
		if e.prefix.Contains(addr) {
			return &m.networks[e.network]
		}
	}
	return nil
}

// Scope returns the addresses that belong to the network with the given ID:
// its blocks, without the blocks of other networks that win over them in
// Match, the more specific ones (or, for a block two networks share, the one
// of the network with the lower ID).
func (m *Matcher) Scope(id int64) (Scope, bool) {
	n, ok := m.Get(id)
	if !ok {
		return nil, false
	}

	scope := make(Scope, 0, len(n.Prefixes))
	for _, prefix := range n.Prefixes {
		prefix = prefix.Masked()
		block := Block{Range: RangeOf(prefix)}
		for _, other := range m.networks {
			if other.ID == id {
				continue
			}
			for _, q := range other.Prefixes {
				q = q.Masked()
				inside := q.Bits() > prefix.Bits() && prefix.Contains(q.Addr())
				shared := q == prefix && other.ID < id
				if inside || shared {
					block.Holes = append(block.Holes, RangeOf(q))
				}
			}
		}
		scope = append(scope, block)
	}
	return scope, true
}
