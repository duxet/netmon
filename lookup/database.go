package lookup

import (
	"sync"

	"github.com/oschwald/geoip2-golang/v2"
)

// database is a GeoLite2 database that can be opened again, such as after a
// newer one replaced the file, while lookups go on. It is safe for concurrent
// use.
type database struct {
	path string

	mu sync.RWMutex
	db *geoip2.Reader
}

// with calls fn with the open database, and reports whether there was one.
// The database stays open until fn returns.
func (d *database) with(fn func(*geoip2.Reader)) bool {
	if d == nil {
		return false
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.db == nil {
		return false
	}
	fn(d.db)
	return true
}

func (d *database) available() bool {
	return d.with(func(*geoip2.Reader) {})
}

// reload opens the file at the database's path again, such as after it was
// replaced. When that fails, the database that was open stays open.
func (d *database) reload() error {
	if d == nil || d.path == "" {
		return nil
	}
	db, err := geoip2.Open(d.path)
	if err != nil {
		return err
	}

	d.mu.Lock()
	old := d.db
	d.db = db
	d.mu.Unlock()

	// Lookups hold the read lock, so none is still using the old database.
	if old != nil {
		_ = old.Close()
	}
	return nil
}

func (d *database) close() error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.db == nil {
		return nil
	}
	err := d.db.Close()
	d.db = nil
	return err
}
