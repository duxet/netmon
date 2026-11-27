package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"time"
)

// flowTables are the tables of traffic, of every resolution.
var flowTables = []string{"flows", "flows_hourly", "flows_daily"}

// ForgetDevice removes what netmon knows of a device: when it was seen, its
// name and icon, and the MAC addresses merged into it, which count as devices
// of their own from then on. With traffic, the flows it was an end of are
// deleted too; without, it is back with its next traffic, as a new device.
func ForgetDevice(db *sql.DB, mac net.HardwareAddr, traffic bool) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("forget device: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	statements := []string{
		`DELETE FROM devices WHERE mac = ?`,
		`DELETE FROM device_names WHERE mac = ?`,
		`DELETE FROM device_icons WHERE mac = ?`,
		`DELETE FROM device_aliases WHERE mac = ?`,
	}
	if traffic {
		for _, table := range flowTables {
			// Its flows with another local device stay that device's.
			statements = append(statements,
				`DELETE FROM `+table+` WHERE src_mac = ?`,
				`DELETE FROM `+table+` WHERE dst_mac = $1 AND octet_length(coalesce(src_mac, ''::BLOB)) = 0`,
				`UPDATE `+table+` SET dst_mac = NULL WHERE dst_mac = ?`,
			)
		}
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement, []byte(mac)); err != nil {
			return fmt.Errorf("forget device: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("forget device: %w", err)
	}
	return nil
}

// MergeDevices makes the devices with the MAC addresses aliases one device
// with mac. Their traffic counts as its, the traffic recorded so far as well
// as the traffic to come, and alert rules about them are about it. It keeps
// mac's name and icon, or takes an alias's when it has none. It can't be
// undone.
func MergeDevices(db *sql.DB, mac net.HardwareAddr, aliases []net.HardwareAddr, now time.Time) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("merge devices: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	device := []byte(mac)
	var merged []string
	for _, alias := range aliases {
		if slices.Equal(alias, mac) {
			continue
		}
		a := []byte(alias)
		merged = append(merged, alias.String())

		statements := []struct {
			query string
			args  []any
		}{
			{`INSERT INTO device_aliases VALUES (?, ?, ?) ON CONFLICT (alias) DO UPDATE SET mac = excluded.mac`, []any{a, device, now}},
			// What was merged into the alias is merged into the device.
			{`UPDATE device_aliases SET mac = ? WHERE mac = ?`, []any{device, a}},
			{`DELETE FROM device_aliases WHERE alias = ?`, []any{device}},
			{`INSERT INTO devices
				SELECT ?, last_ip, first_seen, last_seen FROM devices WHERE mac = ?
				ON CONFLICT (mac) DO UPDATE SET
					last_ip = CASE WHEN excluded.last_seen >= devices.last_seen THEN excluded.last_ip ELSE devices.last_ip END,
					first_seen = least(devices.first_seen, excluded.first_seen),
					last_seen = greatest(devices.last_seen, excluded.last_seen)`, []any{device, a}},
			{`DELETE FROM devices WHERE mac = ?`, []any{a}},
			{`INSERT INTO device_names SELECT ?, name, updated_at FROM device_names WHERE mac = ? ON CONFLICT (mac) DO NOTHING`, []any{device, a}},
			{`DELETE FROM device_names WHERE mac = ?`, []any{a}},
			{`INSERT INTO device_icons SELECT ?, icon, updated_at FROM device_icons WHERE mac = ? ON CONFLICT (mac) DO NOTHING`, []any{device, a}},
			{`DELETE FROM device_icons WHERE mac = ?`, []any{a}},
		}
		for _, table := range flowTables {
			statements = append(statements,
				struct {
					query string
					args  []any
				}{`UPDATE ` + table + ` SET src_mac = ? WHERE src_mac = ?`, []any{device, a}},
				struct {
					query string
					args  []any
				}{`UPDATE ` + table + ` SET dst_mac = ? WHERE dst_mac = ?`, []any{device, a}},
			)
		}
		for _, s := range statements {
			if _, err := tx.Exec(s.query, s.args...); err != nil {
				return fmt.Errorf("merge devices: %w", err)
			}
		}
	}

	if err := mergeRuleDevices(tx, mac.String(), merged); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("merge devices: %w", err)
	}
	return nil
}

// mergeRuleDevices makes the alert rules that pick some devices pick mac
// instead of the aliases.
func mergeRuleDevices(tx *sql.Tx, mac string, aliases []string) error {
	rows, err := tx.Query(`SELECT id, params FROM alert_rules`)
	if err != nil {
		return fmt.Errorf("merge devices: %w", err)
	}
	type rule struct {
		id     int64
		params string
	}
	var rules []rule
	for rows.Next() {
		var r rule
		if err := rows.Scan(&r.id, &r.params); err != nil {
			rows.Close()
			return fmt.Errorf("merge devices: %w", err)
		}
		rules = append(rules, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("merge devices: %w", err)
	}

	for _, r := range rules {
		var params map[string]json.RawMessage
		if json.Unmarshal([]byte(r.params), &params) != nil || params["macs"] == nil {
			continue
		}
		var macs []string
		if json.Unmarshal(params["macs"], &macs) != nil {
			continue
		}
		var changed []string
		for _, m := range macs {
			if slices.Contains(aliases, m) {
				m = mac
			}
			if !slices.Contains(changed, m) {
				changed = append(changed, m)
			}
		}
		if slices.Equal(changed, macs) {
			continue
		}
		params["macs"], _ = json.Marshal(changed)
		updated, _ := json.Marshal(params)
		if _, err := tx.Exec(`UPDATE alert_rules SET params = ? WHERE id = ?`, string(updated), r.id); err != nil {
			return fmt.Errorf("merge devices: %w", err)
		}
	}
	return nil
}

// ApplyAliases counts the traffic of merged MAC addresses in the per-minute
// rows as their device's, before the rows are rolled up.
func ApplyAliases(db *sql.DB) error {
	for _, column := range []string{"src_mac", "dst_mac"} {
		_, err := db.Exec(`UPDATE flows SET ` + column + ` = a.mac FROM device_aliases a WHERE flows.` + column + ` = a.alias`)
		if err != nil {
			return fmt.Errorf("apply device aliases: %w", err)
		}
	}
	return nil
}

// GetAliases returns the MAC addresses merged into devices, by the device's
// MAC address.
func GetAliases(db *sql.DB) (map[string][]string, error) {
	rows, err := db.Query(`SELECT mac, alias FROM device_aliases ORDER BY mac, alias`)
	if err != nil {
		return nil, fmt.Errorf("query device aliases: %w", err)
	}
	defer rows.Close()

	aliases := map[string][]string{}
	for rows.Next() {
		var mac, alias []byte
		if err := rows.Scan(&mac, &alias); err != nil {
			return nil, fmt.Errorf("query device aliases: %w", err)
		}
		key := net.HardwareAddr(mac).String()
		aliases[key] = append(aliases[key], net.HardwareAddr(alias).String())
	}
	return aliases, rows.Err()
}

// DeleteAlias stops counting the traffic to come of a merged MAC address as
// its device's; what was counted so far stays the device's.
func DeleteAlias(db *sql.DB, alias net.HardwareAddr) error {
	res, err := db.Exec(`DELETE FROM device_aliases WHERE alias = ?`, []byte(alias))
	if err != nil {
		return fmt.Errorf("delete device alias: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("alias %s: %w", alias, ErrNotFound)
	}
	return nil
}
