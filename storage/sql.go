package storage

import (
	"database/sql"
	sq "github.com/Masterminds/squirrel"
	"github.com/duxet/netmon/common"
	"log"
	"net"
)

type FlowsFilter struct {
	MAC *common.MACAddress
	IP  *common.IPAddress
}

func GetFlows(db *sql.DB, filter FlowsFilter) []FlowRecord {
	var query = sq.
		Select("src_mac", "dst_mac", "src_ip", "dst_ip", "ip_proto", "port", "sum(in_bytes)::INT64", "sum(in_packets)::INT64", "sum(out_bytes)::INT64", "sum(out_packets)::INT64").
		From("flows").
		GroupBy("src_mac", "dst_mac", "src_ip", "dst_ip", "ip_proto", "port").
		OrderBy("sum(in_bytes) + sum(out_bytes) DESC")

	if filter.MAC != nil {
		var mac []byte = filter.MAC.HardwareAddr
		query = query.Where(sq.Or{sq.Eq{"src_mac": mac}, sq.Eq{"dst_mac": mac}})
	}

	if filter.IP != nil {
		ip := filter.IP.AsSlice()
		query = query.Where(sq.Or{sq.Eq{"src_ip": ip}, sq.Eq{"dst_ip": ip}})
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
		); err != nil {
			log.Fatal(err)
		}
		flows = append(flows, flow)
	}

	return flows
}

func GetClientFlows(db *sql.DB, clientId string) []FlowRecord {
	var query = sq.
		Select("src_mac", "dst_mac", "src_ip", "dst_ip", "ip_proto", "port", "sum(in_bytes)::INT64", "sum(in_packets)::INT64", "sum(out_bytes)::INT64", "sum(out_packets)::INT64").
		From("flows").
		GroupBy("src_mac", "dst_mac", "src_ip", "dst_ip", "ip_proto", "port").
		OrderBy("sum(in_bytes + out_bytes) DESC")

	if mac, err := net.ParseMAC(clientId); err == nil {
		query = query.Where(sq.Or{sq.Eq{"src_mac": mac}, sq.Eq{"dst_mac": mac}})
	}

	if ip := net.ParseIP(clientId); ip != nil {
		query = query.Where(sq.Or{sq.Eq{"src_ip": ip}, sq.Eq{"dst_ip": ip}})
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
			&flow.SourceIPAddress,
			&flow.DestinationIPAddress,
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
		log.Fatal(err)
	}

	return stats
}

func GetClients(db *sql.DB) []ClientRecord {
	rows, err := db.Query(`
		SELECT src_mac, src_ip, sum(in_bytes)::INT64, sum(in_packets)::INT64, sum(out_bytes)::INT64, sum(out_packets)::INT64
		FROM flows
		GROUP BY src_mac, src_ip
		ORDER BY sum(in_bytes + out_bytes) DESC
	`)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	var clients []ClientRecord
	for rows.Next() {
		var client ClientRecord
		if err := rows.Scan(
			&client.SourceMACAddress,
			&client.SourceIPAddress,
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
