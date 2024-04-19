-- +migrate Up
CREATE TABLE flows(
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

-- +migrate Down
DROP TABLE flows;
