package geoiptest

import (
	"net/netip"
	"testing"

	"github.com/duxet/netmon/lookup"
)

func TestDatabases(t *testing.T) {
	countries := lookup.OpenCountries(Countries(t, Country{"140.82.112.0/20", "US", "United States"}))
	defer countries.Close()
	if code, name, ok := countries.Lookup(netip.MustParseAddr("140.82.121.4")); !ok || code != "US" || name != "United States" {
		t.Errorf("country = %q %q %v", code, name, ok)
	}

	asns := lookup.OpenASNs(ASNs(t, ASN{"140.82.112.0/20", 36459, "GITHUB"}))
	defer asns.Close()
	if number, organization, ok := asns.Lookup(netip.MustParseAddr("140.82.121.4")); !ok || number != 36459 || organization != "GITHUB" {
		t.Errorf("ASN = %d %q %v", number, organization, ok)
	}
}
