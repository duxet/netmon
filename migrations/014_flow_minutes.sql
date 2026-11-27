-- +migrate Up
-- minutes is how long a row covers at most: a minute for per-minute rows,
-- an hour for hourly ones and a day for daily ones; hours says the same in
-- whole hours.
CREATE OR REPLACE VIEW all_flows AS
    SELECT src_mac, dst_mac, src_ip, dst_ip, ip_proto, port,
           in_bytes, in_packets, out_bytes, out_packets, created_at, 1 AS hours, 1 AS minutes
    FROM flows
    UNION ALL
    SELECT src_mac, dst_mac, src_ip, dst_ip, ip_proto, port,
           in_bytes, in_packets, out_bytes, out_packets, created_at, 1 AS hours, 60 AS minutes
    FROM flows_hourly
    UNION ALL
    SELECT src_mac, dst_mac, src_ip, dst_ip, ip_proto, port,
           in_bytes, in_packets, out_bytes, out_packets, created_at, 24 AS hours, 1440 AS minutes
    FROM flows_daily;

-- +migrate Down
CREATE OR REPLACE VIEW all_flows AS
    SELECT src_mac, dst_mac, src_ip, dst_ip, ip_proto, port,
           in_bytes, in_packets, out_bytes, out_packets, created_at, 1 AS hours
    FROM flows
    UNION ALL
    SELECT src_mac, dst_mac, src_ip, dst_ip, ip_proto, port,
           in_bytes, in_packets, out_bytes, out_packets, created_at, 1 AS hours
    FROM flows_hourly
    UNION ALL
    SELECT src_mac, dst_mac, src_ip, dst_ip, ip_proto, port,
           in_bytes, in_packets, out_bytes, out_packets, created_at, 24 AS hours
    FROM flows_daily;
