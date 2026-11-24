package api

import (
	"github.com/duxet/netmon/common"
	"time"
)

type EndpointDTO struct {
	MACAddress common.MACAddress
	IPAddress  common.IPAddress
	Country    *string
}

type TrafficDTO struct {
	InBytes    uint64
	InPackets  uint64
	OutBytes   uint64
	OutPackets uint64
}

type ClientDTO struct {
	Hostname *string
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

type TrafficMeasurementDTO struct {
	InBytes  uint64
	OutBytes uint64
	Date     time.Time
}

type HostnameDTO struct {
	IPAddress common.IPAddress
	Hostname  *string
}

type AppTrafficDTO struct {
	Name    string
	Traffic TrafficDTO
}

// GroupDTO is the traffic of one group of flows, seen from the client the
// summary is for: Download is what it received.
type GroupDTO struct {
	// Key identifies the group: an IP address, "protocol/port" (e.g.
	// "6/443") or an ISO country code. Empty for Rest.
	Key         string
	Download    uint64
	Upload      uint64
	Connections uint64
}

// GroupsDTO holds the largest groups and the sum of all others.
type GroupsDTO struct {
	Items []GroupDTO
	Rest  GroupDTO
}

// FlowSummaryDTO summarizes all flows matching a filter.
type FlowSummaryDTO struct {
	Download    uint64
	Upload      uint64
	Packets     uint64
	Connections uint64
	// Peers is the number of distinct remote hosts.
	Peers     uint64
	TopPeers  GroupsDTO
	Services  GroupsDTO
	Countries GroupsDTO
}
