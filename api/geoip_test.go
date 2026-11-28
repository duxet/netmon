package api

import (
	"testing"

	"github.com/duxet/netmon/internal/geoiptest"
	"github.com/duxet/netmon/lookup"
)

func TestGeoIPStatusWithoutUpdates(t *testing.T) {
	countries := lookup.OpenCountries(geoiptest.Countries(t, geoiptest.Country{Network: "1.0.0.0/8", Code: "AU", Name: "Australia"}))
	defer countries.Close()
	asns := lookup.OpenASNs("missing/GeoLite2-ASN.mmdb")
	app, _ := newTestAppWith(t, Options{Countries: countries, ASNs: asns})

	var status GeoIPStatusDTO
	get(t, app, "/api/geoip", &status)
	if status.Updates || len(status.Databases) != 2 {
		t.Fatalf("/api/geoip = %+v, want two databases without updates", status)
	}
	country, asn := status.Databases[0], status.Databases[1]
	if country.Edition != "GeoLite2-Country" || !country.Available || country.CheckedAt != nil {
		t.Errorf("country database = %+v, want it available and never checked", country)
	}
	if asn.Edition != "GeoLite2-ASN" || asn.Available || asn.Path != "missing/GeoLite2-ASN.mmdb" {
		t.Errorf("ASN database = %+v, want it missing", asn)
	}
}

func TestGeoIPStatusWithoutDatabases(t *testing.T) {
	app, _ := newTestApp(t)

	var status GeoIPStatusDTO
	get(t, app, "/api/geoip", &status)
	if status.Updates || len(status.Databases) != 0 {
		t.Errorf("/api/geoip = %+v, want no databases", status)
	}
}
