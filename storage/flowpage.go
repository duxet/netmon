package storage

import (
	"database/sql"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/duxet/netmon/common"
)

// orientedFlows returns a WITH clause that defines oriented: the flows
// matching filter, one row per flow (endpoints, protocol and port) with its
// traffic, seen from the filtered client or network as GetFlows' callers
// orient them. download is what the client or network received, whichever
// side opened the connection, upload what it sent, and peer the other end.
// Without a client or network, flows are seen from their source.
func orientedFlows(filter FlowsFilter) (string, []any, error) {
	grouped, args, err := filter.apply(sq.
		Select("src_mac", "dst_mac", "src_ip", "dst_ip", "ip_proto", "port",
			"sum(in_bytes)::INT64 AS in_b", "sum(in_packets)::INT64 AS in_p",
			"sum(out_bytes)::INT64 AS out_b", "sum(out_packets)::INT64 AS out_p").
		From("all_flows").
		GroupBy("src_mac", "dst_mac", "src_ip", "dst_ip", "ip_proto", "port")).
		ToSql()
	if err != nil {
		return "", nil, err
	}

	// A flow is the client's outbound one when the client is its source, or
	// isn't its destination either.
	srcIsClient, srcArgs := filter.isClient("src_mac", "src_ip")
	dstIsClient, dstArgs := filter.isClient("dst_mac", "dst_ip")
	args = append(args, srcArgs...)
	args = append(args, dstArgs...)

	return `WITH grouped AS (` + grouped + `),
		directed AS (
			SELECT *, (coalesce(` + srcIsClient + `, FALSE) OR NOT coalesce(` + dstIsClient + `, FALSE)) AS outbound
			FROM grouped
		),
		oriented AS (
			SELECT src_mac, dst_mac, src_ip, dst_ip, ip_proto, port, in_b, in_p, out_b, out_p,
				CASE WHEN outbound THEN dst_ip ELSE src_ip END AS peer,
				CASE WHEN outbound THEN in_b ELSE out_b END AS download,
				CASE WHEN outbound THEN out_b ELSE in_b END AS upload,
				in_p + out_p AS packets
			FROM directed
		)`, args, nil
}

// isClient returns a condition that is true for the end of a flow in macCol
// and ipCol that is the filtered client, or in the filtered network, with
// its arguments. Without either it is false.
func (filter FlowsFilter) isClient(macCol, ipCol string) (string, []any) {
	switch {
	case filter.MAC != nil:
		return macCol + " = ?", []any{[]byte(filter.MAC.HardwareAddr)}
	case filter.IP != nil:
		return ipCol + " = ?", []any{filter.IP.AsSlice()}
	case filter.Network != nil:
		return addrIn(ipCol, *filter.Network)
	default:
		return "FALSE", nil
	}
}

// FlowOrder sorts and pages flows.
type FlowOrder struct {
	// Sort is "total", "download", "upload" or "packets", each seen from the
	// filtered client or network.
	Sort string
	Asc  bool
	// Limit is the most flows to return, with 0 returning all of them.
	Limit  int
	Offset int
}

var flowSortColumns = map[string]string{
	"total":    "download + upload",
	"download": "download",
	"upload":   "upload",
	"packets":  "packets",
}

// addrOrder sorts the addresses in col as netip.Addr.Compare does: IPv4
// before IPv6, then by value, with missing ones first.
func addrOrder(col string) string {
	return "octet_length(" + col + ") NULLS FIRST, " + col + " NULLS FIRST"
}

// GetFlowsPage returns one page of the flows matching filter, sorted by
// order, and how many flows match in all. Ties are broken by the other end,
// the endpoints, protocol and port, so that pages stay the same across
// requests.
func GetFlowsPage(db *sql.DB, filter FlowsFilter, order FlowOrder) ([]FlowRecord, int, error) {
	with, args, err := orientedFlows(filter)
	if err != nil {
		return nil, 0, fmt.Errorf("query flows: %w", err)
	}

	sortColumn, ok := flowSortColumns[order.Sort]
	if !ok {
		sortColumn = flowSortColumns["total"]
	}
	direction := "DESC"
	if order.Asc {
		direction = "ASC"
	}

	query := with + `
		SELECT src_mac, dst_mac, src_ip, dst_ip, ip_proto, port, in_b, in_p, out_b, out_p, count(*) OVER () AS total
		FROM oriented
		ORDER BY ` + sortColumn + ` ` + direction + `, ` + addrOrder("peer") + `, ` + addrOrder("src_ip") + `, ` +
		addrOrder("dst_ip") + `, ip_proto, port, src_mac NULLS FIRST, dst_mac NULLS FIRST`
	if order.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", order.Limit)
	}
	if order.Offset > 0 {
		query += fmt.Sprintf(" OFFSET %d", order.Offset)
	}

	total := 0
	rows, err := db.Query(query, args...)
	flows, err := collect(rows, err, func(rows *sql.Rows, flow *FlowRecord) error {
		return rows.Scan(
			&flow.SourceMACAddress, &flow.DestinationMACAddress,
			&flow.SourceIPAddress, &flow.DestinationIPAddress,
			&flow.IPProto, &flow.Port,
			&flow.InBytes, &flow.InPackets, &flow.OutBytes, &flow.OutPackets,
			&total,
		)
	})
	if err != nil {
		return nil, 0, fmt.Errorf("query flows: %w", err)
	}

	// A page past the last flow has no rows to tell the total.
	if len(flows) == 0 && order.Offset > 0 {
		if err := db.QueryRow(with+` SELECT count(*) FROM oriented`, args...).Scan(&total); err != nil {
			return nil, 0, fmt.Errorf("count flows: %w", err)
		}
	}

	return flows, total, nil
}

// PeerTraffic is the traffic with one remote host, seen from the filtered
// client or network.
type PeerTraffic struct {
	// Peer is the remote host's address, which is missing for flows whose
	// end wasn't recorded.
	Peer     common.IPAddress
	Download uint64
	Upload   uint64
	Packets  uint64
	// Flows is the number of flows (endpoints, protocol and port) with it.
	Flows uint64
}

// GetPeerTraffic returns the traffic of the flows matching filter per remote
// host, most traffic first.
func GetPeerTraffic(db *sql.DB, filter FlowsFilter) ([]PeerTraffic, error) {
	with, args, err := orientedFlows(filter)
	if err != nil {
		return nil, fmt.Errorf("query traffic per host: %w", err)
	}

	rows, err := db.Query(with+`
		SELECT peer, sum(download)::INT64, sum(upload)::INT64, sum(packets)::INT64, count(*)
		FROM oriented
		GROUP BY peer
		ORDER BY sum(download) + sum(upload) DESC, `+addrOrder("peer"), args...)
	peers, err := collect(rows, err, func(rows *sql.Rows, p *PeerTraffic) error {
		return rows.Scan(&p.Peer, &p.Download, &p.Upload, &p.Packets, &p.Flows)
	})
	if err != nil {
		return nil, fmt.Errorf("query traffic per host: %w", err)
	}
	return peers, nil
}

// ServiceTraffic is the traffic to one protocol and port, seen from the
// filtered client or network.
type ServiceTraffic struct {
	IPProto  uint8
	Port     uint16
	Download uint64
	Upload   uint64
	// Flows is the number of flows (endpoints, protocol and port) to it.
	Flows uint64
}

// GetServiceTraffic returns the traffic of the flows matching filter per
// protocol and port, most traffic first.
func GetServiceTraffic(db *sql.DB, filter FlowsFilter) ([]ServiceTraffic, error) {
	with, args, err := orientedFlows(filter)
	if err != nil {
		return nil, fmt.Errorf("query traffic per service: %w", err)
	}

	rows, err := db.Query(with+`
		SELECT ip_proto, port, sum(download)::INT64, sum(upload)::INT64, count(*)
		FROM oriented
		GROUP BY ip_proto, port
		ORDER BY sum(download) + sum(upload) DESC, ip_proto, port`, args...)
	services, err := collect(rows, err, func(rows *sql.Rows, s *ServiceTraffic) error {
		return rows.Scan(&s.IPProto, &s.Port, &s.Download, &s.Upload, &s.Flows)
	})
	if err != nil {
		return nil, fmt.Errorf("query traffic per service: %w", err)
	}
	return services, nil
}
