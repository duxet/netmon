package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	sq "github.com/Masterminds/squirrel"
)

// RuleRecord is an alert rule: when to send notifications.
type RuleRecord struct {
	ID   int64
	Name string
	// Type picks the condition, e.g. "new_device"; Params holds its
	// settings as JSON, which the alerting package reads.
	Type     string
	Params   string
	Severity string
	// RepeatIntervalSeconds is how often a firing alert is sent again, or
	// nil to send it once.
	RepeatIntervalSeconds *int64
	NotifyOnResolve       bool
	Enabled               bool
	// ChannelIDs are the channels the rule notifies, in ascending order.
	ChannelIDs []int64
	// FiringAlerts is the number of the rule's alerts that are firing.
	FiringAlerts    int
	LastEvaluatedAt *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
	// SnoozedUntil holds back the rule's notifications until then.
	SnoozedUntil *time.Time
	// QuietFrom and QuietTo ("22:00", "07:00") hold back its notifications
	// every day between them, in netmon's time zone; empty for none.
	QuietFrom string
	QuietTo   string
}

const ruleColumns = `id, name, type, params, severity, repeat_interval_s, notify_on_resolve, enabled,
	(SELECT count(*) FROM alerts WHERE alerts.rule_id = alert_rules.id AND alerts.state = 'firing'),
	last_evaluated_at, created_at, updated_at, snoozed_until, coalesce(quiet_from, ''), coalesce(quiet_to, '')`

func scanRule(row interface{ Scan(...any) error }, r *RuleRecord) error {
	return row.Scan(&r.ID, &r.Name, &r.Type, &r.Params, &r.Severity, &r.RepeatIntervalSeconds,
		&r.NotifyOnResolve, &r.Enabled, &r.FiringAlerts, &r.LastEvaluatedAt, &r.CreatedAt, &r.UpdatedAt,
		&r.SnoozedUntil, &r.QuietFrom, &r.QuietTo)
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ruleChannels returns the channel IDs of the given rules, or of all rules
// for nil, by rule ID.
func ruleChannels(db *sql.DB, ruleIDs []int64) (map[int64][]int64, error) {
	query := sq.Select("rule_id", "channel_id").From("alert_rule_channels").OrderBy("rule_id", "channel_id")
	if ruleIDs != nil {
		query = query.Where(sq.Eq{"rule_id": ruleIDs})
	}

	rows, err := query.RunWith(db).Query()
	type link struct{ rule, channel int64 }
	links, err := collect(rows, err, func(rows *sql.Rows, l *link) error {
		return rows.Scan(&l.rule, &l.channel)
	})
	if err != nil {
		return nil, err
	}

	channels := map[int64][]int64{}
	for _, l := range links {
		channels[l.rule] = append(channels[l.rule], l.channel)
	}
	return channels, nil
}

// ListRules returns all alert rules, oldest first.
func ListRules(db *sql.DB) ([]RuleRecord, error) {
	rows, err := db.Query(`SELECT ` + ruleColumns + ` FROM alert_rules ORDER BY id`)
	rules, err := collect(rows, err, func(rows *sql.Rows, r *RuleRecord) error {
		return scanRule(rows, r)
	})
	if err != nil {
		return nil, fmt.Errorf("query alert rules: %w", err)
	}

	channels, err := ruleChannels(db, nil)
	if err != nil {
		return nil, fmt.Errorf("query alert rule channels: %w", err)
	}
	for i := range rules {
		rules[i].ChannelIDs = channels[rules[i].ID]
	}
	return rules, nil
}

// GetRule returns the alert rule with the given ID, or ErrNotFound.
func GetRule(db *sql.DB, id int64) (RuleRecord, error) {
	var r RuleRecord
	err := scanRule(db.QueryRow(`SELECT `+ruleColumns+` FROM alert_rules WHERE id = ?`, id), &r)
	if errors.Is(err, sql.ErrNoRows) {
		return RuleRecord{}, fmt.Errorf("alert rule %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return RuleRecord{}, fmt.Errorf("query alert rule %d: %w", id, err)
	}

	channels, err := ruleChannels(db, []int64{id})
	if err != nil {
		return RuleRecord{}, fmt.Errorf("query alert rule %d channels: %w", id, err)
	}
	r.ChannelIDs = channels[id]
	return r, nil
}

// setRuleChannels replaces the channels of a rule.
func setRuleChannels(tx *sql.Tx, ruleID int64, channelIDs []int64) error {
	if _, err := tx.Exec(`DELETE FROM alert_rule_channels WHERE rule_id = ?`, ruleID); err != nil {
		return err
	}
	ids := slices.Clone(channelIDs)
	slices.Sort(ids)
	for _, id := range slices.Compact(ids) {
		if _, err := tx.Exec(`INSERT INTO alert_rule_channels VALUES (?, ?)`, ruleID, id); err != nil {
			return err
		}
	}
	return nil
}

// CreateRule adds an alert rule with its channels, created at now, and
// returns it with its ID.
func CreateRule(db *sql.DB, r RuleRecord, now time.Time) (RuleRecord, error) {
	tx, err := db.Begin()
	if err != nil {
		return RuleRecord{}, fmt.Errorf("create alert rule: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var id int64
	err = tx.QueryRow(`
		INSERT INTO alert_rules (name, type, params, severity, repeat_interval_s, notify_on_resolve, enabled, created_at, updated_at,
			quiet_from, quiet_to)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING id`,
		r.Name, r.Type, r.Params, r.Severity, r.RepeatIntervalSeconds, r.NotifyOnResolve, r.Enabled, now, now,
		nullIfEmpty(r.QuietFrom), nullIfEmpty(r.QuietTo),
	).Scan(&id)
	if err != nil {
		return RuleRecord{}, fmt.Errorf("create alert rule: %w", err)
	}
	if err := setRuleChannels(tx, id, r.ChannelIDs); err != nil {
		return RuleRecord{}, fmt.Errorf("create alert rule channels: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return RuleRecord{}, fmt.Errorf("create alert rule: %w", err)
	}

	return GetRule(db, id)
}

// UpdateRule replaces everything but the evaluation time of the rule with
// r's ID, updated at now, and returns it. With resolve, the rule's firing
// alerts are resolved without a notification, e.g. because the condition
// changed. It returns ErrNotFound when there is no such rule.
func UpdateRule(db *sql.DB, r RuleRecord, now time.Time, resolve bool) (RuleRecord, error) {
	tx, err := db.Begin()
	if err != nil {
		return RuleRecord{}, fmt.Errorf("update alert rule %d: %w", r.ID, err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(`
		UPDATE alert_rules
		SET name = ?, type = ?, params = ?, severity = ?, repeat_interval_s = ?, notify_on_resolve = ?, enabled = ?, updated_at = ?,
			quiet_from = ?, quiet_to = ?
		WHERE id = ?`,
		r.Name, r.Type, r.Params, r.Severity, r.RepeatIntervalSeconds, r.NotifyOnResolve, r.Enabled, now,
		nullIfEmpty(r.QuietFrom), nullIfEmpty(r.QuietTo), r.ID)
	if err != nil {
		return RuleRecord{}, fmt.Errorf("update alert rule %d: %w", r.ID, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return RuleRecord{}, fmt.Errorf("alert rule %d: %w", r.ID, ErrNotFound)
	}
	if err := setRuleChannels(tx, r.ID, r.ChannelIDs); err != nil {
		return RuleRecord{}, fmt.Errorf("update alert rule %d channels: %w", r.ID, err)
	}
	if resolve {
		if err := resolveRuleAlerts(tx, r.ID, now); err != nil {
			return RuleRecord{}, fmt.Errorf("resolve alerts of rule %d: %w", r.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return RuleRecord{}, fmt.Errorf("update alert rule %d: %w", r.ID, err)
	}

	return GetRule(db, r.ID)
}

// DeleteRule removes the alert rule with the given ID and its channel links,
// resolving its firing alerts without a notification; the alerts stay in
// the history. It returns ErrNotFound when there is no such rule.
func DeleteRule(db *sql.DB, id int64, now time.Time) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("delete alert rule %d: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(`DELETE FROM alert_rules WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete alert rule %d: %w", id, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("alert rule %d: %w", id, ErrNotFound)
	}
	if _, err := tx.Exec(`DELETE FROM alert_rule_channels WHERE rule_id = ?`, id); err != nil {
		return fmt.Errorf("delete alert rule %d channels: %w", id, err)
	}
	if err := resolveRuleAlerts(tx, id, now); err != nil {
		return fmt.Errorf("resolve alerts of rule %d: %w", id, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete alert rule %d: %w", id, err)
	}
	return nil
}

// SnoozeRule holds back a rule's notifications until a time, or with nil no
// longer. It returns ErrNotFound when there is no such rule.
func SnoozeRule(db *sql.DB, id int64, until *time.Time) error {
	res, err := db.Exec(`UPDATE alert_rules SET snoozed_until = ? WHERE id = ?`, until, id)
	if err != nil {
		return fmt.Errorf("snooze alert rule %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("alert rule %d: %w", id, ErrNotFound)
	}
	return nil
}

// MarkRuleEvaluated records when a rule was last evaluated.
func MarkRuleEvaluated(db *sql.DB, id int64, at time.Time) error {
	if _, err := db.Exec(`UPDATE alert_rules SET last_evaluated_at = ? WHERE id = ?`, at, id); err != nil {
		return fmt.Errorf("mark alert rule %d evaluated: %w", id, err)
	}
	return nil
}
