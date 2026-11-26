package networks

import (
	"net/netip"
	"sync/atomic"
)

// broadcast is the IPv4 limited broadcast address, which never leaves the
// local network.
var broadcast = netip.AddrFrom4([4]byte{255, 255, 255, 255})

// IsLocal reports whether addr is a local one: in the private ranges
// (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16 and fc00::/7), loopback,
// link-local, multicast, broadcast or unspecified, or in the blocks of one of
// the matcher's networks, which is how local addresses outside the private
// ranges, such as public IPv6 prefixes, count too. A nil matcher has no
// networks.
func (m *Matcher) IsLocal(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() {
		return false
	}
	if addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() ||
		addr.IsMulticast() || addr.IsUnspecified() || addr == broadcast {
		return true
	}
	return m.Match(addr) != nil
}

// LocalTraffic decides which traffic to leave out for being between local
// addresses (see [Matcher.IsLocal]), when that is turned on. It is safe for
// concurrent use; the zero value is turned off and knows no networks.
type LocalTraffic struct {
	ignore  atomic.Bool
	matcher atomic.Pointer[Matcher]
}

// SetIgnored turns leaving out local traffic on or off.
func (l *LocalTraffic) SetIgnored(ignore bool) {
	l.ignore.Store(ignore)
}

// SetMatcher sets the networks whose blocks count as local, on top of the
// private ranges.
func (l *LocalTraffic) SetMatcher(m *Matcher) {
	l.matcher.Store(m)
}

// Ignored reports whether traffic between src and dst is to be left out: it
// is turned on and both addresses are local.
func (l *LocalTraffic) Ignored(src, dst netip.Addr) bool {
	if l == nil || !l.ignore.Load() {
		return false
	}
	m := l.matcher.Load()
	return m.IsLocal(src) && m.IsLocal(dst)
}
