package storage

import (
	"github.com/duxet/netmon/common"
	"time"
)

type FlowRecord struct {
	ClientID   common.ClientID
	LocalIP    common.IPAddress
	RemoteIP   common.IPAddress
	IPProto    uint8
	Port       uint16
	InBytes    uint64
	InPackets  uint64
	OutBytes   uint64
	OutPackets uint64
}

type ClientRecord struct {
	ID         common.ClientID
	MACAddress common.MACAddress
	Hostname   *string
}

type ClientWithStatsRecord struct {
	ID          common.ClientID
	MACAddress  common.MACAddress
	IPAddresses common.IPAddresses
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
