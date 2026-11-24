package api

import (
	"log"

	"github.com/duxet/netmon/common"
	"github.com/oschwald/geoip2-golang/v2"
)

// countryLookup resolves IP addresses to ISO country codes. The database is
// opened once; without it every lookup returns nil.
type countryLookup struct {
	db *geoip2.Reader
}

func newCountryLookup(path string) *countryLookup {
	if path == "" {
		return &countryLookup{}
	}

	db, err := geoip2.Open(path)
	if err != nil {
		log.Printf("GeoIP database %s not available, countries will not be shown: %v", path, err)
		return &countryLookup{}
	}

	return &countryLookup{db: db}
}

// Country returns the ISO 3166-1 alpha-2 code for the address, or nil when
// it is unknown (no database, a private address, or a failed lookup).
func (l *countryLookup) Country(ipAddress common.IPAddress) *string {
	if l.db == nil || ipAddress.Addr == nil {
		return nil
	}

	country, err := l.db.Country(ipAddress.Unmap())
	if err != nil {
		log.Printf("GeoIP lookup for %s failed: %v", ipAddress, err)
		return nil
	}

	if country.Country.ISOCode == "" {
		return nil
	}

	return &country.Country.ISOCode
}

func (l *countryLookup) Close() error {
	if l.db == nil {
		return nil
	}
	return l.db.Close()
}
