package api

import "github.com/duxet/netmon/common"

type EndpointDTO struct {
	MACAddress common.MACAddress
	IPAddress  common.IPAddress
	Hostname   *string
	Country    *string
}

type TrafficDTO struct {
	InBytes    uint64
	InPackets  uint64
	OutBytes   uint64
	OutPackets uint64
}

type ClientDTO struct {
	Endpoint EndpointDTO
	Traffic  TrafficDTO
}

type FlowDTO struct {
	Source      EndpointDTO
	Destination EndpointDTO
	IPProto     uint8
	Port        uint16
	Traffic     TrafficDTO
}

type StatsDTO struct {
	TotalClients uint64
	TotalBytes   *uint64
	TotalPackets *uint64
}
