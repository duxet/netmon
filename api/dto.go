package api

import (
	"github.com/duxet/netmon/common"
	"time"
)

type TrafficDTO struct {
	InBytes    uint64
	InPackets  uint64
	OutBytes   uint64
	OutPackets uint64
}

type ClientDTO struct {
	ID          common.ClientID
	MACAddress  common.MACAddress
	IPAddresses common.IPAddresses
	Hostname    *string
	Traffic     TrafficDTO
}

type FlowDTO struct {
	ClientID common.ClientID
	LocalIP  common.IPAddress
	RemoteIP common.IPAddress
	Country  *string
	IPProto  uint8
	Port     uint16
	Traffic  TrafficDTO
}

type StatsDTO struct {
	TotalClients uint64
	TotalBytes   *uint64
	TotalPackets *uint64
}

type TrafficMeasurementDTO struct {
	InBytes  uint64
	OutBytes uint64
	Date     time.Time
}

type HostnameDTO struct {
	IPAddress common.IPAddress
	Hostname  *string
}
