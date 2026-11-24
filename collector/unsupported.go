//go:build !linux && !darwin

package collector

import (
	"database/sql"
	"errors"

	"github.com/duxet/netmon/live"
)

func CollectTraffic(db *sql.DB, meter *live.Meter) (*Collector, error) {
	// TODO: implement a collector for this platform (Linux uses Netfilter,
	// macOS uses libpcap)
	return nil, errors.New("collector not implemented")
}
