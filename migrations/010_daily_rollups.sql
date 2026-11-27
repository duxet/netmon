-- +migrate Up
-- Hourly rows older than hourly_days, summed per flow and UTC day by
-- storage.Compact. created_at is the start of the day.
CREATE TABLE flows_daily(
    src_mac BLOB,
    dst_mac BLOB,
    src_ip BLOB,
    dst_ip BLOB,
    ip_proto UTINYINT,
    port USMALLINT,
    in_bytes UHUGEINT,
    in_packets UHUGEINT,
    out_bytes UHUGEINT,
    out_packets UHUGEINT,
    created_at TIMESTAMPTZ
);

-- Every row, whatever its resolution. hours is how long a row covers: an
-- hour at most for per-minute and hourly rows, a day for daily ones.
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

-- +migrate Down
CREATE OR REPLACE VIEW all_flows AS
    SELECT src_mac, dst_mac, src_ip, dst_ip, ip_proto, port,
           in_bytes, in_packets, out_bytes, out_packets, created_at
    FROM flows
    UNION ALL
    SELECT src_mac, dst_mac, src_ip, dst_ip, ip_proto, port,
           in_bytes, in_packets, out_bytes, out_packets, created_at
    FROM flows_hourly;
DROP TABLE flows_daily;
