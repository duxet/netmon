package storage

import (
	"database/sql"
	"errors"
	"slices"
	"testing"
	"time"
)

var ruleTime = time.Date(2026, 11, 24, 18, 0, 0, 0, time.UTC)

func newChannel(t *testing.T, db *sql.DB, name string) ChannelRecord {
	t.Helper()
	c, err := CreateChannel(db, ChannelRecord{Name: name, Service: "ntfy", URL: "ntfy://ntfy.sh/" + name, Enabled: true}, ruleTime)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func newRule(t *testing.T, db *sql.DB, channelIDs ...int64) RuleRecord {
	t.Helper()
	hour := int64(3600)
	r, err := CreateRule(db, RuleRecord{
		Name: "Big downloads", Type: "traffic_threshold", Params: `{"threshold_bytes": 1000}`, Severity: "warning",
		RepeatIntervalSeconds: &hour, NotifyOnResolve: true, Enabled: true, ChannelIDs: channelIDs,
	}, ruleTime)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func newAlert(t *testing.T, db *sql.DB, a AlertRecord) AlertRecord {
	t.Helper()
	if a.State == "" {
		a.State = AlertFiring
	}
	if a.LastEvaluatedAt.IsZero() {
		a.LastEvaluatedAt = a.StartedAt
	}
	id, err := InsertAlert(db, a)
	if err != nil {
		t.Fatal(err)
	}
	a.ID = id
	return a
}

func TestRules(t *testing.T) {
	db := openTestDB(t)
	phone := newChannel(t, db, "phone")
	email := newChannel(t, db, "email")

	// Channels are kept sorted and without duplicates.
	rule := newRule(t, db, email.ID, phone.ID, email.ID)
	if rule.ID == 0 || !slices.Equal(rule.ChannelIDs, []int64{phone.ID, email.ID}) ||
		*rule.RepeatIntervalSeconds != 3600 || rule.LastEvaluatedAt != nil || !rule.CreatedAt.Equal(ruleTime) {
		t.Errorf("created = %+v", rule)
	}
	if c, _ := GetChannel(db, phone.ID); c.UsedByRules != 1 {
		t.Errorf("phone used by %d rules, want 1", c.UsedByRules)
	}

	newAlert(t, db, AlertRecord{RuleID: rule.ID, RuleName: rule.Name, Subject: "aa", Severity: "warning", StartedAt: ruleTime})
	if r, _ := GetRule(db, rule.ID); r.FiringAlerts != 1 {
		t.Errorf("firing alerts = %d, want 1", r.FiringAlerts)
	}

	// Renaming keeps the alert firing; changing what the rule checks resolves it.
	rule.Name = "Huge downloads"
	rule.RepeatIntervalSeconds = nil
	rule.ChannelIDs = []int64{email.ID}
	updated, err := UpdateRule(db, rule, ruleTime.Add(time.Hour), false)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Huge downloads" || updated.RepeatIntervalSeconds != nil ||
		!slices.Equal(updated.ChannelIDs, []int64{email.ID}) || updated.FiringAlerts != 1 {
		t.Errorf("updated = %+v", updated)
	}
	if updated, _ = UpdateRule(db, updated, ruleTime.Add(2*time.Hour), true); updated.FiringAlerts != 0 {
		t.Errorf("firing alerts after resolving = %d, want 0", updated.FiringAlerts)
	}

	if err := MarkRuleEvaluated(db, rule.ID, ruleTime.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	rules, err := ListRules(db)
	if err != nil || len(rules) != 1 || rules[0].LastEvaluatedAt == nil || !rules[0].LastEvaluatedAt.Equal(ruleTime.Add(3*time.Hour)) {
		t.Errorf("rules = %+v, %v; want the rule, evaluated", rules, err)
	}

	// Deleting a channel takes it off the rule.
	if err := DeleteChannel(db, email.ID); err != nil {
		t.Fatal(err)
	}
	if r, _ := GetRule(db, rule.ID); len(r.ChannelIDs) != 0 {
		t.Errorf("channels after deleting the channel = %v", r.ChannelIDs)
	}

	// Deleting the rule resolves its alerts but keeps them.
	newAlert(t, db, AlertRecord{RuleID: rule.ID, RuleName: rule.Name, Subject: "bb", Severity: "warning", StartedAt: ruleTime})
	if err := DeleteRule(db, rule.ID, ruleTime.Add(4*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := GetRule(db, rule.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetRule after delete = %v, want ErrNotFound", err)
	}
	alerts, total, err := ListAlerts(db, AlertsFilter{})
	if err != nil || total != 2 || len(alerts) != 2 || alerts[0].State != AlertResolved || alerts[1].State != AlertResolved {
		t.Errorf("alerts after deleting the rule = %+v (%d), %v; want both, resolved", alerts, total, err)
	}
	if c, _ := GetChannel(db, phone.ID); c.UsedByRules != 0 {
		t.Errorf("phone used by %d rules after the delete, want 0", c.UsedByRules)
	}
}

func TestMissingRules(t *testing.T) {
	db := openTestDB(t)
	if _, err := UpdateRule(db, RuleRecord{ID: 42, Name: "x", Type: "new_device", Params: "{}", Severity: "info"}, ruleTime, false); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateRule = %v, want ErrNotFound", err)
	}
	if err := DeleteRule(db, 42, ruleTime); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteRule = %v, want ErrNotFound", err)
	}
	if _, err := GetAlert(db, 42); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetAlert = %v, want ErrNotFound", err)
	}
}

func TestAlerts(t *testing.T) {
	db := openTestDB(t)
	rule := newRule(t, db)
	other := newRule(t, db)
	value := 1500.0

	tv := newAlert(t, db, AlertRecord{
		RuleID: rule.ID, RuleName: rule.Name, Subject: "3c:22:fb:12:34:56", Severity: "warning", Value: &value,
		Title: "Large download on TV", Body: "1.5 kB", Labels: map[string]string{"mac": "3c:22:fb:12:34:56"},
		StartedAt: ruleTime,
	})
	nas := newAlert(t, db, AlertRecord{RuleID: rule.ID, RuleName: rule.Name, Subject: "00:11:32:9b:c4:01", Severity: "warning", StartedAt: ruleTime.Add(time.Minute)})
	newAlert(t, db, AlertRecord{RuleID: other.ID, RuleName: other.Name, Subject: "3c:22:fb:12:34:56", Severity: "warning", StartedAt: ruleTime.Add(2 * time.Minute)})

	firing, err := FiringAlerts(db, rule.ID)
	if err != nil || len(firing) != 2 {
		t.Fatalf("firing = %+v, %v; want the rule's two", firing, err)
	}
	if got := firing[tv.Subject]; got.Title != tv.Title || got.Labels["mac"] != tv.Subject || got.Value == nil || *got.Value != value {
		t.Errorf("firing TV alert = %+v", got)
	}

	// Resolve the NAS alert.
	resolvedAt := ruleTime.Add(time.Hour)
	nas.State, nas.ResolvedAt, nas.LastEvaluatedAt = AlertResolved, &resolvedAt, resolvedAt
	nas.Title = "Resolved"
	if err := UpdateAlert(db, nas); err != nil {
		t.Fatal(err)
	}
	if firing, _ = FiringAlerts(db, rule.ID); len(firing) != 1 {
		t.Errorf("firing after resolving = %+v, want the TV's", firing)
	}
	got, err := GetAlert(db, nas.ID)
	if err != nil || got.State != AlertResolved || got.ResolvedAt == nil || !got.ResolvedAt.Equal(resolvedAt) || got.Title != "Resolved" {
		t.Errorf("resolved alert = %+v, %v", got, err)
	}

	subjects, err := AlertedSubjects(db, rule.ID, ruleTime.Add(30*time.Second))
	if err != nil || len(subjects) != 1 || !subjects[nas.Subject] {
		t.Errorf("subjects alerted since 18:00:30 = %v, %v; want the NAS", subjects, err)
	}

	// Most recent first, filtered and paged.
	alerts, total, err := ListAlerts(db, AlertsFilter{RuleID: rule.ID})
	if err != nil || total != 2 || len(alerts) != 2 || alerts[0].ID != nas.ID || alerts[1].ID != tv.ID {
		t.Errorf("rule's alerts = %+v (%d), %v", alerts, total, err)
	}
	alerts, total, _ = ListAlerts(db, AlertsFilter{State: AlertFiring, Limit: 1})
	if total != 2 || len(alerts) != 1 {
		t.Errorf("first firing alert = %+v of %d, want 1 of 2", alerts, total)
	}
	alerts, _, _ = ListAlerts(db, AlertsFilter{Limit: 2, Offset: 2})
	if len(alerts) != 1 || alerts[0].ID != tv.ID {
		t.Errorf("third alert = %+v, want the TV's", alerts)
	}

	// Deliveries, by alert.
	failure := "connection refused"
	for _, d := range []DeliveryRecord{
		{AlertID: tv.ID, ChannelID: 1, Kind: "firing", Status: "sent", Attempts: 1, CreatedAt: ruleTime},
		{AlertID: tv.ID, ChannelID: 2, Kind: "firing", Status: "failed", Attempts: 3, Error: &failure, CreatedAt: ruleTime.Add(time.Second)},
		{AlertID: nas.ID, ChannelID: 1, Kind: "resolved", Status: "sent", Attempts: 1, CreatedAt: resolvedAt},
	} {
		if err := InsertDelivery(db, d); err != nil {
			t.Fatal(err)
		}
	}
	deliveries, err := Deliveries(db, []int64{tv.ID, nas.ID})
	if err != nil || len(deliveries[tv.ID]) != 2 || len(deliveries[nas.ID]) != 1 ||
		deliveries[tv.ID][1].Error == nil || *deliveries[tv.ID][1].Error != failure {
		t.Errorf("deliveries = %+v, %v", deliveries, err)
	}
}

func TestExpireAlerts(t *testing.T) {
	db := openTestDB(t)
	rule := newRule(t, db)
	old := ruleTime.Add(-40 * 24 * time.Hour)
	oldResolved := old.Add(time.Hour)

	expiring := newAlert(t, db, AlertRecord{RuleID: rule.ID, RuleName: rule.Name, Subject: "a", Severity: "info",
		State: AlertResolved, StartedAt: old, ResolvedAt: &oldResolved})
	stillFiring := newAlert(t, db, AlertRecord{RuleID: rule.ID, RuleName: rule.Name, Subject: "b", Severity: "info", StartedAt: old})
	recent := newAlert(t, db, AlertRecord{RuleID: rule.ID, RuleName: rule.Name, Subject: "c", Severity: "info",
		State: AlertResolved, StartedAt: ruleTime, ResolvedAt: &ruleTime})
	for _, d := range []DeliveryRecord{
		{AlertID: expiring.ID, ChannelID: 1, Kind: "firing", Status: "sent", Attempts: 1, CreatedAt: old},
		{AlertID: stillFiring.ID, ChannelID: 1, Kind: "firing", Status: "sent", Attempts: 1, CreatedAt: old},
		{AlertID: recent.ID, ChannelID: 1, Kind: "firing", Status: "sent", Attempts: 1, CreatedAt: ruleTime},
	} {
		if err := InsertDelivery(db, d); err != nil {
			t.Fatal(err)
		}
	}

	if n, err := ExpireAlerts(db, ruleTime, 0); err != nil || n != 0 {
		t.Errorf("ExpireAlerts without retention = %d, %v; want nothing deleted", n, err)
	}
	n, err := ExpireAlerts(db, ruleTime, 30*24*time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("ExpireAlerts = %d, %v; want 1 alert deleted", n, err)
	}

	alerts, _, _ := ListAlerts(db, AlertsFilter{})
	if len(alerts) != 2 || alerts[0].ID != recent.ID || alerts[1].ID != stillFiring.ID {
		t.Errorf("alerts left = %+v, want the recent and the firing one", alerts)
	}
	if count(t, db, "notification_deliveries") != 1 {
		t.Errorf("%d deliveries left, want the recent one", count(t, db, "notification_deliveries"))
	}
}
