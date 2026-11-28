package lookup

import (
	"log/slog"
	"net/netip"

	"github.com/oschwald/geoip2-golang/v2"
)

// ASNs resolves IP addresses to the autonomous system that announces them.
// Without a database, and on a nil *ASNs, every lookup finds nothing. Reload
// opens the database again, such as after it was updated.
type ASNs struct {
	db *database
}

// OpenASNs opens the GeoLite2-ASN database at path. With an empty path, or a
// database that can't be opened, lookups find nothing until Reload opens one.
func OpenASNs(path string) *ASNs {
	l := &ASNs{db: &database{path: path}}
	if path == "" {
		return l
	}
	if err := l.db.reload(); err != nil {
		slog.Warn("ASN database not available, networks will not be shown", "path", path, "err", err)
	}
	return l
}

// Path is where the database is read from, empty when there is none.
func (l *ASNs) Path() string {
	if l == nil || l.db == nil {
		return ""
	}
	return l.db.path
}

// Reload opens the database at Path again, such as after a newer one replaced
// it. Lookups go on with the database that was open until it succeeds.
func (l *ASNs) Reload() error {
	if l == nil {
		return nil
	}
	return l.db.reload()
}

// Available reports whether a database is loaded, so lookups can succeed.
func (l *ASNs) Available() bool {
	return l != nil && l.db.available()
}

// Lookup returns the number and the organization of the address's autonomous
// system; ok is false when it is unknown (no database, a private address, or
// an address no system announces).
func (l *ASNs) Lookup(addr netip.Addr) (number uint, organization string, ok bool) {
	if l == nil || !addr.IsValid() {
		return 0, "", false
	}
	l.db.with(func(db *geoip2.Reader) {
		asn, err := db.ASN(addr.Unmap())
		if err != nil {
			slog.Debug("ASN lookup failed", "addr", addr, "err", err)
			return
		}
		number, organization = asn.AutonomousSystemNumber, asn.AutonomousSystemOrganization
	})
	if number == 0 {
		return 0, "", false
	}
	return number, organization, true
}

// Close closes the database.
func (l *ASNs) Close() error {
	if l == nil {
		return nil
	}
	return l.db.close()
}
