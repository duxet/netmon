-- +migrate Up
-- Icons users picked for their devices, keyed by MAC address (as in flows),
-- apart from their names so a device can have either. icon is one of
-- api.DeviceIcons.
CREATE TABLE device_icons(
    mac BLOB PRIMARY KEY,
    icon VARCHAR NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

-- +migrate Down
DROP TABLE device_icons;
