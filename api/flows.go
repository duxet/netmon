package api

import (
	"bytes"
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/duxet/netmon/common"
	"github.com/duxet/netmon/lookup"
	"github.com/duxet/netmon/storage"
)

const (
	maxFlowsPerPage  = 1000
	defaultTopGroups = 8
	maxTopGroups     = 50
)

// orientedFlow is a flow seen from the client or network a filter selects:
// Download is what it received, whichever side opened the connection.
// Without either, flows are seen from their source.
type orientedFlow struct {
	record   storage.FlowRecord
	peer     common.IPAddress
	download uint64
	upload   uint64
	packets  uint64
}

func (f orientedFlow) total() uint64 { return f.download + f.upload }

func sameAddr(a, b common.IPAddress) bool {
	return a.Addr != nil && b.Addr != nil && a.Unmap() == b.Unmap()
}

func isClient(mac common.MACAddress, ip common.IPAddress, filter storage.FlowsFilter) bool {
	switch {
	case filter.MAC != nil:
		return bytes.Equal(mac.HardwareAddr, filter.MAC.HardwareAddr)
	case filter.IP != nil:
		return sameAddr(ip, *filter.IP)
	case filter.Network != nil:
		return ip.Addr != nil && filter.Network.Contains(*ip.Addr)
	default:
		return false
	}
}

func orientFlows(records []storage.FlowRecord, filter storage.FlowsFilter) []orientedFlow {
	flows := make([]orientedFlow, 0, len(records))

	for _, record := range records {
		srcIsClient := isClient(record.SourceMACAddress, record.SourceIPAddress, filter)
		dstIsClient := isClient(record.DestinationMACAddress, record.DestinationIPAddress, filter)
		// The source opened the connection, so its replies (In) are downloads.
		outbound := srcIsClient || !dstIsClient

		flow := orientedFlow{
			record:  record,
			packets: record.InPackets + record.OutPackets,
		}
		if outbound {
			flow.peer = record.DestinationIPAddress
			flow.download, flow.upload = record.InBytes, record.OutBytes
		} else {
			flow.peer = record.SourceIPAddress
			flow.download, flow.upload = record.OutBytes, record.InBytes
		}
		flows = append(flows, flow)
	}

	return flows
}

var protocolNames = map[uint8]string{
	1: "icmp", 2: "igmp", 6: "tcp", 17: "udp", 41: "ipv6", 47: "gre",
	50: "esp", 51: "ah", 58: "icmpv6", 132: "sctp",
}

// matchesQuery reports whether a flow matches a search: a part of the remote
// host's IP address (either endpoint without a client filter), a port
// number, or a protocol name.
func matchesQuery(flow orientedFlow, query string, hasClient bool) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}

	if port, err := strconv.ParseUint(query, 10, 16); err == nil && uint16(port) == flow.record.Port {
		return true
	}

	if protocolNames[flow.record.IPProto] == query {
		return true
	}

	addrs := []common.IPAddress{flow.peer}
	if !hasClient {
		addrs = []common.IPAddress{flow.record.SourceIPAddress, flow.record.DestinationIPAddress}
	}
	for _, addr := range addrs {
		if addr.Addr != nil && strings.Contains(strings.ToLower(addr.String()), query) {
			return true
		}
	}

	return false
}

// FlowsPage selects, sorts and pages flows.
type FlowsPage struct {
	Query  string
	Sort   string // "total" (default), "download", "upload" or "packets"
	Asc    bool
	Limit  int // 0 returns all flows
	Offset int
}

var flowSortKeys = map[string]func(orientedFlow) uint64{
	"total":    orientedFlow.total,
	"download": func(f orientedFlow) uint64 { return f.download },
	"upload":   func(f orientedFlow) uint64 { return f.upload },
	"packets":  func(f orientedFlow) uint64 { return f.packets },
}

func compareAddrs(a, b common.IPAddress) int {
	switch {
	case a.Addr == nil || b.Addr == nil:
		return cmp.Compare(boolInt(a.Addr != nil), boolInt(b.Addr != nil))
	default:
		return a.Compare(*b.Addr)
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// pageFlows filters and sorts flows and returns the requested page with the
// number of flows matching the query. Ties are broken by endpoints, protocol
// and port, so pages stay stable across requests.
func pageFlows(flows []orientedFlow, page FlowsPage, hasClient bool) ([]orientedFlow, int) {
	matching := make([]orientedFlow, 0, len(flows))
	for _, flow := range flows {
		if matchesQuery(flow, page.Query, hasClient) {
			matching = append(matching, flow)
		}
	}

	key := flowSortKeys[page.Sort]
	if key == nil {
		key = flowSortKeys["total"]
	}

	slices.SortStableFunc(matching, func(a, b orientedFlow) int {
		c := cmp.Compare(key(a), key(b))
		if !page.Asc {
			c = -c
		}
		if c != 0 {
			return c
		}
		return cmp.Or(
			compareAddrs(a.peer, b.peer),
			compareAddrs(a.record.SourceIPAddress, b.record.SourceIPAddress),
			compareAddrs(a.record.DestinationIPAddress, b.record.DestinationIPAddress),
			cmp.Compare(a.record.IPProto, b.record.IPProto),
			cmp.Compare(a.record.Port, b.record.Port),
			bytes.Compare(a.record.SourceMACAddress.HardwareAddr, b.record.SourceMACAddress.HardwareAddr),
			bytes.Compare(a.record.DestinationMACAddress.HardwareAddr, b.record.DestinationMACAddress.HardwareAddr),
		)
	})

	total := len(matching)
	start := min(page.Offset, total)
	end := total
	if page.Limit > 0 {
		end = min(start+page.Limit, total)
	}

	return matching[start:end], total
}

// groupTraffic is traffic to add to a group.
type groupTraffic struct {
	key         string
	download    uint64
	upload      uint64
	connections uint64
}

// topGroups sums traffic per key (skipping the key "") and returns the top
// largest groups plus the sum of the rest.
func topGroups(items []groupTraffic, top int) GroupsDTO {
	byKey := map[string]*GroupDTO{}
	for _, item := range items {
		if item.key == "" {
			continue
		}
		group, ok := byKey[item.key]
		if !ok {
			group = &GroupDTO{Key: item.key}
			byKey[item.key] = group
		}
		group.Download += item.download
		group.Upload += item.upload
		group.Connections += item.connections
	}

	groups := make([]GroupDTO, 0, len(byKey))
	for _, group := range byKey {
		groups = append(groups, *group)
	}
	slices.SortFunc(groups, func(a, b GroupDTO) int {
		return cmp.Or(
			cmp.Compare(b.Download+b.Upload, a.Download+a.Upload),
			strings.Compare(a.Key, b.Key),
		)
	})

	result := GroupsDTO{Items: groups[:min(top, len(groups))]}
	for _, group := range groups[len(result.Items):] {
		result.Rest.Download += group.Download
		result.Rest.Upload += group.Upload
		result.Rest.Connections += group.Connections
	}

	return result
}

// labelASNs sets the label of AS number groups to their organization.
func labelASNs(groups *GroupsDTO, peerASNs map[string]*ASNDTO) {
	organizations := map[string]string{}
	for _, asn := range peerASNs {
		if asn != nil {
			organizations[strconv.FormatUint(uint64(asn.Number), 10)] = asn.Organization
		}
	}
	for i := range groups.Items {
		groups.Items[i].Label = organizations[groups.Items[i].Key]
	}
}

// asnOf is the autonomous system of an address, or nil when it is unknown.
func asnOf(asns *lookup.ASNs, ip common.IPAddress) *ASNDTO {
	if ip.Addr == nil {
		return nil
	}
	number, organization, ok := asns.Lookup(ip.Unmap())
	if !ok {
		return nil
	}
	return &ASNDTO{Number: number, Organization: organization}
}

// summarizeFlows totals the traffic and groups it by remote host, service,
// country and autonomous system, from the traffic per remote host and per
// service. Countries and systems are looked up once per remote host.
func summarizeFlows(peers []storage.PeerTraffic, services []storage.ServiceTraffic, top int, countries *lookup.Countries, asns *lookup.ASNs) FlowSummaryDTO {
	var summary FlowSummaryDTO
	var byPeer, byCountry, byASN []groupTraffic
	peerASNs := map[string]*ASNDTO{}

	for _, p := range peers {
		summary.Download += p.Download
		summary.Upload += p.Upload
		summary.Packets += p.Packets
		summary.Connections += p.Flows

		if p.Peer.Addr == nil {
			continue
		}
		peer := p.Peer.Unmap().String()
		traffic := groupTraffic{key: peer, download: p.Download, upload: p.Upload, connections: p.Flows}
		byPeer = append(byPeer, traffic)

		if _, ok := peerASNs[peer]; !ok {
			peerASNs[peer] = asnOf(asns, p.Peer)
		}
		if code := countries.Country(p.Peer); code != nil {
			traffic.key = *code
			byCountry = append(byCountry, traffic)
		}
		if asn := peerASNs[peer]; asn != nil {
			traffic.key = strconv.FormatUint(uint64(asn.Number), 10)
			byASN = append(byASN, traffic)
		}
	}
	summary.Peers = uint64(len(peerASNs))

	var byService []groupTraffic
	for _, s := range services {
		byService = append(byService, groupTraffic{
			key:      strconv.Itoa(int(s.IPProto)) + "/" + strconv.Itoa(int(s.Port)),
			download: s.Download, upload: s.Upload, connections: s.Flows,
		})
	}

	summary.TopPeers = topGroups(byPeer, top)
	summary.Services = topGroups(byService, top)
	summary.Countries = topGroups(byCountry, top)
	summary.ASNs = topGroups(byASN, top)
	// Systems of one organization are different keys, so label each group
	// with the name its AS number is registered under.
	labelASNs(&summary.ASNs, peerASNs)

	return summary
}
