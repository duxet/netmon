-- +migrate Up
-- The names devices go by on the local network, from DHCP leases or mDNS,
-- keyed by MAC address (as in flows), so that a device keeps its name when
-- its lease is gone or it is offline. source is "dhcp" or "mdns".
CREATE TABLE device_hostnames(
    mac BLOB PRIMARY KEY,
    name VARCHAR NOT NULL,
    source VARCHAR NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

-- +migrate Down
DROP TABLE device_hostnames;
