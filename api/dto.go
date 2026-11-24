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
	// Name is the name the user gave the device, if any.
	Name *string
	// Hostname is from reverse DNS, with the trailing dot.
	Hostname *string
	// Vendor is the manufacturer the MAC address was assigned to, if known.
	Vendor   *string
	Endpoint EndpointDTO
	Traffic  TrafficDTO
}

// DeviceDTO is a device seen on the network, identified by its MAC address.
type DeviceDTO struct {
	MACAddress common.MACAddress
	// IPAddress is the address the device used last.
	IPAddress common.IPAddress
	// Name is the name the user gave the device, if any.
	Name *string
	// Hostname is from reverse DNS of IPAddress, with the trailing dot.
	Hostname *string
	// Vendor is the manufacturer the MAC address was assigned to, if known.
	Vendor *string
	// FirstSeen and LastSeen are when the device was first and last seen in
	// flows, on either side of a connection.
	FirstSeen time.Time
	LastSeen  time.Time
}

// DeviceNameDTO is a device's user-given name.
type DeviceNameDTO struct {
	MACAddress string
	Name       string
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

// LiveSampleDTO is the traffic of the devices on the local network during one
// interval, as measured by the live view.
type LiveSampleDTO struct {
	// Time is when the interval ended, to the millisecond.
	Time time.Time
	// Devices are the ones that had traffic; none in a quiet second.
	Devices []LiveDeviceDTO
}

// LiveDeviceDTO is how much of the network a device used during a sample, from
// its own side: Download is what it received.
type LiveDeviceDTO struct {
	MACAddress string
	// IPAddress is an address the device used, or empty when that is not known.
	IPAddress string
	// Download and Upload are in bytes per second.
	Download uint64
	Upload   uint64
}

// NotificationServiceDTO is a service notification channels can use.
type NotificationServiceDTO struct {
	Scheme string
	Name   string
	// ExampleURL shows the service URL format, with placeholders in
	// capitals. It is empty for the less common services.
	ExampleURL string
	// DocsURL explains where to find the values the URL needs.
	DocsURL string
}

// NotificationChannelDTO is a notification channel. Its URL holds the
// service's credentials, so only a masked form is returned.
type NotificationChannelDTO struct {
	ID   int64
	Name string
	// Service is the URL's scheme, e.g. "telegram".
	Service string
	// MaskedURL is the URL with its secrets replaced by "REDACTED".
	MaskedURL string
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}
