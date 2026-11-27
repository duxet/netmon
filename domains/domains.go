// Package domains learns which names the addresses of remote hosts answered
// for when clients looked them up, from DNS answers, so that traffic to a
// CDN's address can be told by the name it was for, such as netflix.com.
package domains

import (
	"database/sql"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	// maxNames is how many names are kept per address, newest first.
	maxNames = 4
	// Retention is how long a name is kept after it was last seen.
	Retention = 30 * 24 * time.Hour
)

type entry struct {
	name string
	seen time.Time
}

type observation struct {
	addr netip.Addr
	name string
	at   time.Time
}

// Names knows the names addresses answered for. It is safe for concurrent
// use; a nil Names knows none.
type Names struct {
	mu      sync.RWMutex
	byAddr  map[netip.Addr][]entry
	pending []observation
}

func New() *Names {
	return &Names{byAddr: map[netip.Addr][]entry{}}
}

// Normalize returns a name as Names keeps it: lowercase, without the dot at
// the end, or empty for what isn't a name.
func Normalize(name string) string {
	name = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
	if name == "" || len(name) > 253 || strings.ContainsAny(name, " /\\<>") {
		return ""
	}
	return name
}

// Observe records that addr answered for name at a time.
func (n *Names) Observe(addr netip.Addr, name string, at time.Time) {
	name = Normalize(name)
	if n == nil || !addr.IsValid() || name == "" {
		return
	}
	addr = addr.Unmap()

	n.mu.Lock()
	defer n.mu.Unlock()
	n.remember(addr, name, at)
	n.pending = append(n.pending, observation{addr, name, at})
}

func (n *Names) remember(addr netip.Addr, name string, at time.Time) {
	entries := slices.DeleteFunc(n.byAddr[addr], func(e entry) bool { return e.name == name })
	entries = slices.Insert(entries, 0, entry{name, at})
	slices.SortStableFunc(entries, func(a, b entry) int { return b.seen.Compare(a.seen) })
	n.byAddr[addr] = entries[:min(len(entries), maxNames)]
}

// Names returns the names addr answered for, the one seen last first.
func (n *Names) Names(addr netip.Addr) []string {
	if n == nil || !addr.IsValid() {
		return nil
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	entries := n.byAddr[addr.Unmap()]
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.name)
	}
	return names
}

// Name returns the name addr answered for last, or empty.
func (n *Names) Name(addr netip.Addr) string {
	if names := n.Names(addr); len(names) > 0 {
		return names[0]
	}
	return ""
}

// Len returns how many addresses have names.
func (n *Names) Len() int {
	if n == nil {
		return 0
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	return len(n.byAddr)
}

// Load reads the names seen since a time from the database.
func (n *Names) Load(db *sql.DB, since time.Time) error {
	rows, err := db.Query(`SELECT ip, name, last_seen FROM dns_names WHERE last_seen >= ? ORDER BY last_seen`, since)
	if err != nil {
		return fmt.Errorf("load dns names: %w", err)
	}
	defer rows.Close()

	n.mu.Lock()
	defer n.mu.Unlock()
	for rows.Next() {
		var ip []byte
		var name string
		var seen time.Time
		if err := rows.Scan(&ip, &name, &seen); err != nil {
			return fmt.Errorf("load dns names: %w", err)
		}
		if addr, ok := netip.AddrFromSlice(ip); ok {
			n.remember(addr.Unmap(), name, seen)
		}
	}
	return rows.Err()
}

// Flush writes the names learned since the last flush to the database, and
// forgets those not seen for Retention in memory and in the database.
func (n *Names) Flush(db *sql.DB, now time.Time) error {
	if n == nil {
		return nil
	}
	n.mu.Lock()
	pending := n.pending
	n.pending = nil
	cutoff := now.Add(-Retention)
	for addr, entries := range n.byAddr {
		entries = slices.DeleteFunc(entries, func(e entry) bool { return e.seen.Before(cutoff) })
		if len(entries) == 0 {
			delete(n.byAddr, addr)
		} else {
			n.byAddr[addr] = entries
		}
	}
	n.mu.Unlock()

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("save dns names: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// The same name for the same address is seen many times; once is enough.
	latest := map[observation]time.Time{}
	for _, o := range pending {
		key := observation{addr: o.addr, name: o.name}
		if o.at.After(latest[key]) {
			latest[key] = o.at
		}
	}
	for key, at := range latest {
		_, err := tx.Exec(`
			INSERT INTO dns_names VALUES (?, ?, ?, ?)
			ON CONFLICT (ip, name) DO UPDATE SET last_seen = greatest(dns_names.last_seen, excluded.last_seen)`,
			key.addr.AsSlice(), key.name, at, at)
		if err != nil {
			return fmt.Errorf("save dns names: %w", err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM dns_names WHERE last_seen < ?`, cutoff); err != nil {
		return fmt.Errorf("expire dns names: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("save dns names: %w", err)
	}
	return nil
}
