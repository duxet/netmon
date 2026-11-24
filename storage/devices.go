package storage

import (
	"database/sql"
	"fmt"
	"net"
	"time"
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

// SyncDevices records the devices seen in per-minute flows written at or
// after since: new MAC addresses are added, and known ones get the IP
// address they used last and the time they were last seen. It never moves
// first_seen later or last_seen earlier, so running it again over rows it
// has already seen changes nothing and callers can use overlapping windows.
// Flows without a MAC address (NULL, or empty when the collector found an
// incomplete neighbor entry) are skipped.
func SyncDevices(db *sql.DB, since time.Time) error {
	_, err := db.Exec(`
		INSERT INTO devices
		SELECT mac, arg_max(ip, created_at), min(created_at), max(created_at)
		FROM (
			SELECT src_mac AS mac, src_ip AS ip, created_at FROM flows
			WHERE octet_length(src_mac) > 0 AND created_at >= ?::TIMESTAMPTZ
			UNION ALL
			SELECT dst_mac, dst_ip, created_at FROM flows
			WHERE octet_length(dst_mac) > 0 AND created_at >= ?::TIMESTAMPTZ
		)
		GROUP BY mac
		ON CONFLICT (mac) DO UPDATE SET
			last_ip = CASE WHEN excluded.last_seen >= devices.last_seen
				THEN excluded.last_ip ELSE devices.last_ip END,
			first_seen = least(devices.first_seen, excluded.first_seen),
			last_seen = greatest(devices.last_seen, excluded.last_seen)
	`, since, since)
	if err != nil {
		return fmt.Errorf("sync devices: %w", err)
	}
	return nil
}

// GetDevices returns every device seen in flows, most recently seen first,
// with the names users gave them.
func GetDevices(db *sql.DB) ([]DeviceRecord, error) {
	rows, err := db.Query(`
		SELECT d.mac, d.last_ip, n.name, d.first_seen, d.last_seen
		FROM devices d
		LEFT JOIN device_names n ON n.mac = d.mac
		ORDER BY d.last_seen DESC, d.mac
	`)
	devices, err := collect(rows, err, func(rows *sql.Rows, d *DeviceRecord) error {
		return rows.Scan(&d.MACAddress, &d.IPAddress, &d.Name, &d.FirstSeen, &d.LastSeen)
	})
	if err != nil {
		return nil, fmt.Errorf("query devices: %w", err)
	}
	return devices, nil
}
