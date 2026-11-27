-- +migrate Up
-- The names clients looked up that addresses answered for, from the DNS
-- server's log or captured DNS answers, as domains.Names learns them.
CREATE TABLE dns_names(
    ip BLOB NOT NULL,
    name VARCHAR NOT NULL,
    first_seen TIMESTAMPTZ NOT NULL,
    last_seen TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (ip, name)
);

-- +migrate Down
DROP TABLE dns_names;
