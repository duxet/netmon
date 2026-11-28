// Package localnames learns the names devices on the local network go by,
// from the DHCP server's leases and from mDNS, and keeps them per MAC
// address, so that a device keeps its name when its lease is gone, it gets
// another address or it is offline.
package localnames

import (
	"database/sql"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Source is where a name was learned from.
type Source string

const (
	// DHCP names are the ones devices sent their DHCP server.
	DHCP Source = "dhcp"
	// MDNS names are the ones devices announce over multicast DNS, such as
	// Johns-MacBook.local.
	MDNS Source = "mdns"
)

// rank orders the sources: a name from a source of a higher rank replaces
// one from a lower rank, never the other way around. A device's DHCP name is
// the one its DHCP server, and so reverse DNS, knows it by.
func (s Source) rank() int {
	switch s {
	case DHCP:
		return 2
	case MDNS:
		return 1
	}
	return 0
}

// Observation is a name a device went by.
type Observation struct {
	// MAC is the device's MAC address, when the source tells it (mDNS
	// doesn't).
	MAC net.HardwareAddr
	// Addr is the address the device had, if known.
	Addr netip.Addr
	Name string
	// Source is where the name is from.
	Source Source
	// Until is when Addr stops being the device's, such as when its DHCP
	// lease expires; zero is never. A time before now learns the name for
	// MAC alone.
	Until time.Time
}

type named struct {
	name   string
	source Source
}

// live is an address a device has now.
type live struct {
	named
	// mac is the device's MAC address, empty when unknown.
	mac   string
	until time.Time
}

func (l live) expired(now time.Time) bool {
	return !l.until.IsZero() && !now.Before(l.until)
}

// pending is a name to save, of the device with a MAC address or, when that
// isn't known, of the device that used an address last.
type pending struct {
	named
	at time.Time
}

// Names knows the names of local devices. It is safe for concurrent use; a
// nil Names knows none.
type Names struct {
	now func() time.Time

	mu sync.RWMutex
	// byMAC are the names saved in the database, along with the ones learned
	// since, by MAC address in its canonical form.
	byMAC map[string]named
	// byIP are the addresses devices have now, as the sources said.
	byIP map[netip.Addr]live
	// lastIP is which device, by MAC address, used an address last, for the
	// devices with a saved name.
	lastIP map[netip.Addr]string
	// toSave and toSaveByIP are what was learned since the last Flush.
	toSave     map[string]pending
	toSaveByIP map[netip.Addr]pending
}

// New returns a Names that knows no names yet.
func New() *Names {
	return &Names{
		now:        time.Now,
		byMAC:      map[string]named{},
		byIP:       map[netip.Addr]live{},
		lastIP:     map[netip.Addr]string{},
		toSave:     map[string]pending{},
		toSaveByIP: map[netip.Addr]pending{},
	}
}

// maxNameLength is the longest name kept, that of a DNS name.
const maxNameLength = 253

// Normalize returns a name as Names keeps it, without spaces around it or a
// dot at the end, or empty for what isn't a name, such as dnsmasq's "*" for
// a lease without one.
func Normalize(name string) string {
	name = strings.TrimSuffix(strings.TrimSpace(name), ".")
	if name == "" || name == "*" || len(name) > maxNameLength || strings.ContainsFunc(name, unicode.IsControl) {
		return ""
	}
	return name
}

// Learn records that a device went by a name.
func (n *Names) Learn(o Observation) {
	name := Normalize(o.Name)
	if n == nil || name == "" || o.Source.rank() == 0 {
		return
	}
	addr := o.Addr.Unmap()
	var mac string
	if len(o.MAC) > 0 {
		mac = o.MAC.String()
	}
	entry := named{name: name, source: o.Source}

	n.mu.Lock()
	defer n.mu.Unlock()
	now := n.now()

	if addr.IsValid() && (o.Until.IsZero() || now.Before(o.Until)) {
		current, ok := n.byIP[addr]
		if !ok || current.expired(now) || o.Source.rank() >= current.source.rank() || (mac != "" && mac != current.mac) {
			n.byIP[addr] = live{named: entry, mac: mac, until: o.Until}
		}
	}

	switch {
	case mac != "":
		if current, ok := n.byMAC[mac]; ok && (current == entry || current.source.rank() > entry.source.rank()) {
			return
		}
		n.byMAC[mac] = entry
		n.toSave[mac] = pending{named: entry, at: now}
	case addr.IsValid():
		if current, ok := n.toSaveByIP[addr]; ok && current.source.rank() > entry.source.rank() {
			return
		}
		n.toSaveByIP[addr] = pending{named: entry, at: now}
	}
}

// ByMAC returns the name of the device with a MAC address, or empty.
func (n *Names) ByMAC(mac net.HardwareAddr) string {
	if n == nil || len(mac) == 0 {
		return ""
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.byMAC[mac.String()].name
}

// ByIP returns the name of the device with an address, or empty: the name
// its source gave for the address, or the device's own name, whichever
// source ranks higher, when it is known which device has the address.
func (n *Names) ByIP(addr netip.Addr) string {
	if n == nil || !addr.IsValid() {
		return ""
	}
	addr = addr.Unmap()
	n.mu.RLock()
	defer n.mu.RUnlock()

	var best named
	mac := n.lastIP[addr]
	if l, ok := n.byIP[addr]; ok && !l.expired(n.now()) {
		best = l.named
		if l.mac != "" {
			mac = l.mac
		}
	}
	if device, ok := n.byMAC[mac]; ok && mac != "" && device.source.rank() >= best.source.rank() {
		best = device
	}
	return best.name
}

// Load reads the saved names from the database, along with the address
// each device used last, unless another device was seen with it since.
func (n *Names) Load(db *sql.DB) error {
	if n == nil {
		return nil
	}
	rows, err := db.Query(`
		SELECT h.mac, h.name, h.source, d.last_ip
		FROM device_hostnames h
		LEFT JOIN devices d ON d.mac = h.mac AND NOT EXISTS (
			SELECT 1 FROM devices o WHERE o.last_ip = d.last_ip AND o.last_seen > d.last_seen
		)
	`)
	if err != nil {
		return fmt.Errorf("load device hostnames: %w", err)
	}
	defer rows.Close()

	byMAC := map[string]named{}
	lastIP := map[netip.Addr]string{}
	for rows.Next() {
		var mac, ip []byte
		var e named
		if err := rows.Scan(&mac, &e.name, &e.source, &ip); err != nil {
			return fmt.Errorf("load device hostnames: %w", err)
		}
		key := net.HardwareAddr(mac).String()
		byMAC[key] = e
		if addr, ok := netip.AddrFromSlice(ip); ok {
			lastIP[addr.Unmap()] = key
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("load device hostnames: %w", err)
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	// What was learned while the query ran isn't saved yet, so is kept.
	for mac, p := range n.toSave {
		byMAC[mac] = p.named
	}
	n.byMAC = byMAC
	n.lastIP = lastIP
	return nil
}

// Flush saves the names learned since the last flush to the database, and
// then reads them all back, which brings in the changes made there, such as
// forgotten or merged devices. A name learned for an address alone is the
// name of the device that used the address last.
func (n *Names) Flush(db *sql.DB) error {
	if n == nil {
		return nil
	}
	n.mu.Lock()
	toSave, toSaveByIP := n.toSave, n.toSaveByIP
	n.toSave, n.toSaveByIP = map[string]pending{}, map[netip.Addr]pending{}
	now := n.now()
	for addr, l := range n.byIP {
		if l.expired(now) {
			delete(n.byIP, addr)
		}
	}
	n.mu.Unlock()

	if len(toSaveByIP) > 0 {
		macs, err := devicesByIP(db)
		if err != nil {
			n.requeue(toSave, toSaveByIP)
			return err
		}
		for addr, p := range toSaveByIP {
			mac, ok := macs[addr]
			if !ok {
				continue
			}
			if current, ok := toSave[mac]; ok && current.source.rank() > p.source.rank() {
				continue
			}
			toSave[mac] = p
		}
	}

	if len(toSave) > 0 {
		if err := save(db, toSave); err != nil {
			n.requeue(toSave, nil)
			return err
		}
	}
	return n.Load(db)
}

// requeue puts back what a failed flush didn't save, unless something newer
// was learned meanwhile.
func (n *Names) requeue(toSave map[string]pending, toSaveByIP map[netip.Addr]pending) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for mac, p := range toSave {
		if _, ok := n.toSave[mac]; !ok {
			n.toSave[mac] = p
		}
	}
	for addr, p := range toSaveByIP {
		if _, ok := n.toSaveByIP[addr]; !ok {
			n.toSaveByIP[addr] = p
		}
	}
}

// devicesByIP returns which device, by MAC address, used each address last.
func devicesByIP(db *sql.DB) (map[netip.Addr]string, error) {
	rows, err := db.Query(`SELECT mac, last_ip FROM devices ORDER BY last_seen`)
	if err != nil {
		return nil, fmt.Errorf("query devices: %w", err)
	}
	defer rows.Close()
	macs := map[netip.Addr]string{}
	for rows.Next() {
		var mac, ip []byte
		if err := rows.Scan(&mac, &ip); err != nil {
			return nil, fmt.Errorf("query devices: %w", err)
		}
		if addr, ok := netip.AddrFromSlice(ip); ok {
			// Seen last, so the device that has the address now wins.
			macs[addr.Unmap()] = net.HardwareAddr(mac).String()
		}
	}
	return macs, rows.Err()
}

func save(db *sql.DB, toSave map[string]pending) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("save device hostnames: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for key, p := range toSave {
		mac, err := net.ParseMAC(key)
		if err != nil {
			continue
		}
		// An mDNS name doesn't replace a DHCP one.
		_, err = tx.Exec(`
			INSERT INTO device_hostnames VALUES (?, ?, ?, ?)
			ON CONFLICT (mac) DO UPDATE SET name = excluded.name, source = excluded.source, updated_at = excluded.updated_at
			WHERE NOT (device_hostnames.source = 'dhcp' AND excluded.source <> 'dhcp')
		`, []byte(mac), p.name, string(p.source), p.at)
		if err != nil {
			return fmt.Errorf("save device hostnames: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("save device hostnames: %w", err)
	}
	return nil
}
