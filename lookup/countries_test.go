package lookup

import (
	"testing"

	"github.com/duxet/netmon/common"
)

func TestCountriesWithoutDatabase(t *testing.T) {
	addr, err := common.ParseIPAddress("140.82.121.4")
	if err != nil {
		t.Fatal(err)
	}

	for name, countries := range map[string]*Countries{
		"missing file": OpenCountries("does-not-exist.mmdb"),
		"no path":      OpenCountries(""),
		"nil":          nil,
	} {
		if country := countries.Country(*addr); country != nil {
			t.Errorf("%s: country = %q, want nil without a database", name, *country)
		}
		if err := countries.Close(); err != nil {
			t.Errorf("%s: Close = %v", name, err)
		}
	}
}
