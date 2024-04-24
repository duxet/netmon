package storage

import (
	"database/sql"
	"log"
	"net/netip"
)

func GetFlows(db *sql.DB) []FlowRecord {
	rows, err := db.Query(`
		SELECT src_mac, dst_mac, src_ip, dst_ip, ip_proto, port, sum(in_bytes)::INT64, sum(in_packets)::INT64, sum(out_bytes)::INT64, sum(out_packets)::INT64
		FROM flows
		GROUP BY src_mac, dst_mac, src_ip, dst_ip, ip_proto, port
	`)
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
	id, _ := netip.ParseAddr(clientId)

	rows, err := db.Query(`
		SELECT src_ip, dst_ip, ip_proto, port, sum(in_bytes)::INT64, sum(in_packets)::INT64, sum(out_bytes)::INT64, sum(out_packets)::INT64
		FROM flows
		WHERE src_ip = ? OR dst_ip = ?
		GROUP BY src_ip, dst_ip, ip_proto, port
		ORDER BY sum(in_bytes + out_bytes) DESC
	`, id.AsSlice(), id.AsSlice())
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
		ORDER BY SUM(in_bytes + out_bytes) DESC
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
