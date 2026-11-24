-- +migrate Up
-- Groups of the local network's addresses, such as VLANs or subnets.
-- prefixes holds the blocks of addresses the network uses, in CIDR notation,
-- as a JSON array of strings; an address belongs to the network with the
-- most specific block holding it. vlan is the network's 802.1Q VLAN ID,
-- which is only shown. color is one of networks.Colors.
CREATE SEQUENCE networks_id_seq;
CREATE TABLE networks(
    id INTEGER PRIMARY KEY DEFAULT nextval('networks_id_seq'),
    name VARCHAR NOT NULL,
    vlan INTEGER,
    color VARCHAR NOT NULL,
    prefixes VARCHAR NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

-- +migrate Down
DROP TABLE networks;
DROP SEQUENCE networks_id_seq;
