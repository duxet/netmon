-- +migrate Up
-- Names users gave their devices, keyed by MAC address (as in flows).
CREATE TABLE device_names(
    mac BLOB PRIMARY KEY,
    name VARCHAR NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

-- +migrate Down
DROP TABLE device_names;
