package networks

import (
	"net/netip"
	"strconv"
	"strings"
	"testing"
)

func mustPrefixes(t *testing.T, values ...string) []netip.Prefix {
	t.Helper()
	prefixes, err := ParsePrefixes(values)
	if err != nil {
		t.Fatal(err)
	}
	return prefixes
}

func TestParsePrefix(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"192.168.20.0/24", "192.168.20.0/24"},
		// Host bits are cleared, so a router's own address with the mask works.
		{" 192.168.20.1/24 ", "192.168.20.0/24"},
		{"10.0.0.0/8", "10.0.0.0/8"},
		{"fd00:20::1/64", "fd00:20::/64"},
		{"2001:db8:1:2::/64", "2001:db8:1:2::/64"},
		// A single address is a block of one.
		{"192.168.1.50", "192.168.1.50/32"},
		{"fd00::5", "fd00::5/128"},
		{"0.0.0.0/0", "0.0.0.0/0"},
	} {
		got, err := ParsePrefix(tc.in)
		if err != nil || got.String() != tc.want {
			t.Errorf("ParsePrefix(%q) = %v, %v; want %s", tc.in, got, err, tc.want)
		}
	}

	for _, in := range []string{"", "lan", "192.168.1.0/33", "192.168.1/24", "192.168.1.0/", "fe80::1%eth0", "::ffff:192.168.1.0/120", "::ffff:192.168.1.1"} {
		if got, err := ParsePrefix(in); err == nil {
			t.Errorf("ParsePrefix(%q) = %v, want an error", in, got)
		}
	}
}

func TestParsePrefixes(t *testing.T) {
	got, err := ParsePrefixes([]string{"192.168.20.0/24", "", "192.168.20.9/24", "fd00:20::/64"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].String() != "192.168.20.0/24" || got[1].String() != "fd00:20::/64" {
		t.Errorf("prefixes = %v, want the repeat and the empty line dropped", got)
	}

	if _, err := ParsePrefixes([]string{" ", ""}); err == nil {
		t.Error("no prefixes: want an error")
	}
	if _, err := ParsePrefixes([]string{"192.168.1.0/24", "nope"}); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("invalid prefix: error = %v, want it to name the prefix", err)
	}

	many := make([]string, MaxPrefixes+1)
	for i := range many {
		many[i] = netip.AddrFrom4([4]byte{10, 0, byte(i), 0}).String() + "/24"
	}
	if _, err := ParsePrefixes(many); err == nil {
		t.Errorf("%d prefixes: want an error", len(many))
	}
}

func TestRangeOf(t *testing.T) {
	for _, tc := range []struct{ prefix, first, last string }{
		{"192.168.20.0/24", "192.168.20.0", "192.168.20.255"},
		{"10.0.0.0/8", "10.0.0.0", "10.255.255.255"},
		{"192.168.1.64/26", "192.168.1.64", "192.168.1.127"},
		{"192.168.1.5/32", "192.168.1.5", "192.168.1.5"},
		{"0.0.0.0/0", "0.0.0.0", "255.255.255.255"},
		{"fd00:20::/64", "fd00:20::", "fd00:20::ffff:ffff:ffff:ffff"},
		{"2001:db8::/33", "2001:db8::", "2001:db8:7fff:ffff:ffff:ffff:ffff:ffff"},
	} {
		r := RangeOf(netip.MustParsePrefix(tc.prefix))
		if r.First.String() != tc.first || r.Last.String() != tc.last {
			t.Errorf("RangeOf(%s) = %s-%s, want %s-%s", tc.prefix, r.First, r.Last, tc.first, tc.last)
		}
	}

	r := RangeOf(netip.MustParsePrefix("192.168.1.0/24"))
	if r.Contains(netip.MustParseAddr("::ffff:192.168.1.5")) {
		t.Error("an IPv4 range holds an IPv4-mapped IPv6 address, which the database wouldn't match")
	}
	if r.Contains(netip.MustParseAddr("c0a8:0105::")) {
		t.Error("an IPv4 range holds an IPv6 address with the same leading bytes")
	}
}

// testNetworks nests IoT inside Home and a camera inside IoT, and has a
// separate guest network with an IPv6 block too.
func testNetworks(t *testing.T) []Network {
	t.Helper()
	vlan := 20
	return []Network{
		{ID: 1, Name: "Home", Color: "blue", Prefixes: mustPrefixes(t, "10.0.0.0/8")},
		{ID: 2, Name: "IoT", Color: "orange", VLAN: &vlan, Prefixes: mustPrefixes(t, "10.0.20.0/24")},
		{ID: 3, Name: "Guests", Color: "aqua", Prefixes: mustPrefixes(t, "192.168.50.0/24", "fd00:50::/64")},
		{ID: 4, Name: "Camera", Color: "yellow", Prefixes: mustPrefixes(t, "10.0.20.99")},
	}
}

func TestMatchAndScopeAgree(t *testing.T) {
	m := NewMatcher(testNetworks(t))

	for _, tc := range []struct {
		addr string
		want int64 // 0 for no network
	}{
		{"10.0.0.1", 1},
		{"10.200.3.4", 1},
		{"10.0.20.1", 2},
		{"10.0.20.255", 2},
		{"10.0.21.0", 1},
		{"10.0.20.99", 4},
		{"192.168.50.7", 3},
		{"fd00:50::abcd", 3},
		{"fd00:51::1", 0},
		{"192.168.1.1", 0},
		{"8.8.8.8", 0},
		{"::ffff:10.0.0.1", 0},
	} {
		addr := netip.MustParseAddr(tc.addr)
		var got int64
		if n := m.Match(addr); n != nil {
			got = n.ID
		}
		if got != tc.want {
			t.Errorf("Match(%s) = network %d, want %d", tc.addr, got, tc.want)
		}

		// Each network's scope holds the address exactly when Match puts it there.
		for _, n := range m.Networks() {
			scope, ok := m.Scope(n.ID)
			if !ok {
				t.Fatalf("no scope for network %d", n.ID)
			}
			if in := scope.Contains(addr); in != (n.ID == tc.want) {
				t.Errorf("scope of %s contains %s = %v, want %v", n.Name, tc.addr, in, !in)
			}
		}
	}

	if _, ok := m.Scope(99); ok {
		t.Error("Scope of a missing network: want false")
	}
	if NewMatcher(nil).Match(netip.MustParseAddr("10.0.0.1")) != nil {
		t.Error("a matcher without networks matched an address")
	}
}

// A network inside another can hold a block of the outer one's again, which
// wins there as the more specific block.
func TestScopeWithBlockBackInside(t *testing.T) {
	m := NewMatcher([]Network{
		{ID: 1, Name: "Home", Prefixes: mustPrefixes(t, "10.0.0.0/8", "10.1.1.0/24")},
		{ID: 2, Name: "Lab", Prefixes: mustPrefixes(t, "10.1.0.0/16")},
	})
	home, _ := m.Scope(1)
	lab, _ := m.Scope(2)

	for _, tc := range []struct {
		addr      string
		home, lab bool
	}{
		{"10.0.0.1", true, false},
		{"10.1.0.1", false, true},
		{"10.1.1.1", true, false},
	} {
		addr := netip.MustParseAddr(tc.addr)
		if home.Contains(addr) != tc.home || lab.Contains(addr) != tc.lab {
			t.Errorf("%s: home %v, lab %v; want %v, %v", tc.addr, home.Contains(addr), lab.Contains(addr), tc.home, tc.lab)
		}
		want := int64(2)
		if tc.home {
			want = 1
		}
		if n := m.Match(addr); n == nil || n.ID != want {
			t.Errorf("Match(%s) = %v, want network %d", tc.addr, n, want)
		}
	}
}

// Should two networks have the same block anyway, the one with the lower ID
// gets it, in Match and Scope alike.
func TestSharedBlockGoesToLowerID(t *testing.T) {
	m := NewMatcher([]Network{
		{ID: 7, Name: "Later", Prefixes: mustPrefixes(t, "192.168.1.0/24")},
		{ID: 3, Name: "Earlier", Prefixes: mustPrefixes(t, "192.168.1.0/24")},
	})
	addr := netip.MustParseAddr("192.168.1.10")
	if n := m.Match(addr); n == nil || n.ID != 3 {
		t.Errorf("Match = %v, want network 3", n)
	}
	later, _ := m.Scope(7)
	earlier, _ := m.Scope(3)
	if later.Contains(addr) || !earlier.Contains(addr) {
		t.Errorf("scopes: later %v, earlier %v; want only the earlier", later.Contains(addr), earlier.Contains(addr))
	}
}

func TestConflict(t *testing.T) {
	networks := testNetworks(t)

	// The same block as another network is a conflict...
	n := Network{Name: "New", Prefixes: mustPrefixes(t, "172.16.0.0/12", "10.0.20.0/24")}
	prefix, other, ok := Conflict(n, networks)
	if !ok || prefix.String() != "10.0.20.0/24" || other.Name != "IoT" {
		t.Errorf("Conflict = %v, %v, %v; want 10.0.20.0/24 of IoT", prefix, other, ok)
	}

	// ...but blocks inside one another aren't, and a network doesn't
	// conflict with itself.
	n = Network{Name: "New", Prefixes: mustPrefixes(t, "10.0.30.0/24", "192.168.0.0/16")}
	if _, _, ok := Conflict(n, networks); ok {
		t.Error("nested blocks conflict")
	}
	if _, _, ok := Conflict(networks[2], networks); ok {
		t.Error("a network conflicts with itself")
	}
}

func TestNextColor(t *testing.T) {
	if got := NextColor(nil); got != Colors[0] {
		t.Errorf("first color = %s, want %s", got, Colors[0])
	}
	networks := testNetworks(t) // blue, orange, aqua, yellow
	if got := NextColor(networks); got != "magenta" {
		t.Errorf("next color = %s, want magenta", got)
	}
	networks[1].Color = "green"
	if got := NextColor(networks); got != "orange" {
		t.Errorf("next color = %s, want the freed orange", got)
	}

	var all []Network
	for _, color := range Colors {
		all = append(all, Network{Color: color})
	}
	if got := NextColor(all); !IsColor(got) {
		t.Errorf("with every color taken, next color = %q", got)
	}
	if IsColor("chartreuse") || !IsColor("violet") {
		t.Error("IsColor is wrong")
	}
}

func TestSuggest(t *testing.T) {
	m := NewMatcher([]Network{{ID: 1, Name: "IoT", Prefixes: mustPrefixes(t, "192.168.20.0/24")}})

	var addrs []netip.Addr
	for _, s := range []string{
		"192.168.1.10", "192.168.1.11", "192.168.1.200", // three in an unknown /24
		"192.168.20.5",           // in IoT already
		"10.0.0.5",               // one in another /24
		"fd00:1::5", "fd00:1::6", // two in a /64
		"fe80::1", "169.254.1.1", "127.0.0.1", "224.0.0.251", "0.0.0.0", "::ffff:192.168.9.9",
	} {
		addrs = append(addrs, netip.MustParseAddr(s))
	}
	addrs = append(addrs, netip.Addr{})

	got := Suggest(addrs, m)
	want := []string{"192.168.1.0/24:3", "fd00:1::/64:2", "10.0.0.0/24:1"}
	if len(got) != len(want) {
		t.Fatalf("suggestions = %v, want %v", got, want)
	}
	for i, s := range got {
		if g := s.Prefix.String() + ":" + strconv.Itoa(s.Devices); g != want[i] {
			t.Errorf("suggestion %d = %s, want %s", i, g, want[i])
		}
	}

	if got := Suggest(nil, nil); len(got) != 0 {
		t.Errorf("no addresses: suggestions = %v", got)
	}
}
