package storage

import (
	"github.com/duxet/netmon/common"
	"time"
)

type FlowRecord struct {
	SourceMACAddress      common.MACAddress
	DestinationMACAddress common.MACAddress
	SourceIPAddress       common.IPAddress
	DestinationIPAddress  common.IPAddress
	IPProto               uint8
	Port                  uint16
	InBytes               uint64
	InPackets             uint64
	OutBytes              uint64
	OutPackets            uint64
}

type ClientRecord struct {
	MACAddress common.MACAddress
	IPAddress  common.IPAddress
	InBytes    uint64
	InPackets  uint64
	OutBytes   uint64
	OutPackets uint64
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
