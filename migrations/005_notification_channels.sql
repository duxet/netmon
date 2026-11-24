-- +migrate Up
-- Where notifications go. url is a shoutrrr service URL, e.g.
-- telegram://TOKEN@telegram?chats=CHAT_ID; it holds the service's
-- credentials, so the API never returns it. service is its scheme.
CREATE SEQUENCE notification_channels_id_seq;
CREATE TABLE notification_channels(
    id INTEGER PRIMARY KEY DEFAULT nextval('notification_channels_id_seq'),
    name VARCHAR NOT NULL,
    service VARCHAR NOT NULL,
    url VARCHAR NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

-- +migrate Down
DROP TABLE notification_channels;
DROP SEQUENCE notification_channels_id_seq;
