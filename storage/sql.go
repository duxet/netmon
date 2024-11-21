package storage

import (
	"database/sql"
	"errors"
	sq "github.com/Masterminds/squirrel"
	"github.com/duxet/netmon/common"
	"log"
)

type FlowsFilter struct {
	ClientID *common.ClientID
	IP       *common.IPAddress
}

func GetFlows(db *sql.DB, filter FlowsFilter) []FlowRecord {
	var query = sq.
		Select("client_id", "ip_address", "ip_proto", "port", "sum(in_bytes)::INT64", "sum(in_packets)::INT64", "sum(out_bytes)::INT64", "sum(out_packets)::INT64").
		From("flows").
		GroupBy("client_id", "ip_address", "ip_proto", "port").
		OrderBy("sum(in_bytes) + sum(out_bytes) DESC")

	if filter.ClientID != nil {
		query = query.Where(sq.Eq{"client_id": filter.ClientID})
	}

	if filter.IP != nil {
		ip := filter.IP.AsSlice()
		query = query.Where(sq.Eq{"ip_address": ip})
	}

	rows, err := query.RunWith(db).Query()
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	var flows []FlowRecord

	for rows.Next() {
		var flow FlowRecord
		if err := rows.Scan(
			&flow.ClientID,
			&flow.IPAddress,
			&flow.IPProto,
			&flow.Port,
			&flow.InBytes,
			&flow.InPackets,
			&flow.OutBytes,
			&flow.OutPackets,
		); err != nil {
			log.Fatal(err)
		}
		flows = append(flows, flow)
	}

	return flows
}

func GetStats(db *sql.DB) StatsRecord {
	row := db.QueryRow(`
		SELECT count(DISTINCT client_id), sum(in_bytes + out_bytes)::INT64, sum(in_packets + out_packets)::INT64
		FROM flows
	`)

	var stats StatsRecord
	if err := row.Scan(
		&stats.TotalClients,
		&stats.TotalBytes,
		&stats.TotalPackets,
	); err != nil {
		log.Fatal(err)
	}

	return stats
}

func GetClientByMAC(db *sql.DB, mac common.MACAddress) (*ClientRecord, error) {
	var macByte []byte = mac.HardwareAddr
	query := sq.
		Select("id", "mac_address", "ip_addresses", "hostname").
		Where(sq.Eq{"mac_address": macByte}).
		From("clients")

	var client ClientRecord
	switch err := query.RunWith(db).QueryRow().Scan(
		&client.ID,
		&client.MACAddress,
		&client.IPAddresses,
		&client.Hostname,
	); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err == nil:
		return &client, nil
	default:
		return nil, err
	}
}

func GetClients(db *sql.DB) []ClientWithStatsRecord {
	var query = sq.
		Select("id", "mac_address", "ip_addresses", "hostname", "sum(in_bytes)::INT64", "sum(in_packets)::INT64", "sum(out_bytes)::INT64", "sum(out_packets)::INT64").
		From("clients").
		Join("flows ON id = client_id").
		GroupBy("id", "mac_address", "ip_addresses", "hostname")

	rows, err := query.RunWith(db).Query()
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	var clients []ClientWithStatsRecord
	for rows.Next() {
		var client ClientWithStatsRecord
		if err := rows.Scan(
			&client.MACAddress,
			&client.IPAddresses,
			&client.Hostname,
			&client.InBytes,
			&client.InPackets,
			&client.OutBytes,
			&client.OutPackets,
		); err != nil {
			log.Fatal(err)
		}
		clients = append(clients, client)
	}

	return clients
}

func GetTrafficMeasurements(db *sql.DB) []TrafficMeasurementRecord {
	rows, err := db.Query(`
		SELECT
			sum(in_bytes)::INT64 as in_bytes,
			sum(out_bytes)::INT64 out_bytes,
			time_bucket(interval '1 hour', created_at::TIMESTAMP) as bucket
		FROM FLOWS
		GROUP BY bucket
		ORDER BY bucket ASC
	`)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	var measurements []TrafficMeasurementRecord
	for rows.Next() {
		var measurement TrafficMeasurementRecord
		if err := rows.Scan(
			&measurement.InBytes,
			&measurement.OutBytes,
			&measurement.Date,
		); err != nil {
			log.Fatal(err)
		}
		measurements = append(measurements, measurement)
	}

	return measurements
}
