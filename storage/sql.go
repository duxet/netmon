package storage

import (
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/duxet/netmon/common"
	"github.com/duxet/netmon/networks"
)

// FlowsFilter restricts queries to flows of one client or network, and/or a
// time range.
type FlowsFilter struct {
	MAC *common.MACAddress
	IP  *common.IPAddress
	// Network restricts queries to flows with either endpoint in a network's
	// addresses, counted from the network's side as for a client. GetClients
	// returns the clients whose own address is in it instead.
	Network *networks.Scope
	// From is inclusive and To exclusive. Flows from before the previous
	// hour are stored per hour, so for them the range is effectively rounded
	// to whole hours.
	From *time.Time
	To   *time.Time
}

// apply restricts a query to flows where either endpoint matches the filter.
func (filter FlowsFilter) apply(query sq.SelectBuilder) sq.SelectBuilder {
	if filter.MAC != nil {
		var mac []byte = filter.MAC.HardwareAddr
		query = query.Where(sq.Or{sq.Eq{"src_mac": mac}, sq.Eq{"dst_mac": mac}})
	}

	if filter.IP != nil {
		ip := filter.IP.AsSlice()
		query = query.Where(sq.Or{sq.Eq{"src_ip": ip}, sq.Eq{"dst_ip": ip}})
	}

	if filter.Network != nil {
		src, srcArgs := addrIn("src_ip", *filter.Network)
		dst, dstArgs := addrIn("dst_ip", *filter.Network)
		query = query.Where(sq.Or{sq.Expr(src, srcArgs...), sq.Expr(dst, dstArgs...)})
	}

	if filter.From != nil {
		query = query.Where("created_at >= ?::TIMESTAMPTZ", *filter.From)
	}

	if filter.To != nil {
		query = query.Where("created_at < ?::TIMESTAMPTZ", *filter.To)
	}

	return query
}

// outbound returns a condition that is true for flows the filtered client
// or network originated (it is the source), with its arguments. Without
// either every flow counts as originated from the local network.
func (filter FlowsFilter) outbound() (string, []any) {
	switch {
	case filter.MAC != nil:
		return "src_mac = ?", []any{[]byte(filter.MAC.HardwareAddr)}
	case filter.IP != nil:
		return "src_ip = ?", []any{filter.IP.AsSlice()}
	case filter.Network != nil:
		return addrIn("src_ip", *filter.Network)
	default:
		return "TRUE", nil
	}
}

// addrIn returns a condition that is true when the address in column col
// belongs to the scope, with its arguments. Addresses are BLOBs of 4 bytes
// for IPv4 and 16 for IPv6, which compare byte by byte, so a block is the
// addresses of its family's length between its first and last address.
func addrIn(col string, scope networks.Scope) (string, []any) {
	if len(scope) == 0 {
		return "FALSE", nil
	}

	var b strings.Builder
	var args []any
	b.WriteString("(")
	for i, block := range scope {
		if i > 0 {
			b.WriteString(" OR ")
		}
		fmt.Fprintf(&b, "(octet_length(%s) = %d AND %s BETWEEN ? AND ?", col, block.Range.First.BitLen()/8, col)
		args = append(args, block.Range.First.AsSlice(), block.Range.Last.AsSlice())
		for _, hole := range block.Holes {
			fmt.Fprintf(&b, " AND %s NOT BETWEEN ? AND ?", col)
			args = append(args, hole.First.AsSlice(), hole.Last.AsSlice())
		}
		b.WriteString(")")
	}
	b.WriteString(")")
	return b.String(), args
}

// collect runs a query and scans every row with scan, closing the rows and
// reporting errors from the query, scanning and iteration alike.
func collect[T any](rows *sql.Rows, queryErr error, scan func(*sql.Rows, *T) error) ([]T, error) {
	if queryErr != nil {
		return nil, queryErr
	}
	defer rows.Close()

	var records []T
	for rows.Next() {
		var record T
		if err := scan(rows, &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return records, nil
}

// hourBucket groups TIMESTAMPTZ values by UTC hour. Casting to TIMESTAMP
// first would bucket in the server's local time and shift the results by
// its UTC offset.
const hourBucket = "time_bucket(INTERVAL '1 hour', created_at)"

// GetFlows returns traffic per flow (endpoints, protocol and port), most
// traffic first.
func GetFlows(db *sql.DB, filter FlowsFilter) ([]FlowRecord, error) {
	query := filter.apply(sq.
		Select("src_mac", "dst_mac", "src_ip", "dst_ip", "ip_proto", "port", "sum(in_bytes)::INT64", "sum(in_packets)::INT64", "sum(out_bytes)::INT64", "sum(out_packets)::INT64").
		From("all_flows").
		GroupBy("src_mac", "dst_mac", "src_ip", "dst_ip", "ip_proto", "port").
		OrderBy("sum(in_bytes) + sum(out_bytes) DESC"))

	rows, err := query.RunWith(db).Query()
	flows, err := collect(rows, err, func(rows *sql.Rows, flow *FlowRecord) error {
		return rows.Scan(
			&flow.SourceMACAddress,
			&flow.DestinationMACAddress,
			&flow.SourceIPAddress,
			&flow.DestinationIPAddress,
			&flow.IPProto,
			&flow.Port,
			&flow.InBytes,
			&flow.InPackets,
			&flow.OutBytes,
			&flow.OutPackets,
		)
	})
	if err != nil {
		return nil, fmt.Errorf("query flows: %w", err)
	}

	return flows, nil
}

// GetStats returns the number of clients that opened connections, and the
// traffic. With a network filter, only the network's own clients count.
func GetStats(db *sql.DB, filter FlowsFilter) (StatsRecord, error) {
	clients := sq.Expr("count(DISTINCT src_mac)")
	if filter.Network != nil {
		outbound, args := filter.outbound()
		clients = sq.Expr("count(DISTINCT CASE WHEN "+outbound+" THEN src_mac END)", args...)
	}

	query := filter.apply(sq.
		Select().
		Column(clients).
		Columns("sum(in_bytes + out_bytes)::INT64", "sum(in_packets + out_packets)::INT64").
		From("all_flows"))

	var stats StatsRecord
	if err := query.RunWith(db).QueryRow().Scan(
		&stats.TotalClients,
		&stats.TotalBytes,
		&stats.TotalPackets,
	); err != nil {
		return StatsRecord{}, fmt.Errorf("query stats: %w", err)
	}

	return stats, nil
}

// clientIP is the address of the local endpoint of a flow, as GetClients
// counts it: the source when it has a MAC address, else the destination.
const clientIP = "CASE WHEN src_mac IS NOT NULL THEN src_ip ELSE dst_ip END"

// GetClients returns traffic per local (MAC, IP) pair, most traffic first.
// With a network filter, it returns the pairs whose IP address is in the
// network.
func GetClients(db *sql.DB, filter FlowsFilter) ([]ClientRecord, error) {
	network := filter.Network
	filter.Network = nil

	query := filter.apply(sq.
		Select(
			"CASE WHEN src_mac IS NOT NULL THEN src_mac ELSE dst_mac END AS mac",
			clientIP+" AS ip",
			"sum(in_bytes)::INT64",
			"sum(in_packets)::INT64",
			"sum(out_bytes)::INT64",
			"sum(out_packets)::INT64",
		).
		From("all_flows").
		GroupBy("mac", "ip").
		Having("mac IS NOT NULL").
		OrderBy("sum(in_bytes + out_bytes) DESC"))
	if network != nil {
		cond, args := addrIn("("+clientIP+")", *network)
		query = query.Where(cond, args...)
	}

	rows, err := query.RunWith(db).Query()
	clients, err := collect(rows, err, func(rows *sql.Rows, client *ClientRecord) error {
		return rows.Scan(
			&client.MACAddress,
			&client.IPAddress,
			&client.InBytes,
			&client.InPackets,
			&client.OutBytes,
			&client.OutPackets,
		)
	})
	if err != nil {
		return nil, fmt.Errorf("query clients: %w", err)
	}

	return clients, nil
}

// GetTrafficMeasurements returns traffic per hour, oldest first. With a
// client filter, it is counted from that client's side: what it received is
// InBytes even when the other host opened the connection.
func GetTrafficMeasurements(db *sql.DB, filter FlowsFilter) ([]TrafficMeasurementRecord, error) {
	outbound, args := filter.outbound()

	query := filter.apply(sq.
		Select().
		Column(sq.Expr("sum(CASE WHEN "+outbound+" THEN in_bytes ELSE out_bytes END)::INT64", args...)).
		Column(sq.Expr("sum(CASE WHEN "+outbound+" THEN out_bytes ELSE in_bytes END)::INT64", args...)).
		Column(hourBucket + " AS bucket").
		From("all_flows").
		GroupBy("bucket").
		OrderBy("bucket ASC"))

	rows, err := query.RunWith(db).Query()
	measurements, err := collect(rows, err, func(rows *sql.Rows, measurement *TrafficMeasurementRecord) error {
		return rows.Scan(
			&measurement.InBytes,
			&measurement.OutBytes,
			&measurement.Date,
		)
	})
	if err != nil {
		return nil, fmt.Errorf("query traffic measurements: %w", err)
	}

	return measurements, nil
}

func scanEndpointPair(rows *sql.Rows, record *EndpointPairTrafficRecord, extra ...any) error {
	return rows.Scan(append([]any{
		&record.SourceIPAddress,
		&record.DestinationIPAddress,
		&record.InBytes,
		&record.InPackets,
		&record.OutBytes,
		&record.OutPackets,
	}, extra...)...)
}

// GetEndpointPairTraffic returns traffic totals grouped by source and destination IP address.
func GetEndpointPairTraffic(db *sql.DB, filter FlowsFilter) ([]EndpointPairTrafficRecord, error) {
	query := filter.apply(sq.
		Select("src_ip", "dst_ip", "sum(in_bytes)::INT64", "sum(in_packets)::INT64", "sum(out_bytes)::INT64", "sum(out_packets)::INT64").
		From("all_flows").
		GroupBy("src_ip", "dst_ip"))

	rows, err := query.RunWith(db).Query()
	records, err := collect(rows, err, func(rows *sql.Rows, record *EndpointPairTrafficRecord) error {
		return scanEndpointPair(rows, record)
	})
	if err != nil {
		return nil, fmt.Errorf("query endpoint pair traffic: %w", err)
	}

	return records, nil
}

// GetEndpointPairTrafficByHour is GetEndpointPairTraffic per hour, oldest first.
func GetEndpointPairTrafficByHour(db *sql.DB, filter FlowsFilter) ([]EndpointPairTrafficRecord, error) {
	query := filter.apply(sq.
		Select("src_ip", "dst_ip", "sum(in_bytes)::INT64", "sum(in_packets)::INT64", "sum(out_bytes)::INT64", "sum(out_packets)::INT64").
		Column(hourBucket+" AS bucket").
		From("all_flows").
		GroupBy("src_ip", "dst_ip", "bucket").
		OrderBy("bucket ASC"))

	rows, err := query.RunWith(db).Query()
	records, err := collect(rows, err, func(rows *sql.Rows, record *EndpointPairTrafficRecord) error {
		return scanEndpointPair(rows, record, &record.Hour)
	})
	if err != nil {
		return nil, fmt.Errorf("query hourly endpoint pair traffic: %w", err)
	}

	return records, nil
}

// networkOf returns an expression for the ID of the network the address in
// column col belongs to, NULL for none, with its arguments. The scopes of
// networks don't overlap, so the order of the cases doesn't matter.
func networkOf(col string, ids []int64, scopes map[int64]networks.Scope) (string, []any) {
	var b strings.Builder
	var args []any
	b.WriteString("CASE")
	for _, id := range ids {
		cond, condArgs := addrIn(col, scopes[id])
		fmt.Fprintf(&b, " WHEN %s THEN %d", cond, id)
		args = append(args, condArgs...)
	}
	b.WriteString(" END")
	return b.String(), args
}

// GetNetworkTraffic returns the traffic of flows with an endpoint in any of
// the networks, given by their scopes, per source and destination network,
// in the filter's time range. Only the time range of the filter is used.
func GetNetworkTraffic(db *sql.DB, scopes map[int64]networks.Scope, filter FlowsFilter) ([]NetworkTrafficRecord, error) {
	if len(scopes) == 0 {
		return nil, nil
	}
	ids := make([]int64, 0, len(scopes))
	for id := range scopes {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	src, srcArgs := networkOf("src_ip", ids, scopes)
	dst, dstArgs := networkOf("dst_ip", ids, scopes)
	inner := FlowsFilter{From: filter.From, To: filter.To}.apply(sq.
		Select().
		Column(sq.Expr(src+" AS src_net", srcArgs...)).
		Column(sq.Expr(dst+" AS dst_net", dstArgs...)).
		Columns("in_bytes", "in_packets", "out_bytes", "out_packets").
		From("all_flows"))

	query := sq.
		Select("src_net", "dst_net", "sum(in_bytes)::INT64", "sum(in_packets)::INT64", "sum(out_bytes)::INT64", "sum(out_packets)::INT64").
		FromSelect(inner, "f").
		Where("src_net IS NOT NULL OR dst_net IS NOT NULL").
		GroupBy("src_net", "dst_net")

	rows, err := query.RunWith(db).Query()
	records, err := collect(rows, err, func(rows *sql.Rows, r *NetworkTrafficRecord) error {
		var src, dst sql.NullInt64
		if err := rows.Scan(&src, &dst, &r.InBytes, &r.InPackets, &r.OutBytes, &r.OutPackets); err != nil {
			return err
		}
		if src.Valid {
			r.Source = &src.Int64
		}
		if dst.Valid {
			r.Destination = &dst.Int64
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("query network traffic: %w", err)
	}
	return records, nil
}
