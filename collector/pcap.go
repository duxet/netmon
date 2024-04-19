//go:build !linux

package collector

import "database/sql"

func CollectTraffic(db *sql.DB) {
	// TODO: implement pcap collector for non-linux targets
}
