package storage

import (
	"database/sql"
	"fmt"
	"net"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/duxet/netmon/common"
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

// GetDeviceIcons returns the icons users picked for their devices, by MAC
// address in its canonical form (net.HardwareAddr.String).
func GetDeviceIcons(db *sql.DB) (map[string]string, error) {
	rows, err := db.Query(`SELECT mac, icon FROM device_icons`)
	type entry struct {
		mac  []byte
		icon string
	}
	entries, err := collect(rows, err, func(rows *sql.Rows, e *entry) error {
		return rows.Scan(&e.mac, &e.icon)
	})
	if err != nil {
		return nil, fmt.Errorf("query device icons: %w", err)
	}

	icons := make(map[string]string, len(entries))
	for _, e := range entries {
		icons[net.HardwareAddr(e.mac).String()] = e.icon
	}
	return icons, nil
}

// SetDeviceIcon gives the device with the given MAC address an icon,
// replacing any earlier one. Callers check the icon is a known one.
func SetDeviceIcon(db *sql.DB, mac net.HardwareAddr, icon string) error {
	_, err := db.Exec(`
		INSERT INTO device_icons (mac, icon, updated_at) VALUES (?, ?, current_timestamp)
		ON CONFLICT (mac) DO UPDATE SET icon = excluded.icon, updated_at = excluded.updated_at
	`, []byte(mac), icon)
	if err != nil {
		return fmt.Errorf("set device icon: %w", err)
	}
	return nil
}

// DeleteDeviceIcon removes a device's icon; deleting an icon that isn't set
// is not an error.
func DeleteDeviceIcon(db *sql.DB, mac net.HardwareAddr) error {
	if _, err := db.Exec(`DELETE FROM device_icons WHERE mac = ?`, []byte(mac)); err != nil {
		return fmt.Errorf("delete device icon: %w", err)
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
	if err := ApplyAliases(db); err != nil {
		return err
	}

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
// with the names and icons users gave them.
func GetDevices(db *sql.DB) ([]DeviceRecord, error) {
	rows, err := db.Query(`
		SELECT d.mac, d.last_ip, n.name, i.icon, d.first_seen, d.last_seen
		FROM devices d
		LEFT JOIN device_names n ON n.mac = d.mac
		LEFT JOIN device_icons i ON i.mac = d.mac
		ORDER BY d.last_seen DESC, d.mac
	`)
	devices, err := collect(rows, err, func(rows *sql.Rows, d *DeviceRecord) error {
		return rows.Scan(&d.MACAddress, &d.IPAddress, &d.Name, &d.Icon, &d.FirstSeen, &d.LastSeen)
	})
	if err != nil {
		return nil, fmt.Errorf("query devices: %w", err)
	}
	return devices, nil
}

// GetDeviceTraffic returns the traffic of every device since the given time,
// per remote address, from the device's own side, as the client pages count
// it: a flow between two local devices counts for both. Hourly rows count
// when their hour starts at or after since, so for older data the window is
// rounded to whole hours.
func GetDeviceTraffic(db *sql.DB, since time.Time) ([]DeviceTrafficRecord, error) {
	rows, err := db.Query(`
		SELECT mac, peer, sum(download)::UBIGINT, sum(upload)::UBIGINT
		FROM (
			SELECT src_mac AS mac, dst_ip AS peer, in_bytes AS download, out_bytes AS upload
			FROM all_flows
			WHERE octet_length(src_mac) > 0 AND created_at >= ?::TIMESTAMPTZ
			UNION ALL
			SELECT dst_mac, src_ip, out_bytes, in_bytes
			FROM all_flows
			WHERE octet_length(dst_mac) > 0 AND created_at >= ?::TIMESTAMPTZ
		)
		GROUP BY mac, peer
	`, since, since)
	records, err := collect(rows, err, func(rows *sql.Rows, r *DeviceTrafficRecord) error {
		return rows.Scan(&r.MACAddress, &r.Peer, &r.Download, &r.Upload)
	})
	if err != nil {
		return nil, fmt.Errorf("query device traffic: %w", err)
	}
	return records, nil
}

// GetPortTraffic returns the connections devices opened to the given ports
// since the given time, per device, remote address, port and IP protocol
// (6 for TCP, 17 for UDP; none limits it to those two).
func GetPortTraffic(db *sql.DB, since time.Time, ports []uint16, protocols []int) ([]PortTrafficRecord, error) {
	if len(ports) == 0 {
		return nil, nil
	}
	// Not []uint8: that is []byte, which goes to the database as one BLOB.
	if len(protocols) == 0 {
		protocols = []int{6, 17}
	}

	rows, err := sq.Select("src_mac", "dst_ip", "port", "ip_proto", "sum(in_bytes + out_bytes)::UBIGINT").
		From("all_flows").
		Where("octet_length(src_mac) > 0").
		Where("created_at >= ?::TIMESTAMPTZ", since).
		Where(sq.Eq{"port": ports, "ip_proto": protocols}).
		GroupBy("src_mac", "dst_ip", "port", "ip_proto").
		RunWith(db).Query()
	records, err := collect(rows, err, func(rows *sql.Rows, r *PortTrafficRecord) error {
		return rows.Scan(&r.MACAddress, &r.Peer, &r.Port, &r.IPProto, &r.Bytes)
	})
	if err != nil {
		return nil, fmt.Errorf("query port traffic: %w", err)
	}
	return records, nil
}

// ComebackRecord is a device that had traffic again after a quiet while.
type ComebackRecord struct {
	MACAddress common.MACAddress
	// Back is when its traffic started again.
	Back time.Time
}

// GetComebacks returns the devices with traffic at or after since that had
// none for at least quiet before it. Rows of an hour or a day count for all
// of it, so a device is only quiet when it is for sure.
func GetComebacks(db *sql.DB, since time.Time, quiet time.Duration) ([]ComebackRecord, error) {
	lookback := since.Add(-quiet)
	rows, err := db.Query(`
		SELECT mac, min(created_at) FILTER (WHERE created_at >= $1)
		FROM (
			SELECT src_mac AS mac, created_at, hours FROM all_flows
			WHERE octet_length(src_mac) > 0 AND created_at > $2::TIMESTAMPTZ - INTERVAL 1 DAY
				AND created_at + to_minutes(minutes::BIGINT) > $2
			UNION ALL
			SELECT dst_mac, created_at, hours FROM all_flows
			WHERE octet_length(dst_mac) > 0 AND created_at > $2::TIMESTAMPTZ - INTERVAL 1 DAY
				AND created_at + to_minutes(minutes::BIGINT) > $2
		)
		GROUP BY mac
		HAVING count(*) FILTER (WHERE created_at >= $1) > 0 AND count(*) FILTER (WHERE created_at < $1) = 0
		ORDER BY mac
	`, since, lookback)
	comebacks, err := collect(rows, err, func(rows *sql.Rows, r *ComebackRecord) error {
		return rows.Scan(&r.MACAddress, &r.Back)
	})
	if err != nil {
		return nil, fmt.Errorf("query comebacks: %w", err)
	}
	return comebacks, nil
}

// LastTrafficBefore returns when a device last had traffic before a time:
// the end of the last minute, hour or day it had some in. ok is false when
// it never had any.
func LastTrafficBefore(db *sql.DB, mac []byte, before time.Time) (last time.Time, ok bool, err error) {
	var t sql.NullTime
	err = db.QueryRow(`
		SELECT max(least(created_at + to_minutes(minutes::BIGINT), $2::TIMESTAMPTZ))
		FROM all_flows
		WHERE (src_mac = $1 OR dst_mac = $1) AND created_at < $2
	`, mac, before).Scan(&t)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("query last traffic: %w", err)
	}
	return t.Time, t.Valid, nil
}

// GetDevicePeers returns the remote addresses every device exchanged traffic
// with from from to to, once each.
func GetDevicePeers(db *sql.DB, from, to time.Time) ([]DeviceTrafficRecord, error) {
	rows, err := db.Query(`
		SELECT DISTINCT mac, peer
		FROM (
			SELECT src_mac AS mac, dst_ip AS peer FROM all_flows
			WHERE octet_length(src_mac) > 0 AND created_at >= $1::TIMESTAMPTZ AND created_at < $2::TIMESTAMPTZ
			UNION ALL
			SELECT dst_mac, src_ip FROM all_flows
			WHERE octet_length(dst_mac) > 0 AND created_at >= $1::TIMESTAMPTZ AND created_at < $2::TIMESTAMPTZ
		)
	`, from, to)
	records, err := collect(rows, err, func(rows *sql.Rows, r *DeviceTrafficRecord) error {
		return rows.Scan(&r.MACAddress, &r.Peer)
	})
	if err != nil {
		return nil, fmt.Errorf("query device peers: %w", err)
	}
	return records, nil
}
