-- +migrate Up
-- When each MAC address was first and last seen in flows, on either side of
-- a flow, and the IP address it used last. storage.SyncDevices keeps it up to
-- date from the per-minute rows.
CREATE TABLE devices(
    mac BLOB PRIMARY KEY,
    last_ip BLOB NOT NULL,
    first_seen TIMESTAMPTZ NOT NULL,
    last_seen TIMESTAMPTZ NOT NULL
);

-- Seed from all history, hourly rollups included, so that devices seen
-- before this migration don't count as new. A MAC address can be NULL or
-- empty when the collector had none (e.g. an incomplete neighbor entry).
INSERT INTO devices
SELECT mac, arg_max(ip, created_at), min(created_at), max(created_at)
FROM (
    SELECT src_mac AS mac, src_ip AS ip, created_at FROM all_flows WHERE octet_length(src_mac) > 0
    UNION ALL
    SELECT dst_mac, dst_ip, created_at FROM all_flows WHERE octet_length(dst_mac) > 0
)
GROUP BY mac;

-- +migrate Down
DROP TABLE devices;
