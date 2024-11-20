-- +migrate Up
CREATE SEQUENCE client_id_seq;

CREATE TABLE clients(
    id INTEGER DEFAULT nextval('client_id_seq') PRIMARY KEY,
    mac_address BLOB,
    ip_addresses BLOB[],
    hostname VARCHAR
);

CREATE TABLE flows(
    client_id INTEGER,
    ip_address BLOB,
    ip_proto UTINYINT,
    port USMALLINT,
    in_bytes UHUGEINT,
    in_packets UHUGEINT,
    out_bytes UHUGEINT,
    out_packets UHUGEINT,
    created_at TIMESTAMPTZ,
    FOREIGN KEY (client_id) REFERENCES clients (id)
);

-- +migrate Down
DROP SEQUENCE client_id_seq;
DROP TABLE clients;
DROP TABLE flows;
