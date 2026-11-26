package lookup

import (
	"log"
	"net/netip"

	"github.com/oschwald/geoip2-golang/v2"
)

// ASNs resolves IP addresses to the autonomous system that announces them.
// The database is opened once; without it, and on a nil *ASNs, every lookup
// finds nothing.
type ASNs struct {
	db *geoip2.Reader
}

// OpenASNs opens the GeoLite2-ASN database at path. With an empty path, or a
// database that can't be opened, lookups find nothing.
func OpenASNs(path string) *ASNs {
	if path == "" {
		return &ASNs{}
	}

	db, err := geoip2.Open(path)
	if err != nil {
		log.Printf("ASN database %s not available, networks will not be shown: %v", path, err)
		return &ASNs{}
	}

	return &ASNs{db: db}
}

// Available reports whether a database is loaded, so lookups can succeed.
func (l *ASNs) Available() bool {
	return l != nil && l.db != nil
}

// Lookup returns the number and the organization of the address's autonomous
// system; ok is false when it is unknown (no database, a private address, or
// an address no system announces).
func (l *ASNs) Lookup(addr netip.Addr) (number uint, organization string, ok bool) {
	if !l.Available() || !addr.IsValid() {
		return 0, "", false
	}

	asn, err := l.db.ASN(addr.Unmap())
	if err != nil {
		log.Printf("ASN lookup for %s failed: %v", addr, err)
		return 0, "", false
	}
	if asn.AutonomousSystemNumber == 0 {
		return 0, "", false
	}

	return asn.AutonomousSystemNumber, asn.AutonomousSystemOrganization, true
}

// Close closes the database.
func (l *ASNs) Close() error {
	if l == nil || l.db == nil {
		return nil
	}
	return l.db.Close()
}
