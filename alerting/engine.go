package alerting

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/duxet/netmon/apps"
	"github.com/duxet/netmon/lookup"
	"github.com/duxet/netmon/notify"
	"github.com/duxet/netmon/storage"
	"github.com/duxet/netmon/vendors"
)

// Dispatcher sends the notifications of an evaluation; notify.Dispatcher
// is one.
type Dispatcher interface {
	Dispatch([]notify.Notification)
}

// CountryLookup tells the country of an address; lookup.Countries is one.
type CountryLookup interface {
	// Available reports whether lookups can succeed at all.
	Available() bool
	Lookup(addr netip.Addr) (code, name string, ok bool)
}

// ASNLookup tells the autonomous system of an address; lookup.ASNs is one.
type ASNLookup interface {
	Available() bool
	Lookup(addr netip.Addr) (number uint, organization string, ok bool)
}

// Options configures an Engine.
type Options struct {
	// Hostnames names devices in messages; without it, they have none.
	Hostnames *lookup.Hostnames
	// Countries tells destination_country rules where traffic goes;
	// without it, those rules find nothing.
	Countries CountryLookup
	// ASNs tells new_destination rules about networks which networks
	// traffic goes to; without it, those rules find nothing.
	ASNs ASNLookup
	// Classifier recognizes apps for rules limited to one; default
	// apps.Default().
	Classifier *apps.Classifier
	// PublicURL is where people reach netmon, e.g. http://router.lan:2137,
	// for links in messages; empty leaves them out.
	PublicURL string
	// Now tells the time; tests replace it. Default time.Now.
	Now func() time.Time
}

const (
	// deviceSyncWindow is how far back each evaluation looks for devices.
	// It overlaps earlier rounds, so a failed sync is caught up on.
	deviceSyncWindow = 5 * time.Minute
	// newDeviceLookback bounds how long after it was first seen a device
	// still counts as new, e.g. for a rule that was turned off for a while.
	newDeviceLookback = 24 * time.Hour
	// eventLookback bounds how far back an event rule looks for traffic,
	// e.g. after netmon was stopped for a while. Per-minute rows always
	// cover it.
	eventLookback = time.Hour
	// eventOverlap is how far each evaluation of an event rule looks back
	// before the previous one, so that traffic written while it ran isn't
	// missed. The cooldown keeps it from alerting twice.
	eventOverlap = 2 * time.Minute
	// hostnameTimeout bounds the reverse DNS lookups for messages.
	hostnameTimeout = 5 * time.Second
)

// ErrNoGeoIP is returned for rules that need a GeoIP database when there is
// none.
var ErrNoGeoIP = errors.New("countries need GeoLite2-Country.mmdb next to the netmon binary")

// ErrNoASNs is returned for rules about networks when there is no ASN
// database.
var ErrNoASNs = errors.New("networks need GeoLite2-ASN.mmdb next to the netmon binary")

// Engine evaluates the alert rules when triggered, after every collection
// round. Evaluations run one at a time, on Run's goroutine.
type Engine struct {
	db         *sql.DB
	dispatcher Dispatcher
	opts       Options
	// publicURL is opts.PublicURL, which SetPublicURL changes while Run runs.
	publicURL atomic.Pointer[string]
	trigger   chan struct{}
	// started is when the engine started: netmon knows nothing about
	// traffic from before then, when it may not have been running.
	started time.Time

	noGeoIP sync.Once

	// baselines are what each new_destination rule learned devices talk
	// to, by rule ID.
	baselinesMu sync.Mutex
	baselines   map[int64]*baseline
}

// NewEngine returns an engine; Run starts evaluating.
func NewEngine(db *sql.DB, dispatcher Dispatcher, opts Options) *Engine {
	if opts.Classifier == nil {
		opts.Classifier = apps.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	e := &Engine{db: db, dispatcher: dispatcher, opts: opts, trigger: make(chan struct{}, 1), started: opts.Now()}
	e.SetPublicURL(opts.PublicURL)
	return e
}

// SetPublicURL changes where messages link to, as Options.PublicURL.
func (e *Engine) SetPublicURL(url string) {
	e.publicURL.Store(&url)
}

// Trigger asks for an evaluation, e.g. because new flows were written. It
// never blocks: triggers that arrive during an evaluation make one more.
func (e *Engine) Trigger(time.Time) {
	select {
	case e.trigger <- struct{}{}:
	default:
	}
}

// Run evaluates the rules whenever triggered, until ctx is done.
func (e *Engine) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.trigger:
			e.Evaluate(ctx)
		}
	}
}

// Evaluate brings the devices up to date, evaluates every enabled rule, and
// dispatches the notifications. A rule that fails is logged and skipped.
func (e *Engine) Evaluate(ctx context.Context) {
	now := e.opts.Now()

	if err := storage.SyncDevices(e.db, now.Add(-deviceSyncWindow)); err != nil {
		slog.Error("Failed to update devices", "err", err)
	}

	rules, err := storage.ListRules(e.db)
	if err != nil {
		slog.Error("Failed to load alert rules", "err", err)
		return
	}
	rules = slices.DeleteFunc(rules, func(r storage.RuleRecord) bool { return !r.Enabled })
	if len(rules) == 0 {
		return
	}

	devices, err := e.devices()
	if err != nil {
		slog.Error("Failed to load devices", "err", err)
		return
	}

	var notifications []notify.Notification
	for _, rule := range rules {
		ns, err := e.evaluateRule(ctx, rule, devices, now)
		switch {
		case errors.Is(err, ErrNoGeoIP), errors.Is(err, ErrNoASNs):
			e.noGeoIP.Do(func() { slog.Warn("Alert rule can't run", "rule", rule.ID, "type", rule.Type, "err", err) })
			continue
		case err != nil:
			slog.Error("Alert rule failed", "rule", rule.ID, "type", rule.Type, "err", err)
			continue
		}
		notifications = append(notifications, hold(rule, now, ns)...)

		if err := storage.MarkRuleEvaluated(e.db, rule.ID, now); err != nil {
			slog.Error("Failed to record that an alert rule ran", "rule", rule.ID, "err", err)
		}
	}

	e.dispatcher.Dispatch(notifications)
}

func (e *Engine) evaluateRule(ctx context.Context, rule storage.RuleRecord, devices map[string]device, now time.Time) (ns []notify.Notification, err error) {
	// One broken rule must not stop the others.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()

	if IsEvent(rule.Type) {
		since, cooldown := e.eventWindow(rule, now)
		found, err := e.observe(ctx, rule, devices, now, since, nil)
		if err != nil {
			return nil, err
		}
		alerted, err := storage.AlertedSubjects(e.db, rule.ID, cooldown)
		if err != nil {
			return nil, err
		}
		found = slices.DeleteFunc(found, func(c candidate) bool { return alerted[c.subject] })
		return e.raise(rule, now, found)
	}

	open, err := storage.FiringAlerts(e.db, rule.ID)
	if err != nil {
		return nil, err
	}
	found, err := e.observe(ctx, rule, devices, now, time.Time{}, open)
	if err != nil {
		return nil, err
	}
	found = slices.DeleteFunc(found, func(c candidate) bool { return !c.fires })

	switch rule.Type {
	case TrafficThreshold:
		p, err := parseParams[TrafficParams](rule.Params)
		if err != nil {
			return nil, err
		}
		return e.reconcile(rule, now, open, found, func(alert storage.AlertRecord, lasted time.Duration) (string, string) {
			return trafficResolvedText(p, deviceFromLabels(alert.Labels), lasted)
		})
	case DeviceInactive:
		return e.reconcile(rule, now, open, found, func(alert storage.AlertRecord, lasted time.Duration) (string, string) {
			// The device was quiet since before the alert started: for as
			// long as last measured.
			if alert.Value != nil {
				lasted = time.Duration(*alert.Value) * time.Second
			}
			return inactiveResolvedText(deviceFromLabels(alert.Labels), lasted)
		})
	default:
		return nil, fmt.Errorf("unknown rule type %q", rule.Type)
	}
}

// hold marks the notifications of a rule that is snoozed, or in its quiet
// hours, to be recorded rather than sent.
func hold(rule storage.RuleRecord, now time.Time, ns []notify.Notification) []notify.Notification {
	var reason string
	switch {
	case rule.SnoozedUntil != nil && now.Before(*rule.SnoozedUntil):
		reason = "The rule is snoozed until " + rule.SnoozedUntil.Local().Format("Jan 2 15:04") + "."
	case rule.QuietFrom != "" && inQuietHours(rule.QuietFrom, rule.QuietTo, now.Local()):
		reason = "The rule is quiet from " + rule.QuietFrom + " to " + rule.QuietTo + "."
	default:
		return ns
	}
	for i := range ns {
		ns[i].Hold = reason
	}
	return ns
}

// eventWindow returns since when an event rule looks for events, and since
// when what it alerted about doesn't count again.
func (e *Engine) eventWindow(rule storage.RuleRecord, now time.Time) (since, cooldown time.Time) {
	if rule.Type == NewDevice {
		// Devices seen before the rule existed aren't new to it.
		since = now.Add(-newDeviceLookback)
		if rule.CreatedAt.After(since) {
			since = rule.CreatedAt
		}
		return since, since
	}

	since = rule.CreatedAt
	if rule.LastEvaluatedAt != nil && rule.LastEvaluatedAt.Add(-eventOverlap).After(since) {
		since = rule.LastEvaluatedAt.Add(-eventOverlap)
	}
	if earliest := now.Add(-eventLookback); since.Before(earliest) {
		since = earliest
	}
	// A device can only come back once it has been quiet that long again.
	if rule.Type == DeviceOnline {
		if p, err := parseParams[OnlineParams](rule.Params); err == nil {
			return since, now.Add(-time.Duration(p.InactiveSeconds) * time.Second)
		}
	}
	return since, now.Add(-EventCooldown)
}

// devices returns what is known about every device, by MAC address.
func (e *Engine) devices() (map[string]device, error) {
	records, err := storage.GetDevices(e.db)
	if err != nil {
		return nil, err
	}

	devices := make(map[string]device, len(records))
	for _, r := range records {
		mac := r.MACAddress.String()
		d := device{
			mac: mac, ip: r.IPAddress.String(), vendor: vendors.Lookup(r.MACAddress.HardwareAddr),
			firstSeen: r.FirstSeen, lastSeen: r.LastSeen,
		}
		if r.Name != nil {
			d.name = *r.Name
		}
		devices[mac] = d
	}
	return devices, nil
}

// withHostnames returns the devices with the given MAC addresses, which
// messages are about, with their hostnames.
func (e *Engine) withHostnames(ctx context.Context, devices map[string]device, macs []string) map[string]device {
	result := make(map[string]device, len(macs))
	var addrs []netip.Addr
	for _, mac := range macs {
		d, ok := devices[mac]
		if !ok {
			d = device{mac: mac}
		}
		result[mac] = d
		if addr, err := netip.ParseAddr(d.ip); err == nil {
			addrs = append(addrs, addr)
		}
	}
	if e.opts.Hostnames == nil || len(addrs) == 0 {
		return result
	}

	ctx, cancel := context.WithTimeout(ctx, hostnameTimeout)
	defer cancel()
	names := e.opts.Hostnames.LookupAll(ctx, addrs)
	for mac, d := range result {
		if addr, err := netip.ParseAddr(d.ip); err == nil && names[addr.Unmap()] != nil {
			d.hostname = *names[addr.Unmap()]
			result[mac] = d
		}
	}
	return result
}

// reconcile brings a stateful rule's alerts in line with the conditions
// that hold now: new ones fire, lasting ones are repeated when the rule
// says so, and the others resolve.
func (e *Engine) reconcile(
	rule storage.RuleRecord, now time.Time, open map[string]storage.AlertRecord, holding []candidate,
	resolvedText func(alert storage.AlertRecord, lasted time.Duration) (title, body string),
) ([]notify.Notification, error) {
	var ns []notify.Notification
	held := map[string]bool{}

	for _, c := range holding {
		held[c.subject] = true
		alert, isOpen := open[c.subject]
		if !isOpen {
			alert = storage.AlertRecord{
				RuleID: rule.ID, RuleName: rule.Name, Subject: c.subject, State: storage.AlertFiring, Severity: rule.Severity,
				Value: c.value, Title: c.title, Body: c.body, Labels: c.labels,
				StartedAt: now, LastEvaluatedAt: now, LastNotifiedAt: &now,
			}
			id, err := storage.InsertAlert(e.db, alert)
			if err != nil {
				return ns, err
			}
			alert.ID = id
			ns = append(ns, e.notification(rule, alert, "firing", c.title, c.body))
			continue
		}

		alert.Value, alert.Title, alert.Body, alert.Labels, alert.LastEvaluatedAt = c.value, c.title, c.body, c.labels, now
		// An acknowledged alert isn't sent again while it lasts.
		if repeat := rule.RepeatIntervalSeconds; repeat != nil && alert.AcknowledgedAt == nil &&
			(alert.LastNotifiedAt == nil || now.Sub(*alert.LastNotifiedAt) >= time.Duration(*repeat)*time.Second) {
			alert.LastNotifiedAt = &now
			title, body := c.reminder(alert.StartedAt)
			ns = append(ns, e.notification(rule, alert, "reminder", title, body))
		}
		if err := storage.UpdateAlert(e.db, alert); err != nil {
			return ns, err
		}
	}

	resolved := make([]string, 0, len(open))
	for subject := range open {
		if !held[subject] {
			resolved = append(resolved, subject)
		}
	}
	slices.Sort(resolved)
	for _, subject := range resolved {
		alert := open[subject]
		alert.State, alert.ResolvedAt, alert.LastEvaluatedAt = storage.AlertResolved, &now, now
		if rule.NotifyOnResolve {
			alert.LastNotifiedAt = &now
			title, body := resolvedText(alert, now.Sub(alert.StartedAt))
			ns = append(ns, e.notification(rule, alert, "resolved", title, body))
		}
		if err := storage.UpdateAlert(e.db, alert); err != nil {
			return ns, err
		}
	}
	return ns, nil
}

// raise records an alert for each event an event rule found, resolved as it
// starts, and notifies about it.
func (e *Engine) raise(rule storage.RuleRecord, now time.Time, events []candidate) ([]notify.Notification, error) {
	var ns []notify.Notification
	for _, c := range events {
		alert := storage.AlertRecord{
			RuleID: rule.ID, RuleName: rule.Name, Subject: c.subject, State: storage.AlertResolved, Severity: rule.Severity,
			Value: c.value, Title: c.title, Body: c.body, Labels: c.labels,
			StartedAt: now, LastEvaluatedAt: now, LastNotifiedAt: &now, ResolvedAt: &now,
		}
		id, err := storage.InsertAlert(e.db, alert)
		if err != nil {
			return ns, err
		}
		alert.ID = id
		ns = append(ns, e.notification(rule, alert, "firing", c.title, c.body))
	}
	return ns, nil
}

// link is where a message about an alert points: its page in netmon.
func (e *Engine) link(alert storage.AlertRecord) string {
	base := *e.publicURL.Load()
	if base == "" {
		return ""
	}
	return base + "/alerts/" + strconv.FormatInt(alert.ID, 10)
}

// notification is a message about an alert for the rule's channels. Its
// fields, for webhooks, describe the alert and its subject.
func (e *Engine) notification(rule storage.RuleRecord, alert storage.AlertRecord, kind, title, body string) notify.Notification {
	link := e.link(alert)
	if link != "" {
		body += "\n\n" + link
	}

	fields := map[string]string{
		"payload_version": "1",
		"event":           "alert." + kind,
		"alert_id":        strconv.FormatInt(alert.ID, 10),
		"rule_id":         strconv.FormatInt(rule.ID, 10),
		"rule_name":       rule.Name,
		"rule_type":       rule.Type,
		"severity":        rule.Severity,
		"subject":         alert.Subject,
		"state":           alert.State,
		"started_at":      alert.StartedAt.UTC().Format(time.RFC3339),
	}
	if alert.Value != nil {
		fields["value"] = strconv.FormatFloat(*alert.Value, 'f', 0, 64)
	}
	if alert.ResolvedAt != nil && !IsEvent(rule.Type) {
		fields["resolved_at"] = alert.ResolvedAt.UTC().Format(time.RFC3339)
	}
	if link != "" {
		fields["url"] = link
	}
	for key, value := range alert.Labels {
		if _, taken := fields[key]; !taken {
			fields[key] = value
		}
	}

	return notify.Notification{
		AlertID: alert.ID,
		Kind:    kind,
		Message: notify.Message{
			Title:    title,
			Body:     body,
			Severity: notify.Severity(rule.Severity),
			Fields:   fields,
		},
		ChannelIDs: rule.ChannelIDs,
	}
}
