// Package lookup resolves IP addresses to countries (GeoIP) and hostnames
// (reverse DNS). One instance of each is shared by everything that needs
// them, such as the HTTP API.
package lookup

import (
	"log/slog"
	"net/netip"

	"github.com/duxet/netmon/common"
	"github.com/oschwald/geoip2-golang/v2"
)

// Countries resolves IP addresses to ISO country codes. Without a database,
// and on a nil *Countries, every lookup returns nil. Reload opens the
// database again, such as after it was updated.
type Countries struct {
	db *database
}

// OpenCountries opens the GeoLite2-Country database at path. With an empty
// path, or a database that can't be opened, lookups return nil until Reload
// opens one.
func OpenCountries(path string) *Countries {
	l := &Countries{db: &database{path: path}}
	if path == "" {
		return l
	}
	if err := l.db.reload(); err != nil {
		slog.Warn("GeoIP database not available, countries will not be shown", "path", path, "err", err)
	}
	return l
}

// Path is where the database is read from, empty when there is none.
func (l *Countries) Path() string {
	if l == nil || l.db == nil {
		return ""
	}
	return l.db.path
}

// Reload opens the database at Path again, such as after a newer one replaced
// it. Lookups go on with the database that was open until it succeeds.
func (l *Countries) Reload() error {
	if l == nil {
		return nil
	}
	return l.db.reload()
}

func (l *Countries) lookup(addr netip.Addr) (code, name string, ok bool) {
	if l == nil {
		return "", "", false
	}
	l.db.with(func(db *geoip2.Reader) {
		country, err := db.Country(addr.Unmap())
		if err != nil {
			slog.Debug("GeoIP lookup failed", "addr", addr, "err", err)
			return
		}
		code, name = country.Country.ISOCode, country.Country.Names.English
	})
	if code == "" {
		return "", "", false
	}
	if name == "" {
		name = code
	}
	return code, name, true
}

// Country returns the ISO 3166-1 alpha-2 code for the address, or nil when
// it is unknown (no database, a private address, or a failed lookup).
func (l *Countries) Country(ipAddress common.IPAddress) *string {
	if ipAddress.Addr == nil {
		return nil
	}
	code, _, ok := l.lookup(ipAddress.Unmap())
	if !ok {
		return nil
	}
	return &code
}

// Available reports whether a database is loaded, so lookups can succeed.
func (l *Countries) Available() bool {
	return l != nil && l.db.available()
}

// Lookup returns the ISO 3166-1 alpha-2 code and the English name of the
// address's country; ok is false when it is unknown.
func (l *Countries) Lookup(addr netip.Addr) (code, name string, ok bool) {
	return l.lookup(addr)
}

// Close closes the database.
func (l *Countries) Close() error {
	if l == nil {
		return nil
	}
	return l.db.close()
}
