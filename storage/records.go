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

// TrafficMeasurementRecord is the traffic of one hour. With a MAC or IP
// filter, InBytes is what that client downloaded and OutBytes what it
// uploaded; otherwise they are the reply and original directions.
type TrafficMeasurementRecord struct {
	InBytes  uint64
	OutBytes uint64
	Date     time.Time
}

type EndpointPairTrafficRecord struct {
	SourceIPAddress      common.IPAddress
	DestinationIPAddress common.IPAddress
	InBytes              uint64
	InPackets            uint64
	OutBytes             uint64
	OutPackets           uint64
	// Hour is the start of the hour, for GetEndpointPairTrafficByHour.
	Hour time.Time
}

// DeviceRecord is a device seen in flows, identified by its MAC address.
type DeviceRecord struct {
	MACAddress common.MACAddress
	// IPAddress is the address the device used last.
	IPAddress common.IPAddress
	// Name is the name the user gave the device, if any.
	Name      *string
	FirstSeen time.Time
	LastSeen  time.Time
}

// CompactResult reports what Compact changed.
type CompactResult struct {
	// RolledUp is the number of hourly rows written.
	RolledUp int64
	// Removed is the number of per-minute rows they replaced.
	Removed int64
	// Expired is the number of hourly rows deleted for being past retention.
	Expired int64
}
