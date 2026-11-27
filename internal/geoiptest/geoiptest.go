// Package geoiptest writes small GeoLite2 databases for tests.
package geoiptest

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
)

// Country is what a test country database says about a network.
type Country struct {
	Network string
	Code    string
	Name    string
}

// ASN is what a test ASN database says about a network.
type ASN struct {
	Network      string
	Number       uint32
	Organization string
}

func write(t testing.TB, kind string, records map[string]mmdbtype.Map) string {
	t.Helper()
	tree, err := mmdbwriter.New(mmdbwriter.Options{DatabaseType: "GeoLite2-" + kind, RecordSize: 24, IncludeReservedNetworks: true})
	if err != nil {
		t.Fatal(err)
	}
	for network, record := range records {
		_, ipNet, err := net.ParseCIDR(network)
		if err != nil {
			t.Fatal(err)
		}
		if err := tree.Insert(ipNet, record); err != nil {
			t.Fatal(err)
		}
	}

	path := filepath.Join(t.TempDir(), "GeoLite2-"+kind+".mmdb")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := tree.WriteTo(file); err != nil {
		t.Fatal(err)
	}
	return path
}

// Countries writes a country database, and returns its path.
func Countries(t testing.TB, countries ...Country) string {
	records := map[string]mmdbtype.Map{}
	for _, c := range countries {
		records[c.Network] = mmdbtype.Map{
			"country": mmdbtype.Map{
				"iso_code": mmdbtype.String(c.Code),
				"names":    mmdbtype.Map{"en": mmdbtype.String(c.Name)},
			},
		}
	}
	return write(t, "Country", records)
}

// ASNs writes an ASN database, and returns its path.
func ASNs(t testing.TB, asns ...ASN) string {
	records := map[string]mmdbtype.Map{}
	for _, a := range asns {
		records[a.Network] = mmdbtype.Map{
			"autonomous_system_number":       mmdbtype.Uint32(a.Number),
			"autonomous_system_organization": mmdbtype.String(a.Organization),
		}
	}
	return write(t, "ASN", records)
}
