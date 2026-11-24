package storage

import (
	"database/sql"
	"fmt"
	"net"
)

// GetDeviceNames returns the names users gave their devices, by MAC address
// in its canonical form (net.HardwareAddr.String).
func GetDeviceNames(db *sql.DB) (map[string]string, error) {
	rows, err := db.Query(`SELECT mac, name FROM device_names`)
	type entry struct {
		mac  []byte
		name string
	}
	entries, err := collect(rows, err, func(rows *sql.Rows, e *entry) error {
		return rows.Scan(&e.mac, &e.name)
	})
	if err != nil {
		return nil, fmt.Errorf("query device names: %w", err)
	}

	names := make(map[string]string, len(entries))
	for _, e := range entries {
		names[net.HardwareAddr(e.mac).String()] = e.name
	}
	return names, nil
}

// SetDeviceName gives the device with the given MAC address a name,
// replacing any earlier one.
func SetDeviceName(db *sql.DB, mac net.HardwareAddr, name string) error {
	_, err := db.Exec(`
		INSERT INTO device_names (mac, name, updated_at) VALUES (?, ?, current_timestamp)
		ON CONFLICT (mac) DO UPDATE SET name = excluded.name, updated_at = excluded.updated_at
	`, []byte(mac), name)
	if err != nil {
		return fmt.Errorf("set device name: %w", err)
	}
	return nil
}

// DeleteDeviceName removes a device's name; deleting a name that isn't set
// is not an error.
func DeleteDeviceName(db *sql.DB, mac net.HardwareAddr) error {
	if _, err := db.Exec(`DELETE FROM device_names WHERE mac = ?`, []byte(mac)); err != nil {
		return fmt.Errorf("delete device name: %w", err)
	}
	return nil
}
