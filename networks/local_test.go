package networks

import (
	"net/netip"
	"testing"
)

func TestIsLocal(t *testing.T) {
	m := NewMatcher([]Network{{ID: 1, Name: "Home", Prefixes: mustPrefixes(t, "2001:db8:1::/48", "203.0.113.0/24")}})

	for addr, want := range map[string]bool{
		"192.168.1.10":       true,
		"10.20.0.1":          true,
		"172.16.5.4":         true,
		"fd00::1":            true,
		"127.0.0.1":          true,
		"::1":                true,
		"fe80::1":            true,
		"169.254.10.1":       true,
		"224.0.0.251":        true,
		"239.255.255.250":    true,
		"ff02::fb":           true,
		"255.255.255.255":    true,
		"0.0.0.0":            true,
		"::ffff:192.168.1.1": true,
		// Public addresses are local only in a network's blocks.
		"2001:db8:1:5::10": true,
		"203.0.113.7":      true,
		"2001:db8:2::1":    false,
		"8.8.8.8":          false,
		"2606:4700::1111":  false,
		"100.64.0.1":       false,
		"172.32.0.1":       false,
	} {
		if got := m.IsLocal(netip.MustParseAddr(addr)); got != want {
			t.Errorf("IsLocal(%s) = %v, want %v", addr, got, want)
		}
	}

	var none *Matcher
	if !none.IsLocal(netip.MustParseAddr("192.168.1.1")) || none.IsLocal(netip.MustParseAddr("203.0.113.7")) {
		t.Error("without networks, only the built-in ranges are local")
	}
	if none.IsLocal(netip.Addr{}) {
		t.Error("the zero address is local")
	}
}

func TestLocalTraffic(t *testing.T) {
	lan, nas, internet, own := netip.MustParseAddr("192.168.1.10"), netip.MustParseAddr("10.0.0.5"),
		netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("2001:db8:1::10")

	var l LocalTraffic
	if l.Ignored(lan, nas) {
		t.Error("the zero value ignores local traffic")
	}

	l.SetIgnored(true)
	if !l.Ignored(lan, nas) || !l.Ignored(nas, lan) {
		t.Error("traffic between private addresses is not ignored")
	}
	if l.Ignored(lan, internet) || l.Ignored(internet, lan) {
		t.Error("traffic to the internet is ignored")
	}
	if l.Ignored(lan, own) {
		t.Error("an address in no network's blocks counts as local")
	}

	l.SetMatcher(NewMatcher([]Network{{ID: 1, Prefixes: mustPrefixes(t, "2001:db8:1::/48")}}))
	if !l.Ignored(lan, own) {
		t.Error("an address in a network's blocks doesn't count as local")
	}

	l.SetIgnored(false)
	if l.Ignored(lan, nas) {
		t.Error("local traffic is ignored after turning it off")
	}

	var nilTraffic *LocalTraffic
	if nilTraffic.Ignored(lan, nas) {
		t.Error("a nil LocalTraffic ignores traffic")
	}
}
