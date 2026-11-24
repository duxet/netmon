-- +migrate Up
-- Flows from complete hours, summed per flow and hour by storage.Compact.
-- created_at is the start of the hour.
CREATE TABLE flows_hourly(
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

-- Recent per-minute rows and older hourly rollups, for queries that need both.
CREATE VIEW all_flows AS
    SELECT src_mac, dst_mac, src_ip, dst_ip, ip_proto, port,
           in_bytes, in_packets, out_bytes, out_packets, created_at
    FROM flows
    UNION ALL
    SELECT src_mac, dst_mac, src_ip, dst_ip, ip_proto, port,
           in_bytes, in_packets, out_bytes, out_packets, created_at
    FROM flows_hourly;

-- +migrate Down
DROP VIEW all_flows;
DROP TABLE flows_hourly;
