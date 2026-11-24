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

// DeviceTrafficRecord is what a device exchanged with one remote address,
// from the device's side: Download is what it received.
type DeviceTrafficRecord struct {
	MACAddress common.MACAddress
	Peer       common.IPAddress
	Download   uint64
	Upload     uint64
}

// PortTrafficRecord is what a device exchanged with a remote address on a
// port it connected to.
type PortTrafficRecord struct {
	MACAddress common.MACAddress
	Peer       common.IPAddress
	Port       uint16
	IPProto    uint8
	Bytes      uint64
}

// NetworkTrafficRecord is the traffic of flows from addresses of one network
// to those of another, as GetNetworkTraffic returns it. Source and
// Destination are network IDs, nil for addresses in no network. In is the
// replies, what the source received; Out is what it sent.
type NetworkTrafficRecord struct {
	Source      *int64
	Destination *int64
	InBytes     uint64
	InPackets   uint64
	OutBytes    uint64
	OutPackets  uint64
}
