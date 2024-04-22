package storage

import (
	"database/sql"
	"github.com/duxet/netmon/model"
	"log"
	"net/netip"
)

func GetFlows(db *sql.DB) []model.Flow {
	rows, err := db.Query(`
		SELECT src_mac, dst_mac, src_ip, dst_ip, ip_proto, port, sum(in_bytes)::INT64, sum(in_packets)::INT64, sum(out_bytes)::INT64, sum(out_packets)::INT64
		FROM flows
		GROUP BY src_mac, dst_mac, src_ip, dst_ip, ip_proto, port
	`)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	var flows []model.Flow

	for rows.Next() {
		var flow model.Flow
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

func GetClientFlows(db *sql.DB, clientId string) []model.Flow {
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

	var flows []model.Flow

	for rows.Next() {
		var flow model.Flow
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

func GetStats(db *sql.DB) model.Stats {
	row := db.QueryRow(
		`SELECT count(DISTINCT src_ip), sum(in_bytes + out_bytes)::INT64, sum(in_packets + out_packets)::INT64
		FROM flows
	`)

	var stats model.Stats
	if err := row.Scan(
		&stats.TotalClients,
		&stats.TotalBytes,
		&stats.TotalPackets,
	); err != nil {
		log.Fatal(err)
	}

	return stats
}

func GetClients(db *sql.DB) []model.Client {
	rows, err := db.Query(`
		SELECT src_ip, sum(in_bytes)::INT64, sum(in_packets)::INT64, sum(out_bytes)::INT64, sum(out_packets)::INT64
		FROM flows
		GROUP BY src_ip
		ORDER BY SUM(in_bytes + out_bytes) DESC
	`)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	var clients []model.Client
	for rows.Next() {
		var client model.Client
		if err := rows.Scan(
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
