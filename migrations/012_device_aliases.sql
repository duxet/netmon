-- +migrate Up
-- MAC addresses merged into another device's, such as the random ones a
-- phone uses on different networks: their traffic counts as mac's, past and
-- to come.
CREATE TABLE device_aliases(
    alias BLOB PRIMARY KEY,
    mac BLOB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

-- +migrate Down
DROP TABLE device_aliases;
