package storage

import (
	"database/sql"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/duxet/netmon/common"
)

type FlowsFilter struct {
	MAC *common.MACAddress
	IP  *common.IPAddress
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

	return query
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

func GetFlows(db *sql.DB, filter FlowsFilter) ([]FlowRecord, error) {
	query := filter.apply(sq.
		Select("src_mac", "dst_mac", "src_ip", "dst_ip", "ip_proto", "port", "sum(in_bytes)::INT64", "sum(in_packets)::INT64", "sum(out_bytes)::INT64", "sum(out_packets)::INT64").
		From("flows").
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

func GetStats(db *sql.DB) (StatsRecord, error) {
	row := db.QueryRow(
		`SELECT count(DISTINCT src_mac), sum(in_bytes + out_bytes)::INT64, sum(in_packets + out_packets)::INT64
		FROM flows
	`)

	var stats StatsRecord
	if err := row.Scan(
		&stats.TotalClients,
		&stats.TotalBytes,
		&stats.TotalPackets,
	); err != nil {
		return StatsRecord{}, fmt.Errorf("query stats: %w", err)
	}

	return stats, nil
}

func GetClients(db *sql.DB) ([]ClientRecord, error) {
	rows, err := db.Query(`
		SELECT
		    CASE WHEN src_mac IS NOT NULL THEN src_mac ELSE dst_mac END AS mac,
		    CASE WHEN src_mac IS NOT NULL THEN src_ip ELSE dst_ip END AS ip,
		    sum(in_bytes)::INT64,
		    sum(in_packets)::INT64,
		    sum(out_bytes)::INT64,
		    sum(out_packets)::INT64
		FROM flows
		GROUP BY mac, ip
		HAVING mac IS NOT NULL
		ORDER BY sum(in_bytes + out_bytes) DESC
	`)
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

func GetTrafficMeasurements(db *sql.DB) ([]TrafficMeasurementRecord, error) {
	rows, err := db.Query(`
		SELECT
			sum(in_bytes)::INT64 as in_bytes,
			sum(out_bytes)::INT64 out_bytes,
			time_bucket(interval '1 hour', created_at::TIMESTAMP) as bucket
		FROM FLOWS
		GROUP BY bucket
		ORDER BY bucket ASC
	`)
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

// GetEndpointPairTraffic returns traffic totals grouped by source and destination IP address.
func GetEndpointPairTraffic(db *sql.DB, filter FlowsFilter) ([]EndpointPairTrafficRecord, error) {
	query := filter.apply(sq.
		Select("src_ip", "dst_ip", "sum(in_bytes)::INT64", "sum(in_packets)::INT64", "sum(out_bytes)::INT64", "sum(out_packets)::INT64").
		From("flows").
		GroupBy("src_ip", "dst_ip"))

	rows, err := query.RunWith(db).Query()
	records, err := collect(rows, err, func(rows *sql.Rows, record *EndpointPairTrafficRecord) error {
		return rows.Scan(
			&record.SourceIPAddress,
			&record.DestinationIPAddress,
			&record.InBytes,
			&record.InPackets,
			&record.OutBytes,
			&record.OutPackets,
		)
	})
	if err != nil {
		return nil, fmt.Errorf("query endpoint pair traffic: %w", err)
	}

	return records, nil
}
