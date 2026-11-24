package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/duxet/netmon/storage"
	"github.com/gofiber/fiber/v3"
)

type ruleResponse struct {
	ID                    int64
	Name, Type, Severity  string
	Params                map[string]any
	RepeatIntervalSeconds *int64
	NotifyOnResolve       bool
	Enabled               bool
	ChannelIDs            []int64
	FiringAlerts          int
}

func addTestChannel(t *testing.T, app *fiber.App) channelResponse {
	t.Helper()
	var channel channelResponse
	doJSON(t, app, "POST", "/api/notification-channels", `{"name": "Phone", "url": "ntfy://ntfy.sh/phone"}`, fiber.StatusCreated, &channel)
	return channel
}

func fireAlert(t *testing.T, db *sql.DB, ruleID int64, subject string) storage.AlertRecord {
	t.Helper()
	now := time.Now()
	a := storage.AlertRecord{RuleID: ruleID, RuleName: "rule", Subject: subject, State: storage.AlertFiring, Severity: "warning",
		Title: "Large download on " + subject, Body: "…", Labels: map[string]string{"mac": subject}, StartedAt: now, LastEvaluatedAt: now}
	id, err := storage.InsertAlert(db, a)
	if err != nil {
		t.Fatal(err)
	}
	a.ID = id
	return a
}

func TestAlertRuleOptions(t *testing.T) {
	app, _ := newTestApp(t)
	var options AlertRuleOptionsDTO
	doJSON(t, app, "GET", "/api/alert-rule-options", "", fiber.StatusOK, &options)
	if len(options.Types) != 2 || options.Apps[len(options.Apps)-1] != "Other" || options.MinWindowSeconds != 300 {
		t.Errorf("options = %+v", options)
	}
}

func TestAlertRules(t *testing.T) {
	app, db := newTestApp(t)
	channel := addTestChannel(t, app)

	body := fmt.Sprintf(`{
		"name": " Big downloads ", "type": "traffic_threshold",
		"params": {"direction": "download", "threshold_bytes": 5000000000, "window_s": 3600, "macs": ["3C-22-FB-12-34-56"]},
		"repeat_interval_s": 3600, "channel_ids": [%d]
	}`, channel.ID)
	var rule ruleResponse
	doJSON(t, app, "POST", "/api/alert-rules", body, fiber.StatusCreated, &rule)
	if rule.ID == 0 || rule.Name != "Big downloads" || rule.Severity != "warning" || !rule.Enabled || !rule.NotifyOnResolve ||
		len(rule.ChannelIDs) != 1 || rule.ChannelIDs[0] != channel.ID || *rule.RepeatIntervalSeconds != 3600 {
		t.Errorf("created = %+v", rule)
	}
	// Params come back normalized.
	if macs := rule.Params["macs"].([]any); len(macs) != 1 || macs[0] != "3c:22:fb:12:34:56" {
		t.Errorf("params = %v, want the MAC in canonical form", rule.Params)
	}
	path := fmt.Sprintf("/api/alert-rules/%d", rule.ID)

	var used channelResponseWithRules
	doJSON(t, app, "GET", fmt.Sprintf("/api/notification-channels/%d", channel.ID), "", fiber.StatusOK, &used)
	if used.UsedByRules != 1 {
		t.Errorf("channel used by %d rules, want 1", used.UsedByRules)
	}

	// Alerts about events can't repeat or resolve.
	var newDevices ruleResponse
	doJSON(t, app, "POST", "/api/alert-rules",
		`{"name": "New devices", "type": "new_device", "severity": "info", "repeat_interval_s": 3600, "notify_on_resolve": true}`,
		fiber.StatusCreated, &newDevices)
	if newDevices.RepeatIntervalSeconds != nil || newDevices.NotifyOnResolve || newDevices.Params["ignore_random_macs"] != false {
		t.Errorf("new device rule = %+v", newDevices)
	}

	var rules []ruleResponse
	doJSON(t, app, "GET", "/api/alert-rules", "", fiber.StatusOK, &rules)
	if len(rules) != 2 || rules[0].ID != rule.ID {
		t.Errorf("rules = %+v", rules)
	}

	// Renaming keeps a firing alert; changing the threshold resolves it.
	fireAlert(t, db, rule.ID, "3c:22:fb:12:34:56")
	renamed := strings.Replace(body, " Big downloads ", "Huge downloads", 1)
	doJSON(t, app, "PUT", path, renamed, fiber.StatusOK, &rule)
	if rule.Name != "Huge downloads" || rule.FiringAlerts != 1 {
		t.Errorf("renamed = %+v, want the alert still firing", rule)
	}
	doJSON(t, app, "PUT", path, strings.Replace(renamed, "5000000000", "6000000000", 1), fiber.StatusOK, &rule)
	if rule.FiringAlerts != 0 || rule.Params["threshold_bytes"] != 6e9 {
		t.Errorf("new threshold = %+v, want the alert resolved", rule)
	}

	// Turning it off resolves alerts too.
	fireAlert(t, db, rule.ID, "3c:22:fb:12:34:56")
	doJSON(t, app, "PATCH", path, `{"enabled": false}`, fiber.StatusOK, &rule)
	if rule.Enabled || rule.FiringAlerts != 0 {
		t.Errorf("turned off = %+v", rule)
	}
	doJSON(t, app, "PATCH", path, `{"enabled": true}`, fiber.StatusOK, &rule)
	if !rule.Enabled || rule.Name != "Huge downloads" {
		t.Errorf("turned on = %+v", rule)
	}

	// Deleting the channel takes it off the rule.
	doJSON(t, app, "DELETE", fmt.Sprintf("/api/notification-channels/%d", channel.ID), "", fiber.StatusNoContent, nil)
	doJSON(t, app, "GET", path, "", fiber.StatusOK, &rule)
	if len(rule.ChannelIDs) != 0 {
		t.Errorf("channels after deleting the channel = %v", rule.ChannelIDs)
	}

	doJSON(t, app, "DELETE", path, "", fiber.StatusNoContent, nil)
	doJSON(t, app, "GET", path, "", fiber.StatusNotFound, nil)
	doJSON(t, app, "PUT", path, body, fiber.StatusNotFound, nil)
	doJSON(t, app, "PATCH", path, `{"enabled": true}`, fiber.StatusNotFound, nil)
	doJSON(t, app, "DELETE", path, "", fiber.StatusNotFound, nil)
}

type channelResponseWithRules struct {
	UsedByRules int
}

func TestAlertRuleValidation(t *testing.T) {
	app, _ := newTestApp(t)
	traffic := func(params string) string {
		return `{"name": "x", "type": "traffic_threshold", "params": ` + params + `}`
	}
	valid := `{"direction": "download", "threshold_bytes": 1000, "window_s": 3600}`

	var rule ruleResponse
	doJSON(t, app, "POST", "/api/alert-rules", traffic(valid), fiber.StatusCreated, &rule)
	path := fmt.Sprintf("/api/alert-rules/%d", rule.ID)

	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/api/alert-rules", `{"name": "x", "type": "nope"}`},
		{"POST", "/api/alert-rules", `{"name": "", "type": "new_device"}`},
		{"POST", "/api/alert-rules", `{"name": "x", "type": "new_device", "params": {"nope": true}}`},
		{"POST", "/api/alert-rules", traffic(`{}`)},
		{"POST", "/api/alert-rules", traffic(`{"direction": "sideways", "threshold_bytes": 1000, "window_s": 3600}`)},
		{"POST", "/api/alert-rules", traffic(`{"direction": "download", "threshold_bytes": 0, "window_s": 3600}`)},
		{"POST", "/api/alert-rules", traffic(`{"direction": "download", "threshold_bytes": 1000, "window_s": 60}`)},
		{"POST", "/api/alert-rules", traffic(`{"direction": "download", "threshold_bytes": 1000, "window_s": 3600, "app": "Nope"}`)},
		{"POST", "/api/alert-rules", traffic(`{"direction": "download", "threshold_bytes": 1000, "window_s": 3600, "macs": ["nope"]}`)},
		{"POST", "/api/alert-rules", `{"name": "x", "type": "new_device", "severity": "meh"}`},
		{"POST", "/api/alert-rules", `{"name": "x", "type": "traffic_threshold", "params": ` + valid + `, "repeat_interval_s": 60}`},
		{"POST", "/api/alert-rules", `{"name": "x", "type": "new_device", "channel_ids": [42]}`},
		{"POST", "/api/alert-rules", `{"name": `},
		{"PUT", path, traffic(`{"direction": "download"}`)},
		{"PATCH", path, `{}`},
		{"GET", "/api/alert-rules/abc", ""},
		{"GET", "/api/alerts?state=open", ""},
		{"GET", "/api/alerts?limit=0", ""},
		{"GET", "/api/alerts/abc", ""},
	} {
		body := doJSON(t, app, tc.method, tc.path, tc.body, fiber.StatusBadRequest, nil)
		if msg := errorMessage(t, body); msg == "" {
			t.Errorf("%s %s %s: empty error message", tc.method, tc.path, tc.body)
		}
	}
}

func TestAlerts(t *testing.T) {
	app, db := newTestApp(t)
	var rule, other ruleResponse
	doJSON(t, app, "POST", "/api/alert-rules", `{"name": "New devices", "type": "new_device"}`, fiber.StatusCreated, &rule)
	doJSON(t, app, "POST", "/api/alert-rules", `{"name": "Other", "type": "new_device"}`, fiber.StatusCreated, &other)

	first := fireAlert(t, db, rule.ID, "aa:aa:aa:aa:aa:aa")
	second := fireAlert(t, db, rule.ID, "bb:bb:bb:bb:bb:bb")
	fireAlert(t, db, other.ID, "cc:cc:cc:cc:cc:cc")
	failure := "connection refused"
	for _, d := range []storage.DeliveryRecord{
		{AlertID: first.ID, ChannelID: 1, Kind: "firing", Status: "sent", Attempts: 1, CreatedAt: time.Now()},
		{AlertID: first.ID, ChannelID: 2, Kind: "firing", Status: "failed", Attempts: 3, Error: &failure, CreatedAt: time.Now()},
	} {
		if err := storage.InsertDelivery(db, d); err != nil {
			t.Fatal(err)
		}
	}

	var alerts []AlertDTO
	status, total := get(t, app, fmt.Sprintf("/api/alerts?rule_id=%d&limit=1", rule.ID), &alerts)
	if status != fiber.StatusOK || total != "2" || len(alerts) != 1 || alerts[0].ID != second.ID || len(alerts[0].Deliveries) != 0 {
		t.Errorf("first page = %d %s %+v, want the newest of 2", status, total, alerts)
	}
	get(t, app, fmt.Sprintf("/api/alerts?rule_id=%d&limit=1&offset=1", rule.ID), &alerts)
	if len(alerts) != 1 || alerts[0].ID != first.ID || len(alerts[0].Deliveries) != 2 ||
		alerts[0].Deliveries[1].Status != "failed" || *alerts[0].Deliveries[1].Error != failure ||
		alerts[0].Labels["mac"] != "aa:aa:aa:aa:aa:aa" {
		t.Errorf("second page = %+v, want the first alert with its deliveries", alerts)
	}

	if _, total := get(t, app, "/api/alerts?state=firing", &alerts); total != "3" {
		t.Errorf("firing alerts = %s, want 3", total)
	}
	if _, total := get(t, app, "/api/alerts?state=resolved", &alerts); total != "0" || len(alerts) != 0 {
		t.Errorf("resolved alerts = %s %+v, want none", total, alerts)
	}

	var alert AlertDTO
	doJSON(t, app, "GET", fmt.Sprintf("/api/alerts/%d", first.ID), "", fiber.StatusOK, &alert)
	if alert.ID != first.ID || alert.RuleID != rule.ID || len(alert.Deliveries) != 2 {
		t.Errorf("alert = %+v", alert)
	}
	doJSON(t, app, "GET", "/api/alerts/999", "", fiber.StatusNotFound, nil)

	// Deleting a rule keeps its alerts, resolved.
	doJSON(t, app, "DELETE", fmt.Sprintf("/api/alert-rules/%d", rule.ID), "", fiber.StatusNoContent, nil)
	var body json.RawMessage
	if _, total := get(t, app, "/api/alerts?state=resolved", &body); total != "2" {
		t.Errorf("resolved alerts after deleting the rule = %s, want 2", total)
	}
}
