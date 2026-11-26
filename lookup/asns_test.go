package lookup

import (
	"net/netip"
	"testing"
)

func TestASNsWithoutDatabase(t *testing.T) {
	addr := netip.MustParseAddr("140.82.121.4")

	for name, asns := range map[string]*ASNs{
		"missing file": OpenASNs("does-not-exist.mmdb"),
		"no path":      OpenASNs(""),
		"nil":          nil,
	} {
		if number, org, ok := asns.Lookup(addr); ok || asns.Available() {
			t.Errorf("%s: Lookup = %d, %q, %v; want nothing without a database", name, number, org, ok)
		}
		if err := asns.Close(); err != nil {
			t.Errorf("%s: Close = %v", name, err)
		}
	}
}
