package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/duxet/netmon/networks"
)

// NetworkRecord is a group of the local network's addresses, such as a VLAN.
type NetworkRecord struct {
	networks.Network
	CreatedAt time.Time
	UpdatedAt time.Time
}

const networkColumns = `id, name, vlan, color, prefixes, created_at, updated_at`

func scanNetwork(row interface{ Scan(...any) error }, n *NetworkRecord) error {
	var vlan sql.NullInt64
	var prefixes string
	if err := row.Scan(&n.ID, &n.Name, &vlan, &n.Color, &prefixes, &n.CreatedAt, &n.UpdatedAt); err != nil {
		return err
	}

	n.VLAN = nil
	if vlan.Valid {
		v := int(vlan.Int64)
		n.VLAN = &v
	}

	var values []string
	if err := json.Unmarshal([]byte(prefixes), &values); err != nil {
		return fmt.Errorf("network %d: prefixes: %w", n.ID, err)
	}
	n.Prefixes = make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return fmt.Errorf("network %d: %w", n.ID, err)
		}
		n.Prefixes = append(n.Prefixes, prefix.Masked())
	}
	return nil
}

func networkValues(n networks.Network) (vlan any, prefixes string, err error) {
	if n.VLAN != nil {
		vlan = *n.VLAN
	}
	values := make([]string, 0, len(n.Prefixes))
	for _, prefix := range n.Prefixes {
		values = append(values, prefix.Masked().String())
	}
	data, err := json.Marshal(values)
	return vlan, string(data), err
}

// ListNetworks returns all networks, oldest first.
func ListNetworks(db *sql.DB) ([]NetworkRecord, error) {
	rows, err := db.Query(`SELECT ` + networkColumns + ` FROM networks ORDER BY id`)
	records, err := collect(rows, err, func(rows *sql.Rows, n *NetworkRecord) error {
		return scanNetwork(rows, n)
	})
	if err != nil {
		return nil, fmt.Errorf("query networks: %w", err)
	}
	return records, nil
}

// NetworkMatcher returns a matcher for all networks, which finds the network
// an address belongs to.
func NetworkMatcher(db *sql.DB) (*networks.Matcher, error) {
	records, err := ListNetworks(db)
	if err != nil {
		return nil, err
	}
	list := make([]networks.Network, 0, len(records))
	for _, record := range records {
		list = append(list, record.Network)
	}
	return networks.NewMatcher(list), nil
}

// GetNetwork returns the network with the given ID, or ErrNotFound.
func GetNetwork(db *sql.DB, id int64) (NetworkRecord, error) {
	var n NetworkRecord
	err := scanNetwork(db.QueryRow(`SELECT `+networkColumns+` FROM networks WHERE id = ?`, id), &n)
	if errors.Is(err, sql.ErrNoRows) {
		return NetworkRecord{}, fmt.Errorf("network %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return NetworkRecord{}, fmt.Errorf("query network %d: %w", id, err)
	}
	return n, nil
}

// CreateNetwork adds a network, created at now, and returns it with its ID.
func CreateNetwork(db *sql.DB, n networks.Network, now time.Time) (NetworkRecord, error) {
	vlan, prefixes, err := networkValues(n)
	if err != nil {
		return NetworkRecord{}, fmt.Errorf("create network: %w", err)
	}

	var id int64
	err = db.QueryRow(`
		INSERT INTO networks (name, vlan, color, prefixes, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		RETURNING id`,
		n.Name, vlan, n.Color, prefixes, now, now).Scan(&id)
	if err != nil {
		return NetworkRecord{}, fmt.Errorf("create network: %w", err)
	}
	return GetNetwork(db, id)
}

// UpdateNetwork replaces the name, VLAN, color and blocks of the network
// with n's ID, updated at now, and returns it. It returns ErrNotFound when
// there is no such network.
func UpdateNetwork(db *sql.DB, n networks.Network, now time.Time) (NetworkRecord, error) {
	vlan, prefixes, err := networkValues(n)
	if err != nil {
		return NetworkRecord{}, fmt.Errorf("update network %d: %w", n.ID, err)
	}

	res, err := db.Exec(`
		UPDATE networks SET name = ?, vlan = ?, color = ?, prefixes = ?, updated_at = ?
		WHERE id = ?`,
		n.Name, vlan, n.Color, prefixes, now, n.ID)
	if err != nil {
		return NetworkRecord{}, fmt.Errorf("update network %d: %w", n.ID, err)
	}
	if count, err := res.RowsAffected(); err == nil && count == 0 {
		return NetworkRecord{}, fmt.Errorf("network %d: %w", n.ID, ErrNotFound)
	}
	return GetNetwork(db, n.ID)
}

// DeleteNetwork removes the network with the given ID, or returns
// ErrNotFound. Its devices stay, in whichever network holds their addresses
// next, if any.
func DeleteNetwork(db *sql.DB, id int64) error {
	res, err := db.Exec(`DELETE FROM networks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete network %d: %w", id, err)
	}
	if count, err := res.RowsAffected(); err == nil && count == 0 {
		return fmt.Errorf("network %d: %w", id, ErrNotFound)
	}
	return nil
}
