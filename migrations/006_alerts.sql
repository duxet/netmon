-- +migrate Up
-- When to send notifications. type picks the condition (new_device,
-- traffic_threshold) and params holds its settings as JSON.
-- repeat_interval_s is how often a firing alert is sent again; NULL never.
CREATE SEQUENCE alert_rules_id_seq;
CREATE TABLE alert_rules(
    id INTEGER PRIMARY KEY DEFAULT nextval('alert_rules_id_seq'),
    name VARCHAR NOT NULL,
    type VARCHAR NOT NULL,
    params VARCHAR NOT NULL,
    severity VARCHAR NOT NULL,
    repeat_interval_s INTEGER,
    notify_on_resolve BOOLEAN NOT NULL,
    enabled BOOLEAN NOT NULL,
    last_evaluated_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

-- The channels each rule notifies. DuckDB has no ON DELETE CASCADE, so
-- storage removes links along with rules and channels.
CREATE TABLE alert_rule_channels(
    rule_id INTEGER NOT NULL,
    channel_id INTEGER NOT NULL,
    PRIMARY KEY (rule_id, channel_id)
);

-- A rule firing for one subject, usually a device's MAC address. Alerts of
-- rules for events, such as a new device, are resolved when they start.
-- rule_name is a copy, so the history survives renaming or deleting the
-- rule; labels holds what the message was about as a JSON object.
CREATE SEQUENCE alerts_id_seq;
CREATE TABLE alerts(
    id INTEGER PRIMARY KEY DEFAULT nextval('alerts_id_seq'),
    rule_id INTEGER NOT NULL,
    rule_name VARCHAR NOT NULL,
    subject VARCHAR NOT NULL,
    state VARCHAR NOT NULL,
    severity VARCHAR NOT NULL,
    value DOUBLE,
    title VARCHAR NOT NULL,
    body VARCHAR NOT NULL,
    labels VARCHAR NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    last_evaluated_at TIMESTAMPTZ NOT NULL,
    last_notified_at TIMESTAMPTZ,
    resolved_at TIMESTAMPTZ
);

-- Every notification sent, or not, about an alert: kind is firing, reminder
-- or resolved; status is sent, failed or suppressed (by the rate limit).
CREATE SEQUENCE notification_deliveries_id_seq;
CREATE TABLE notification_deliveries(
    id INTEGER PRIMARY KEY DEFAULT nextval('notification_deliveries_id_seq'),
    alert_id INTEGER NOT NULL,
    channel_id INTEGER NOT NULL,
    kind VARCHAR NOT NULL,
    status VARCHAR NOT NULL,
    attempts INTEGER NOT NULL,
    error VARCHAR,
    created_at TIMESTAMPTZ NOT NULL
);

-- +migrate Down
DROP TABLE notification_deliveries;
DROP SEQUENCE notification_deliveries_id_seq;
DROP TABLE alerts;
DROP SEQUENCE alerts_id_seq;
DROP TABLE alert_rule_channels;
DROP TABLE alert_rules;
DROP SEQUENCE alert_rules_id_seq;
