package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
)

// Alert states.
const (
	AlertFiring   = "firing"
	AlertResolved = "resolved"
)

// AlertRecord is an alert rule firing for one subject, usually a device.
type AlertRecord struct {
	ID       int64
	RuleID   int64
	RuleName string
	// Subject identifies what the alert is about within its rule, e.g. a
	// MAC address; a rule has at most one firing alert per subject.
	Subject  string
	State    string
	Severity string
	// Value is what was measured, e.g. bytes, if the rule measures anything.
	Value *float64
	Title string
	Body  string
	// Labels describe the subject, e.g. its MAC and IP address and name.
	Labels          map[string]string
	StartedAt       time.Time
	LastEvaluatedAt time.Time
	LastNotifiedAt  *time.Time
	ResolvedAt      *time.Time
	// AcknowledgedAt is when someone said they know of the alert, which
	// isn't sent again while it lasts then, and AcknowledgedBy who.
	AcknowledgedAt *time.Time
	AcknowledgedBy string
}

// DeliveryRecord is a notification sent, or not, about an alert.
type DeliveryRecord struct {
	ID        int64
	AlertID   int64
	ChannelID int64
	// Kind is firing, reminder or resolved.
	Kind string
	// Status is sent, failed, suppressed (by the rate limit) or held (by a
	// snooze or quiet hours).
	Status   string
	Attempts int
	// Error explains a failure, with the channel's secrets redacted.
	Error     *string
	CreatedAt time.Time
}

const alertColumns = `id, rule_id, rule_name, subject, state, severity, value, title, body, labels,
	started_at, last_evaluated_at, last_notified_at, resolved_at, acknowledged_at, coalesce(acknowledged_by, '')`

func scanAlert(row interface{ Scan(...any) error }, a *AlertRecord) error {
	var labels string
	err := row.Scan(&a.ID, &a.RuleID, &a.RuleName, &a.Subject, &a.State, &a.Severity, &a.Value,
		&a.Title, &a.Body, &labels, &a.StartedAt, &a.LastEvaluatedAt, &a.LastNotifiedAt, &a.ResolvedAt,
		&a.AcknowledgedAt, &a.AcknowledgedBy)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(labels), &a.Labels)
}

func encodeLabels(labels map[string]string) (string, error) {
	if labels == nil {
		labels = map[string]string{}
	}
	data, err := json.Marshal(labels)
	return string(data), err
}

// InsertAlert adds an alert and returns its ID.
func InsertAlert(db *sql.DB, a AlertRecord) (int64, error) {
	labels, err := encodeLabels(a.Labels)
	if err != nil {
		return 0, fmt.Errorf("encode alert labels: %w", err)
	}

	var id int64
	err = db.QueryRow(`
		INSERT INTO alerts (rule_id, rule_name, subject, state, severity, value, title, body, labels,
			started_at, last_evaluated_at, last_notified_at, resolved_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING id`,
		a.RuleID, a.RuleName, a.Subject, a.State, a.Severity, a.Value, a.Title, a.Body, labels,
		a.StartedAt, a.LastEvaluatedAt, a.LastNotifiedAt, a.ResolvedAt,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert alert: %w", err)
	}
	return id, nil
}

// UpdateAlert stores what changed in an alert since it was inserted: its
// state, value, text, labels and times.
func UpdateAlert(db *sql.DB, a AlertRecord) error {
	labels, err := encodeLabels(a.Labels)
	if err != nil {
		return fmt.Errorf("encode alert labels: %w", err)
	}

	_, err = db.Exec(`
		UPDATE alerts
		SET state = ?, value = ?, title = ?, body = ?, labels = ?,
			last_evaluated_at = ?, last_notified_at = ?, resolved_at = ?
		WHERE id = ?`,
		a.State, a.Value, a.Title, a.Body, labels, a.LastEvaluatedAt, a.LastNotifiedAt, a.ResolvedAt, a.ID)
	if err != nil {
		return fmt.Errorf("update alert %d: %w", a.ID, err)
	}
	return nil
}

// AcknowledgeAlert records that someone knows of an alert, or with a zero
// time takes that back. It returns ErrNotFound when there is no such alert.
func AcknowledgeAlert(db *sql.DB, id int64, at time.Time, by string) error {
	var when, who any
	if !at.IsZero() {
		when, who = at, by
	}
	res, err := db.Exec(`UPDATE alerts SET acknowledged_at = ?, acknowledged_by = ? WHERE id = ?`, when, who, id)
	if err != nil {
		return fmt.Errorf("acknowledge alert %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("alert %d: %w", id, ErrNotFound)
	}
	return nil
}

// FiringAlerts returns the firing alerts of a rule, by subject.
func FiringAlerts(db *sql.DB, ruleID int64) (map[string]AlertRecord, error) {
	rows, err := db.Query(`SELECT `+alertColumns+` FROM alerts WHERE rule_id = ? AND state = ?`, ruleID, AlertFiring)
	alerts, err := collect(rows, err, func(rows *sql.Rows, a *AlertRecord) error {
		return scanAlert(rows, a)
	})
	if err != nil {
		return nil, fmt.Errorf("query firing alerts of rule %d: %w", ruleID, err)
	}

	bySubject := make(map[string]AlertRecord, len(alerts))
	for _, a := range alerts {
		bySubject[a.Subject] = a
	}
	return bySubject, nil
}

// AlertedSubjects returns the subjects a rule has had alerts about that
// started at or after since.
func AlertedSubjects(db *sql.DB, ruleID int64, since time.Time) (map[string]bool, error) {
	rows, err := db.Query(`SELECT DISTINCT subject FROM alerts WHERE rule_id = ? AND started_at >= ?`, ruleID, since)
	subjects, err := collect(rows, err, func(rows *sql.Rows, s *string) error {
		return rows.Scan(s)
	})
	if err != nil {
		return nil, fmt.Errorf("query alerted subjects of rule %d: %w", ruleID, err)
	}

	set := make(map[string]bool, len(subjects))
	for _, s := range subjects {
		set[s] = true
	}
	return set, nil
}

// resolveRuleAlerts resolves the firing alerts of a rule at now.
func resolveRuleAlerts(tx *sql.Tx, ruleID int64, now time.Time) error {
	_, err := tx.Exec(`UPDATE alerts SET state = ?, resolved_at = ? WHERE rule_id = ? AND state = ?`,
		AlertResolved, now, ruleID, AlertFiring)
	return err
}

// AlertsFilter selects alerts for ListAlerts.
type AlertsFilter struct {
	// State is "firing" or "resolved", or empty for both.
	State  string
	RuleID int64
	// Limit is the most alerts to return; zero is no limit.
	Limit  uint64
	Offset uint64
}

// ListAlerts returns the alerts matching filter, most recent first, and how
// many there are in all.
func ListAlerts(db *sql.DB, filter AlertsFilter) ([]AlertRecord, int, error) {
	where := sq.And{}
	if filter.State != "" {
		where = append(where, sq.Eq{"state": filter.State})
	}
	if filter.RuleID != 0 {
		where = append(where, sq.Eq{"rule_id": filter.RuleID})
	}

	var total int
	if err := sq.Select("count(*)").From("alerts").Where(where).RunWith(db).QueryRow().Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count alerts: %w", err)
	}

	query := sq.Select(alertColumns).From("alerts").Where(where).OrderBy("started_at DESC", "id DESC")
	if filter.Limit > 0 {
		query = query.Limit(filter.Limit)
	}
	if filter.Offset > 0 {
		query = query.Offset(filter.Offset)
	}

	rows, err := query.RunWith(db).Query()
	alerts, err := collect(rows, err, func(rows *sql.Rows, a *AlertRecord) error {
		return scanAlert(rows, a)
	})
	if err != nil {
		return nil, 0, fmt.Errorf("query alerts: %w", err)
	}
	return alerts, total, nil
}

// GetAlert returns the alert with the given ID, or ErrNotFound.
func GetAlert(db *sql.DB, id int64) (AlertRecord, error) {
	var a AlertRecord
	err := scanAlert(db.QueryRow(`SELECT `+alertColumns+` FROM alerts WHERE id = ?`, id), &a)
	if errors.Is(err, sql.ErrNoRows) {
		return AlertRecord{}, fmt.Errorf("alert %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return AlertRecord{}, fmt.Errorf("query alert %d: %w", id, err)
	}
	return a, nil
}

// InsertDelivery records a notification about an alert.
func InsertDelivery(db *sql.DB, d DeliveryRecord) error {
	_, err := db.Exec(`
		INSERT INTO notification_deliveries (alert_id, channel_id, kind, status, attempts, error, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		d.AlertID, d.ChannelID, d.Kind, d.Status, d.Attempts, d.Error, d.CreatedAt)
	if err != nil {
		return fmt.Errorf("record delivery of alert %d: %w", d.AlertID, err)
	}
	return nil
}

// Deliveries returns the notifications about the given alerts, oldest first,
// by alert ID.
func Deliveries(db *sql.DB, alertIDs []int64) (map[int64][]DeliveryRecord, error) {
	byAlert := map[int64][]DeliveryRecord{}
	if len(alertIDs) == 0 {
		return byAlert, nil
	}

	rows, err := sq.Select("id", "alert_id", "channel_id", "kind", "status", "attempts", "error", "created_at").
		From("notification_deliveries").
		Where(sq.Eq{"alert_id": alertIDs}).
		OrderBy("created_at", "id").
		RunWith(db).Query()
	deliveries, err := collect(rows, err, func(rows *sql.Rows, d *DeliveryRecord) error {
		return rows.Scan(&d.ID, &d.AlertID, &d.ChannelID, &d.Kind, &d.Status, &d.Attempts, &d.Error, &d.CreatedAt)
	})
	if err != nil {
		return nil, fmt.Errorf("query deliveries: %w", err)
	}

	for _, d := range deliveries {
		byAlert[d.AlertID] = append(byAlert[d.AlertID], d)
	}
	return byAlert, nil
}

// ExpireAlerts deletes resolved alerts and deliveries older than retention
// (zero keeps them forever), and returns how many alerts it deleted. Firing
// alerts are kept however old they are.
func ExpireAlerts(db *sql.DB, now time.Time, retention time.Duration) (int64, error) {
	if retention <= 0 {
		return 0, nil
	}
	cutoff := now.Add(-retention)

	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("expire alerts: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`DELETE FROM notification_deliveries WHERE created_at < ?`, cutoff); err != nil {
		return 0, fmt.Errorf("expire deliveries: %w", err)
	}
	res, err := tx.Exec(`DELETE FROM alerts WHERE state = ? AND resolved_at < ?`, AlertResolved, cutoff)
	if err != nil {
		return 0, fmt.Errorf("expire alerts: %w", err)
	}
	expired, _ := res.RowsAffected()

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("expire alerts: %w", err)
	}
	return expired, nil
}
