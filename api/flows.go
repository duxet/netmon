package api

import (
	"bytes"
	"cmp"
	"context"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/duxet/netmon/apps"
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

// maxSearchLookups is how many remote hosts a search looks the names of up,
// the ones with the most traffic; others only match by names known already.
const maxSearchLookups = 256

// flowSearch matches flows against a search: a port number, a protocol name,
// or part of the remote host's address (either end's without a client),
// hostname, country code or name, AS number or organization, or app.
type flowSearch struct {
	query     string
	hasClient bool

	countries  *lookup.Countries
	asns       *lookup.ASNs
	classifier *apps.Classifier
	names      map[netip.Addr]*string
	hostnames  *lookup.Hostnames

	// about caches what is known of each address.
	about map[netip.Addr]string
}

// newFlowSearch prepares a search of flows, looking up the names of the
// remote hosts with the most traffic first.
func newFlowSearch(ctx context.Context, query string, hasClient bool, flows []orientedFlow,
	countries *lookup.Countries, asns *lookup.ASNs, classifier *apps.Classifier, hostnames *lookup.Hostnames,
) *flowSearch {
	s := &flowSearch{
		query: strings.ToLower(strings.TrimSpace(query)), hasClient: hasClient,
		countries: countries, asns: asns, classifier: classifier, hostnames: hostnames,
		names: map[netip.Addr]*string{}, about: map[netip.Addr]string{},
	}
	// Names only matter for searches that could be part of one.
	if s.query == "" || hostnames == nil || !strings.ContainsFunc(s.query, unicode.IsLetter) {
		return s
	}

	traffic := map[netip.Addr]uint64{}
	for _, flow := range flows {
		for _, addr := range s.ends(flow) {
			traffic[addr] += flow.total()
		}
	}
	addrs := make([]netip.Addr, 0, len(traffic))
	for addr := range traffic {
		addrs = append(addrs, addr)
	}
	slices.SortFunc(addrs, func(a, b netip.Addr) int { return cmp.Or(cmp.Compare(traffic[b], traffic[a]), a.Compare(b)) })
	for _, addr := range addrs[min(len(addrs), maxSearchLookups):] {
		if name, ok := hostnames.Cached(addr); ok {
			s.names[addr] = name
		}
	}
	for addr, name := range hostnames.LookupAll(ctx, addrs[:min(len(addrs), maxSearchLookups)]) {
		s.names[addr] = name
	}
	return s
}

// ends returns the ends of a flow a search looks at: the remote host, or
// both without a client.
func (s *flowSearch) ends(flow orientedFlow) []netip.Addr {
	addrs := []common.IPAddress{flow.peer}
	if !s.hasClient {
		addrs = []common.IPAddress{flow.record.SourceIPAddress, flow.record.DestinationIPAddress}
	}
	var ends []netip.Addr
	for _, addr := range addrs {
		if addr.Addr != nil {
			ends = append(ends, addr.Unmap())
		}
	}
	return ends
}

// describe returns what is known about an address, lowercase, one thing per
// line: its address, name, country code and name, AS number and
// organization, and app.
func (s *flowSearch) describe(addr netip.Addr) string {
	if about, ok := s.about[addr]; ok {
		return about
	}

	parts := []string{addr.String()}
	if name := s.names[addr]; name != nil {
		parts = append(parts, *name)
	}
	if code, name, ok := s.countries.Lookup(addr); ok {
		parts = append(parts, code, name)
	}
	if number, organization, ok := s.asns.Lookup(addr); ok {
		parts = append(parts, "as"+strconv.FormatUint(uint64(number), 10), organization)
	}
	if s.classifier != nil {
		if app := s.classifier.Classify(addr); app != apps.Other {
			parts = append(parts, app)
		}
	}

	about := strings.ToLower(strings.Join(parts, "\n"))
	s.about[addr] = about
	return about
}

// matches reports whether a flow matches the search.
func (s *flowSearch) matches(flow orientedFlow) bool {
	if s.query == "" {
		return true
	}
	if port, err := strconv.ParseUint(s.query, 10, 16); err == nil && uint16(port) == flow.record.Port {
		return true
	}
	if protocolNames[flow.record.IPProto] == s.query {
		return true
	}

	for _, addr := range s.ends(flow) {
		about := s.describe(addr)
		// Two letters are a country code, which has to match in full rather
		// than as part of a name.
		if len(s.query) == 2 && !strings.ContainsFunc(s.query, unicode.IsDigit) {
			if slices.Contains(strings.Split(about, "\n"), s.query) {
				return true
			}
			continue
		}
		if s.query == "other" && s.classifier != nil && s.classifier.Classify(addr) == apps.Other {
			return true
		}
		if strings.Contains(about, s.query) {
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
func pageFlows(flows []orientedFlow, page FlowsPage, matches func(orientedFlow) bool) ([]orientedFlow, int) {
	matching := make([]orientedFlow, 0, len(flows))
	for _, flow := range flows {
		if matches(flow) {
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
