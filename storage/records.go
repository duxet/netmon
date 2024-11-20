package storage

import (
	"github.com/duxet/netmon/common"
	"github.com/marcboeker/go-duckdb"
	"time"
)

type FlowRecord struct {
	ClientID   uint32
	IPAddress  common.IPAddress
	IPProto    uint8
	Port       uint16
	InBytes    uint64
	InPackets  uint64
	OutBytes   uint64
	OutPackets uint64
}

type ClientRecord struct {
	ID          uint32
	MACAddress  common.MACAddress
	IPAddresses duckdb.Composite[[]common.IPAddress]
	Hostname    string
}

type ClientWithStatsRecord struct {
	ID          uint32
	MACAddress  common.MACAddress
	IPAddresses duckdb.Composite[[]common.IPAddress]
	Hostname    *string
	InBytes     uint64
	InPackets   uint64
	OutBytes    uint64
	OutPackets  uint64
}

type StatsRecord struct {
	TotalClients uint64
	TotalBytes   *uint64
	TotalPackets *uint64
}

type TrafficMeasurementRecord struct {
	InBytes  uint64
	OutBytes uint64
	Date     time.Time
}
