//go:build !linux

package collector

import (
	"database/sql"
	"errors"
)

func CollectTraffic(db *sql.DB) (*Collector, error) {
	// TODO: implement pcap collector for non-linux targets
	return nil, errors.New("collector not implemented")
}
