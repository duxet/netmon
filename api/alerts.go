package api

import (
	"cmp"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/duxet/netmon/alerting"
	"github.com/duxet/netmon/apps"
	"github.com/duxet/netmon/auth"
	"github.com/duxet/netmon/lookup"
	"github.com/duxet/netmon/storage"
	"github.com/gofiber/fiber/v3"
)

const (
	defaultAlertsLimit = 50
	maxAlertsLimit     = 500

	// maxSnooze is how long a rule can be snoozed for at most.
	maxSnooze = 30 * 24 * time.Hour
)

// ruleBody is the request body for creating or replacing an alert rule.
type ruleBody struct {
	Name     string          `json:"name"`
	Type     string          `json:"type"`
	Params   json.RawMessage `json:"params"`
	Severity string          `json:"severity"`
	// RepeatIntervalSeconds is how often a firing alert is sent again;
	// null sends it once.
	RepeatIntervalSeconds *int64 `json:"repeat_interval_s"`
	// NotifyOnResolve and Enabled default to true.
	NotifyOnResolve *bool   `json:"notify_on_resolve"`
	Enabled         *bool   `json:"enabled"`
	ChannelIDs      []int64 `json:"channel_ids"`
	// QuietFrom and QuietTo, such as "22:00" and "07:00", hold back the
	// rule's notifications every day between them; both empty for none.
	QuietFrom string `json:"quiet_from,omitempty"`
	QuietTo   string `json:"quiet_to,omitempty"`
}

func parseRuleID(c fiber.Ctx) (int64, error) {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, badRequest("invalid rule id: " + c.Params("id"))
	}
	return id, nil
}

// ruleNotFound turns storage.ErrNotFound into a 404.
func ruleNotFound(err error) error {
	if errors.Is(err, storage.ErrNotFound) {
		return fiber.NewError(fiber.StatusNotFound, "alert rule not found")
	}
	return err
}

// parseRule checks a rule body and turns it into a record.
func parseRule(db *sql.DB, body *ruleBody) (storage.RuleRecord, error) {
	name, err := parseName(body.Name, "name must not be empty")
	if err != nil {
		return storage.RuleRecord{}, err
	}

	params, err := alerting.ValidateParams(body.Type, body.Params)
	if err != nil {
		return storage.RuleRecord{}, badRequest(err.Error())
	}

	severity := body.Severity
	if severity == "" {
		severity = "warning"
	}
	if err := alerting.ValidateSeverity(severity); err != nil {
		return storage.RuleRecord{}, badRequest(err.Error())
	}
	if err := alerting.ValidateRepeat(body.RepeatIntervalSeconds); err != nil {
		return storage.RuleRecord{}, badRequest(err.Error())
	}
	if err := alerting.ValidateQuietHours(body.QuietFrom, body.QuietTo); err != nil {
		return storage.RuleRecord{}, badRequest(err.Error())
	}

	channels, err := storage.ListChannels(db)
	if err != nil {
		return storage.RuleRecord{}, err
	}
	for _, id := range body.ChannelIDs {
		if !slices.ContainsFunc(channels, func(c storage.ChannelRecord) bool { return c.ID == id }) {
			return storage.RuleRecord{}, badRequest(fmt.Sprintf("unknown notification channel %d", id))
		}
	}

	rule := storage.RuleRecord{
		Name:                  name,
		Type:                  body.Type,
		Params:                params,
		Severity:              severity,
		RepeatIntervalSeconds: body.RepeatIntervalSeconds,
		NotifyOnResolve:       body.NotifyOnResolve == nil || *body.NotifyOnResolve,
		Enabled:               body.Enabled == nil || *body.Enabled,
		ChannelIDs:            body.ChannelIDs,
		QuietFrom:             body.QuietFrom,
		QuietTo:               body.QuietTo,
	}
	// Alerts about events are over at once: there is nothing to repeat or
	// resolve.
	if alerting.IsEvent(rule.Type) {
		rule.RepeatIntervalSeconds = nil
		rule.NotifyOnResolve = false
	}
	return rule, nil
}

func toRuleDTO(r storage.RuleRecord) AlertRuleDTO {
	channels := r.ChannelIDs
	if channels == nil {
		channels = []int64{}
	}
	return AlertRuleDTO{
		ID:                    r.ID,
		Name:                  r.Name,
		Type:                  r.Type,
		Params:                json.RawMessage(r.Params),
		Severity:              r.Severity,
		RepeatIntervalSeconds: r.RepeatIntervalSeconds,
		NotifyOnResolve:       r.NotifyOnResolve,
		Enabled:               r.Enabled,
		ChannelIDs:            channels,
		FiringAlerts:          r.FiringAlerts,
		LastEvaluatedAt:       r.LastEvaluatedAt,
		CreatedAt:             r.CreatedAt,
		UpdatedAt:             r.UpdatedAt,
		SnoozedUntil:          r.SnoozedUntil,
		QuietFrom:             nonEmpty(r.QuietFrom),
		QuietTo:               nonEmpty(r.QuietTo),
	}
}

func toAlertDTO(a storage.AlertRecord, deliveries []storage.DeliveryRecord) AlertDTO {
	dto := AlertDTO{
		ID:              a.ID,
		RuleID:          a.RuleID,
		RuleName:        a.RuleName,
		Subject:         a.Subject,
		State:           a.State,
		Severity:        a.Severity,
		Value:           a.Value,
		Title:           a.Title,
		Body:            a.Body,
		Labels:          a.Labels,
		StartedAt:       a.StartedAt,
		LastEvaluatedAt: a.LastEvaluatedAt,
		LastNotifiedAt:  a.LastNotifiedAt,
		ResolvedAt:      a.ResolvedAt,
		AcknowledgedAt:  a.AcknowledgedAt,
		AcknowledgedBy:  nonEmpty(a.AcknowledgedBy),
		Deliveries:      []DeliveryDTO{},
	}
	for _, d := range deliveries {
		dto.Deliveries = append(dto.Deliveries, DeliveryDTO{
			ChannelID: d.ChannelID, Kind: d.Kind, Status: d.Status, Attempts: d.Attempts, Error: d.Error, CreatedAt: d.CreatedAt,
		})
	}
	return dto
}

// previewRuleName stands in for the name of a rule previewed before it has
// one, in the messages it would send.
const previewRuleName = "New rule"

// registerAlertRoutes adds the endpoints for alert rules and alerts.
func registerAlertRoutes(app *fiber.App, db *sql.DB, alerts *alerting.Engine, countries *lookup.Countries, asns *lookup.ASNs) {
	// What the rule form offers.
	app.Get("/api/alert-rule-options", func(c fiber.Ctx) error {
		names := make([]string, 0, len(apps.Known)+1)
		for _, a := range apps.Known {
			names = append(names, a.Name)
		}
		names = append(names, apps.Other)

		return c.JSON(AlertRuleOptionsDTO{
			Types:            alerting.Types,
			Apps:             names,
			MinWindowSeconds: alerting.MinWindowSeconds,
			MaxWindowSeconds: alerting.MaxWindowSeconds,
			MinRepeatSeconds: alerting.MinRepeatSeconds,
			GeoIP:            countries.Available(),
			ASNs:             asns.Available(),
		})
	})

	// Holds back a rule's notifications for a while, or until a time.
	app.Post("/api/alert-rules/:id/snooze", func(c fiber.Ctx) error {
		id, err := parseRuleID(c)
		if err != nil {
			return err
		}
		var body struct {
			ForSeconds int64      `json:"for_s"`
			Until      *time.Time `json:"until"`
		}
		if err := c.Bind().Body(&body); err != nil {
			return badRequest("invalid request body: " + err.Error())
		}
		until := time.Now().Add(time.Duration(body.ForSeconds) * time.Second)
		if body.Until != nil {
			until = *body.Until
		}
		if !until.After(time.Now()) || until.After(time.Now().Add(maxSnooze)) {
			return badRequest("snooze for_s seconds, or until a time, in the next 30 days")
		}
		if err := storage.SnoozeRule(db, id, &until); err != nil {
			return ruleNotFound(err)
		}
		slog.Info("Snoozed alert rule", "id", id, "until", until)
		rule, err := storage.GetRule(db, id)
		if err != nil {
			return ruleNotFound(err)
		}
		return c.JSON(toRuleDTO(rule))
	})

	app.Delete("/api/alert-rules/:id/snooze", func(c fiber.Ctx) error {
		id, err := parseRuleID(c)
		if err != nil {
			return err
		}
		if err := storage.SnoozeRule(db, id, nil); err != nil {
			return ruleNotFound(err)
		}
		rule, err := storage.GetRule(db, id)
		if err != nil {
			return ruleNotFound(err)
		}
		return c.JSON(toRuleDTO(rule))
	})

	// Says someone knows of an alert, which isn't sent again while it lasts.
	acknowledge := func(c fiber.Ctx, at time.Time) error {
		id, err := strconv.ParseInt(c.Params("id"), 10, 64)
		if err != nil || id <= 0 {
			return badRequest("invalid alert id: " + c.Params("id"))
		}
		by := ""
		if user := auth.UserFrom(c); user != nil {
			by = cmp.Or(user.Name, user.Email)
		}
		if err := storage.AcknowledgeAlert(db, id, at, by); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				return fiber.NewError(fiber.StatusNotFound, "alert not found")
			}
			return err
		}
		alert, err := storage.GetAlert(db, id)
		if err != nil {
			return err
		}
		deliveries, err := storage.Deliveries(db, []int64{id})
		if err != nil {
			return err
		}
		return c.JSON(toAlertDTO(alert, deliveries[id]))
	}
	app.Post("/api/alerts/:id/acknowledge", func(c fiber.Ctx) error { return acknowledge(c, time.Now()) })
	app.Delete("/api/alerts/:id/acknowledge", func(c fiber.Ctx) error { return acknowledge(c, time.Time{}) })

	// What a rule would find now, without saving it.
	app.Post("/api/alert-rules/preview", func(c fiber.Ctx) error {
		body := new(ruleBody)
		if err := c.Bind().Body(body); err != nil {
			return badRequest("invalid request body: " + err.Error())
		}
		if strings.TrimSpace(body.Name) == "" {
			body.Name = previewRuleName
		}
		rule, err := parseRule(db, body)
		if err != nil {
			return err
		}
		rule.CreatedAt = time.Now()

		preview, err := alerts.Preview(c.Context(), rule)
		if errors.Is(err, alerting.ErrNoGeoIP) || errors.Is(err, alerting.ErrNoASNs) {
			return badRequest(err.Error())
		}
		if err != nil {
			return err
		}

		dto := AlertRulePreviewDTO{Findings: []FindingDTO{}, Total: preview.Total}
		if !preview.Since.IsZero() {
			dto.Since = &preview.Since
		}
		for _, f := range preview.Findings {
			dto.Findings = append(dto.Findings, FindingDTO(f))
		}
		return c.JSON(dto)
	})

	app.Get("/api/alert-rules", func(c fiber.Ctx) error {
		records, err := storage.ListRules(db)
		if err != nil {
			return err
		}
		rules := make([]AlertRuleDTO, 0, len(records))
		for _, r := range records {
			rules = append(rules, toRuleDTO(r))
		}
		return c.JSON(rules)
	})

	app.Post("/api/alert-rules", func(c fiber.Ctx) error {
		body := new(ruleBody)
		if err := c.Bind().Body(body); err != nil {
			return badRequest("invalid request body: " + err.Error())
		}
		rule, err := parseRule(db, body)
		if err != nil {
			return err
		}

		created, err := storage.CreateRule(db, rule, time.Now())
		if err != nil {
			return err
		}
		slog.Info("Added alert rule", "id", created.ID, "type", created.Type)
		return c.Status(fiber.StatusCreated).JSON(toRuleDTO(created))
	})

	app.Get("/api/alert-rules/:id", func(c fiber.Ctx) error {
		id, err := parseRuleID(c)
		if err != nil {
			return err
		}
		rule, err := storage.GetRule(db, id)
		if err != nil {
			return ruleNotFound(err)
		}
		return c.JSON(toRuleDTO(rule))
	})

	// update stores a changed rule. Its firing alerts are resolved, without
	// a notification, when it checks something else now or is turned off:
	// whatever they said may no longer hold.
	update := func(c fiber.Ctx, old, rule storage.RuleRecord) error {
		resolve := rule.Type != old.Type || rule.Params != old.Params || (old.Enabled && !rule.Enabled)
		updated, err := storage.UpdateRule(db, rule, time.Now(), resolve)
		if err != nil {
			return ruleNotFound(err)
		}
		slog.Info("Updated alert rule", "id", updated.ID, "type", updated.Type)
		return c.JSON(toRuleDTO(updated))
	}

	app.Put("/api/alert-rules/:id", func(c fiber.Ctx) error {
		id, err := parseRuleID(c)
		if err != nil {
			return err
		}
		body := new(ruleBody)
		if err := c.Bind().Body(body); err != nil {
			return badRequest("invalid request body: " + err.Error())
		}

		old, err := storage.GetRule(db, id)
		if err != nil {
			return ruleNotFound(err)
		}
		rule, err := parseRule(db, body)
		if err != nil {
			return err
		}
		rule.ID = id
		return update(c, old, rule)
	})

	// Turns a rule on or off.
	app.Patch("/api/alert-rules/:id", func(c fiber.Ctx) error {
		id, err := parseRuleID(c)
		if err != nil {
			return err
		}
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if err := c.Bind().Body(&body); err != nil {
			return badRequest("invalid request body: " + err.Error())
		}
		if body.Enabled == nil {
			return badRequest("enabled is required")
		}

		old, err := storage.GetRule(db, id)
		if err != nil {
			return ruleNotFound(err)
		}
		rule := old
		rule.Enabled = *body.Enabled
		return update(c, old, rule)
	})

	app.Delete("/api/alert-rules/:id", func(c fiber.Ctx) error {
		id, err := parseRuleID(c)
		if err != nil {
			return err
		}
		if err := storage.DeleteRule(db, id, time.Now()); err != nil {
			return ruleNotFound(err)
		}
		slog.Info("Removed alert rule", "id", id)
		return c.SendStatus(fiber.StatusNoContent)
	})

	app.Get("/api/alerts", func(c fiber.Ctx) error {
		filter := storage.AlertsFilter{State: c.Query("state")}
		if filter.State != "" && filter.State != storage.AlertFiring && filter.State != storage.AlertResolved {
			return badRequest("state must be firing or resolved")
		}
		ruleID, err := parseInt(c, "rule_id", 0, 0, math.MaxInt32)
		if err != nil {
			return err
		}
		limit, err := parseInt(c, "limit", defaultAlertsLimit, 1, maxAlertsLimit)
		if err != nil {
			return err
		}
		offset, err := parseInt(c, "offset", 0, 0, math.MaxInt32)
		if err != nil {
			return err
		}
		filter.RuleID, filter.Limit, filter.Offset = int64(ruleID), uint64(limit), uint64(offset)

		records, total, err := storage.ListAlerts(db, filter)
		if err != nil {
			return err
		}
		ids := make([]int64, 0, len(records))
		for _, a := range records {
			ids = append(ids, a.ID)
		}
		deliveries, err := storage.Deliveries(db, ids)
		if err != nil {
			return err
		}

		alerts := make([]AlertDTO, 0, len(records))
		for _, a := range records {
			alerts = append(alerts, toAlertDTO(a, deliveries[a.ID]))
		}
		c.Set(totalCountHeader, strconv.Itoa(total))
		return c.JSON(alerts)
	})

	app.Get("/api/alerts/:id", func(c fiber.Ctx) error {
		id, err := strconv.ParseInt(c.Params("id"), 10, 64)
		if err != nil || id <= 0 {
			return badRequest("invalid alert id: " + c.Params("id"))
		}
		alert, err := storage.GetAlert(db, id)
		if errors.Is(err, storage.ErrNotFound) {
			return fiber.NewError(fiber.StatusNotFound, "alert not found")
		}
		if err != nil {
			return err
		}
		deliveries, err := storage.Deliveries(db, []int64{id})
		if err != nil {
			return err
		}
		return c.JSON(toAlertDTO(alert, deliveries[id]))
	})
}
