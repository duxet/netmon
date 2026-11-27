-- +migrate Up
-- snoozed_until holds back a rule's notifications until then; quiet_from and
-- quiet_to ("22:00", "07:00", in netmon's time zone) every day between them.
ALTER TABLE alert_rules ADD COLUMN snoozed_until TIMESTAMPTZ;
ALTER TABLE alert_rules ADD COLUMN quiet_from VARCHAR;
ALTER TABLE alert_rules ADD COLUMN quiet_to VARCHAR;

-- An acknowledged alert isn't sent again while it lasts.
ALTER TABLE alerts ADD COLUMN acknowledged_at TIMESTAMPTZ;
ALTER TABLE alerts ADD COLUMN acknowledged_by VARCHAR;

-- +migrate Down
ALTER TABLE alerts DROP COLUMN acknowledged_by;
ALTER TABLE alerts DROP COLUMN acknowledged_at;
ALTER TABLE alert_rules DROP COLUMN quiet_to;
ALTER TABLE alert_rules DROP COLUMN quiet_from;
ALTER TABLE alert_rules DROP COLUMN snoozed_until;
