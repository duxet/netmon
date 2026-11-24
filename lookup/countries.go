// Package lookup resolves IP addresses to countries (GeoIP) and hostnames
// (reverse DNS). One instance of each is shared by everything that needs
// them, such as the HTTP API.
package lookup

import (
	"log"
	"net/netip"

	"github.com/duxet/netmon/common"
	"github.com/oschwald/geoip2-golang/v2"
)

// Countries resolves IP addresses to ISO country codes. The database is
// opened once; without it, and on a nil *Countries, every lookup returns nil.
type Countries struct {
	db *geoip2.Reader
}

// OpenCountries opens the GeoLite2-Country database at path. With an empty
// path, or a database that can't be opened, lookups return nil.
func OpenCountries(path string) *Countries {
	if path == "" {
		return &Countries{}
	}

	db, err := geoip2.Open(path)
	if err != nil {
		log.Printf("GeoIP database %s not available, countries will not be shown: %v", path, err)
		return &Countries{}
	}

	return &Countries{db: db}
}

// Country returns the ISO 3166-1 alpha-2 code for the address, or nil when
// it is unknown (no database, a private address, or a failed lookup).
func (l *Countries) Country(ipAddress common.IPAddress) *string {
	if l == nil || l.db == nil || ipAddress.Addr == nil {
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

// Available reports whether a database is loaded, so lookups can succeed.
func (l *Countries) Available() bool {
	return l != nil && l.db != nil
}

// Lookup returns the ISO 3166-1 alpha-2 code and the English name of the
// address's country; ok is false when it is unknown.
func (l *Countries) Lookup(addr netip.Addr) (code, name string, ok bool) {
	if !l.Available() {
		return "", "", false
	}

	country, err := l.db.Country(addr.Unmap())
	if err != nil {
		log.Printf("GeoIP lookup for %s failed: %v", addr, err)
		return "", "", false
	}
	if country.Country.ISOCode == "" {
		return "", "", false
	}

	name = country.Country.Names.English
	if name == "" {
		name = country.Country.ISOCode
	}
	return country.Country.ISOCode, name, true
}

// Close closes the database.
func (l *Countries) Close() error {
	if l == nil || l.db == nil {
		return nil
	}
	return l.db.Close()
}
