//go:build !linux && !darwin

package collector

import (
	"database/sql"
	"errors"
)

func CollectTraffic(db *sql.DB) (*Collector, error) {
	// TODO: implement a collector for this platform (Linux uses Netfilter,
	// macOS uses libpcap)
	return nil, errors.New("collector not implemented")
}
