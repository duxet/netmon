package api

import (
	"time"

	"github.com/duxet/netmon/geoipupdate"
	"github.com/duxet/netmon/lookup"
	"github.com/gofiber/fiber/v3"
)

// GeoIPDatabaseDTO is how a GeoIP database is doing.
type GeoIPDatabaseDTO struct {
	// Edition is MaxMind's name of the database, such as GeoLite2-Country.
	Edition string
	Path    string
	// Available tells whether it is loaded, so lookups can succeed.
	Available bool
	// Built is when the database was built; null when unknown, and without
	// updates.
	Built *time.Time
	// CheckedAt is when a newer release was last looked for, null before
	// and without updates. Error is why the last update failed.
	CheckedAt *time.Time
	Error     string
}

// GeoIPStatusDTO is how the GeoIP databases are doing.
type GeoIPStatusDTO struct {
	// Updates tells whether netmon keeps the databases up to date.
	Updates   bool
	Databases []GeoIPDatabaseDTO
}

func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// registerGeoIPRoutes adds the endpoint that tells how the GeoIP databases
// and their updates are doing.
func registerGeoIPRoutes(app *fiber.App, countries *lookup.Countries, asns *lookup.ASNs, updater *geoipupdate.Updater) {
	app.Get("/api/geoip", func(c fiber.Ctx) error {
		statuses := map[string]geoipupdate.Status{}
		for _, status := range updater.Statuses() {
			statuses[status.Edition] = status
		}

		dto := GeoIPStatusDTO{Updates: updater != nil, Databases: []GeoIPDatabaseDTO{}}
		for _, db := range []struct {
			edition   string
			path      string
			available bool
		}{
			{"GeoLite2-Country", countries.Path(), countries.Available()},
			{"GeoLite2-ASN", asns.Path(), asns.Available()},
		} {
			if db.path == "" {
				continue
			}
			status := statuses[db.edition]
			dto.Databases = append(dto.Databases, GeoIPDatabaseDTO{
				Edition:   db.edition,
				Path:      db.path,
				Available: db.available,
				Built:     optionalTime(status.Built),
				CheckedAt: optionalTime(status.CheckedAt),
				Error:     status.Error,
			})
		}
		return c.JSON(dto)
	})
}
