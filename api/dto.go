package api

import (
	"encoding/json"
	"time"

	"github.com/duxet/netmon/common"
)

type EndpointDTO struct {
	MACAddress common.MACAddress
	IPAddress  common.IPAddress
	Country    *string
	// ASN is the autonomous system that announces the address. Like Country,
	// it is only set by /api/flows, and only with a GeoLite2-ASN database.
	ASN *ASNDTO
}

// ASNDTO is an autonomous system: the network of an ISP, a cloud provider or
// another organization.
type ASNDTO struct {
	Number       uint
	Organization string
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
	// Icon is the icon the user picked for the device, one of DeviceIcons,
	// if any.
	Icon *string
	// Hostname is from reverse DNS, with the trailing dot.
	Hostname *string
	// Vendor is the manufacturer the MAC address was assigned to, if known.
	Vendor *string
	// NetworkID is the network the client's IP address belongs to, if any.
	NetworkID *int64
	// UniFi is what the UniFi Network Application knows about the device,
	// when it is configured and knows it.
	UniFi    *UniFiClientDTO
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
	// Icon is the icon the user picked for the device, one of DeviceIcons,
	// if any.
	Icon *string
	// Hostname is from reverse DNS of IPAddress, with the trailing dot.
	Hostname *string
	// Vendor is the manufacturer the MAC address was assigned to, if known.
	Vendor *string
	// NetworkID is the network IPAddress belongs to, if any.
	NetworkID *int64
	// UniFi is what the UniFi Network Application knows about the device,
	// when it is configured and knows it.
	UniFi *UniFiClientDTO
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

// DeviceIconDTO is the icon the user picked for a device.
type DeviceIconDTO struct {
	MACAddress string
	Icon       string
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
	// "6/443"), an ISO country code or an AS number (e.g. "15169"). Empty
	// for Rest.
	Key string
	// Label says more about the key when it needs it: the organization of an
	// AS number. It is empty for the other groups.
	Label       string
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
	// ASNs groups remote hosts by the autonomous system they are in. It is
	// empty without a GeoLite2-ASN database.
	ASNs GroupsDTO
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
	// UsedByRules is the number of alert rules that notify the channel.
	UsedByRules int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// AlertRuleOptionsDTO is what the rule form offers.
type AlertRuleOptionsDTO struct {
	Types []string
	// Apps are the apps a traffic rule can be limited to.
	Apps             []string
	MinWindowSeconds int64
	MaxWindowSeconds int64
	MinRepeatSeconds int64
	// GeoIP tells whether a GeoIP database is loaded, which country rules
	// need.
	GeoIP bool
}

// AlertRulePreviewDTO is what a rule would find now.
type AlertRulePreviewDTO struct {
	// Findings are the first 20, those the rule would alert about first.
	Findings []FindingDTO
	Total    int
	// Since is where a preview of an event rule starts looking; null for
	// rules about what holds now.
	Since *time.Time
}

// FindingDTO is something a previewed rule finds.
type FindingDTO struct {
	Subject string
	// Label is who it is about: a device's name, or "the network".
	Label string
	// Value is bytes for traffic and events, seconds for quiet devices.
	Value *float64
	// Fires tells whether the rule would alert about it; rules about traffic
	// and quiet devices also list what doesn't meet their condition.
	Fires bool
	Title string
	Body  string
}

// AlertRuleDTO is an alert rule.
type AlertRuleDTO struct {
	ID   int64
	Name string
	// Type is new_device or traffic_threshold; Params are its settings.
	Type     string
	Params   json.RawMessage
	Severity string
	// RepeatIntervalSeconds is how often a firing alert is sent again, or
	// null to send it once.
	RepeatIntervalSeconds *int64
	NotifyOnResolve       bool
	Enabled               bool
	ChannelIDs            []int64
	// FiringAlerts is the number of the rule's alerts that are firing.
	FiringAlerts    int
	LastEvaluatedAt *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// AlertDTO is an alert: a rule firing for a subject, usually a device.
type AlertDTO struct {
	ID       int64
	RuleID   int64
	RuleName string
	Subject  string
	// State is firing or resolved.
	State    string
	Severity string
	Value    *float64
	Title    string
	Body     string
	// Labels describe the subject, e.g. mac, ip, device_name, vendor.
	Labels          map[string]string
	StartedAt       time.Time
	LastEvaluatedAt time.Time
	LastNotifiedAt  *time.Time
	ResolvedAt      *time.Time
	Deliveries      []DeliveryDTO
}

// DeliveryDTO is a notification sent, or not, about an alert.
type DeliveryDTO struct {
	ChannelID int64
	// Kind is firing, reminder or resolved; Status is sent, failed or
	// suppressed.
	Kind      string
	Status    string
	Attempts  int
	Error     *string
	CreatedAt time.Time
}

// SessionDTO tells whether people have to sign in to use netmon, and who the
// request is from.
type SessionDTO struct {
	AuthEnabled bool
	// User is who is signed in. It is null when netmon has no sign-in.
	User *UserDTO
}

// UserDTO is a person signed in through single sign-on. Name and Email are
// null when the provider didn't say.
type UserDTO struct {
	Name  *string
	Email *string
}

// NetworkDTO is a group of the local network's addresses, such as a VLAN.
type NetworkDTO struct {
	ID   int64
	Name string
	// VLAN is the network's 802.1Q VLAN ID, if it was given one.
	VLAN *int
	// Color is the color the network is shown in: blue, orange, aqua,
	// yellow, magenta, green, violet or red.
	Color string
	// Prefixes are the blocks of addresses the network uses, in CIDR
	// notation. An address belongs to the network with the most specific
	// block holding it.
	Prefixes  []string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NetworkSummaryDTO is the traffic of every network over a time range.
type NetworkSummaryDTO struct {
	// Networks are all networks, in the order /api/networks lists them,
	// with or without traffic.
	Networks []NetworkTrafficDTO
	// UnassignedDevices is the number of devices with traffic that have no
	// address in any network.
	UnassignedDevices int
}

// NetworkTrafficDTO is the traffic of one network, counted from its side:
// Download is what its addresses received, from the internet and from
// other networks alike. Traffic between two networks counts for both.
type NetworkTrafficDTO struct {
	NetworkID int64
	// Devices is the number of devices with traffic from an address in the
	// network.
	Devices  int
	Download uint64
	Upload   uint64
	Packets  uint64
}

// NetworkSuggestionDTO is a block of addresses devices use that no network
// has yet.
type NetworkSuggestionDTO struct {
	Prefix string
	// Devices is the number of known devices whose last address is in it.
	Devices int
}
