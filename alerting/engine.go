package alerting

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"log"
	"maps"
	"net"
	"net/netip"
	"slices"
	"strconv"
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

// Options configures an Engine.
type Options struct {
	// Hostnames names devices in messages; without it, they have none.
	Hostnames *lookup.Hostnames
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
	// hostnameTimeout bounds the reverse DNS lookups for messages.
	hostnameTimeout = 5 * time.Second
)

// Engine evaluates the alert rules when triggered, after every collection
// round. Evaluations run one at a time, on Run's goroutine.
type Engine struct {
	db         *sql.DB
	dispatcher Dispatcher
	opts       Options
	trigger    chan struct{}
}

// NewEngine returns an engine; Run starts evaluating.
func NewEngine(db *sql.DB, dispatcher Dispatcher, opts Options) *Engine {
	if opts.Classifier == nil {
		opts.Classifier = apps.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Engine{db: db, dispatcher: dispatcher, opts: opts, trigger: make(chan struct{}, 1)}
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
		log.Println("Failed to update devices:", err)
	}

	rules, err := storage.ListRules(e.db)
	if err != nil {
		log.Println("Failed to load alert rules:", err)
		return
	}
	rules = slices.DeleteFunc(rules, func(r storage.RuleRecord) bool { return !r.Enabled })
	if len(rules) == 0 {
		return
	}

	devices, err := e.devices()
	if err != nil {
		log.Println("Failed to load devices:", err)
		return
	}

	var notifications []notify.Notification
	for _, rule := range rules {
		ns, err := e.evaluateRule(ctx, rule, devices, now)
		if err != nil {
			log.Printf("Alert rule %d (%s) failed: %v", rule.ID, rule.Type, err)
			continue
		}
		notifications = append(notifications, ns...)

		if err := storage.MarkRuleEvaluated(e.db, rule.ID, now); err != nil {
			log.Println(err)
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

	switch rule.Type {
	case NewDevice:
		return e.evaluateNewDevice(ctx, rule, devices, now)
	case TrafficThreshold:
		return e.evaluateTraffic(ctx, rule, devices, now)
	default:
		return nil, fmt.Errorf("unknown rule type %q", rule.Type)
	}
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
		d := device{mac: mac, ip: r.IPAddress.String(), vendor: vendors.Lookup(r.MACAddress.HardwareAddr), firstSeen: r.FirstSeen}
		if r.Name != nil {
			d.name = *r.Name
		}
		devices[mac] = d
	}
	return devices, nil
}

// withHostnames looks up the hostnames of the devices with the given MAC
// addresses, which messages are about.
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

// isRandomMAC reports whether a MAC address is locally administered, as the
// private addresses phones use on Wi-Fi are.
func isRandomMAC(mac string) bool {
	hw, err := net.ParseMAC(mac)
	return err == nil && len(hw) > 0 && hw[0]&0x02 != 0
}

func (e *Engine) evaluateNewDevice(ctx context.Context, rule storage.RuleRecord, devices map[string]device, now time.Time) ([]notify.Notification, error) {
	p, err := parseParams[NewDeviceParams](rule.Params)
	if err != nil {
		return nil, err
	}

	// Devices seen before the rule existed aren't new to it.
	baseline := now.Add(-newDeviceLookback)
	if rule.CreatedAt.After(baseline) {
		baseline = rule.CreatedAt
	}
	alerted, err := storage.AlertedSubjects(e.db, rule.ID, baseline)
	if err != nil {
		return nil, err
	}

	var fresh []string
	for mac, d := range devices {
		if d.firstSeen.Before(baseline) || alerted[mac] || (p.IgnoreRandomMACs && isRandomMAC(mac)) {
			continue
		}
		fresh = append(fresh, mac)
	}
	slices.SortFunc(fresh, func(a, b string) int {
		return cmp.Or(devices[a].firstSeen.Compare(devices[b].firstSeen), cmp.Compare(a, b))
	})

	named := e.withHostnames(ctx, devices, fresh)
	var ns []notify.Notification
	for _, mac := range fresh {
		d := named[mac]
		title, body := newDeviceText(d)
		labels := d.labels()
		labels["first_seen"] = d.firstSeen.UTC().Format(time.RFC3339)

		alert := storage.AlertRecord{
			RuleID: rule.ID, RuleName: rule.Name, Subject: mac, State: storage.AlertResolved, Severity: rule.Severity,
			Title: title, Body: body, Labels: labels,
			StartedAt: now, LastEvaluatedAt: now, LastNotifiedAt: &now, ResolvedAt: &now,
		}
		if alert.ID, err = storage.InsertAlert(e.db, alert); err != nil {
			return ns, err
		}
		ns = append(ns, e.notification(rule, alert, "firing", title, body))
	}
	return ns, nil
}

// trafficUsage sums each device's traffic in a rule's window.
func (e *Engine) trafficUsage(p TrafficParams, now time.Time) (map[string]float64, error) {
	records, err := storage.GetDeviceTraffic(e.db, now.Add(-time.Duration(p.WindowSeconds)*time.Second))
	if err != nil {
		return nil, err
	}

	usage := map[string]float64{}
	for _, r := range records {
		mac := r.MACAddress.String()
		if len(p.MACs) > 0 && !slices.Contains(p.MACs, mac) {
			continue
		}
		if p.App != "" {
			if r.Peer.Addr == nil || !apps.IsPublic(*r.Peer.Addr) || e.opts.Classifier.Classify(*r.Peer.Addr) != p.App {
				continue
			}
		}

		switch p.Direction {
		case Download:
			usage[mac] += float64(r.Download)
		case Upload:
			usage[mac] += float64(r.Upload)
		default:
			usage[mac] += float64(r.Download + r.Upload)
		}
	}
	return usage, nil
}

func (e *Engine) evaluateTraffic(ctx context.Context, rule storage.RuleRecord, devices map[string]device, now time.Time) ([]notify.Notification, error) {
	p, err := parseParams[TrafficParams](rule.Params)
	if err != nil {
		return nil, err
	}
	usage, err := e.trafficUsage(p, now)
	if err != nil {
		return nil, err
	}
	open, err := storage.FiringAlerts(e.db, rule.ID)
	if err != nil {
		return nil, err
	}

	// An alert keeps firing until the traffic is well under the threshold.
	threshold := float64(p.ThresholdBytes)
	firing := map[string]float64{}
	for mac, value := range usage {
		if _, isOpen := open[mac]; value > threshold || (isOpen && value >= ResolveFraction*threshold) {
			firing[mac] = value
		}
	}

	subjects := slices.Sorted(maps.Keys(firing))
	var resolved []string
	for mac := range open {
		if _, ok := firing[mac]; !ok {
			resolved = append(resolved, mac)
		}
	}
	slices.Sort(resolved)
	named := e.withHostnames(ctx, devices, append(slices.Clone(subjects), resolved...))

	var ns []notify.Notification
	for _, mac := range subjects {
		d, value := named[mac], firing[mac]
		title, body := trafficFiringText(rule.Name, p, d, value)

		alert, isOpen := open[mac]
		if !isOpen {
			alert = storage.AlertRecord{
				RuleID: rule.ID, RuleName: rule.Name, Subject: mac, State: storage.AlertFiring, Severity: rule.Severity,
				Value: &value, Title: title, Body: body, Labels: d.labels(),
				StartedAt: now, LastEvaluatedAt: now, LastNotifiedAt: &now,
			}
			if alert.ID, err = storage.InsertAlert(e.db, alert); err != nil {
				return ns, err
			}
			ns = append(ns, e.notification(rule, alert, "firing", title, body))
			continue
		}

		alert.Value, alert.Title, alert.Body, alert.Labels, alert.LastEvaluatedAt = &value, title, body, d.labels(), now
		if repeat := rule.RepeatIntervalSeconds; repeat != nil &&
			(alert.LastNotifiedAt == nil || now.Sub(*alert.LastNotifiedAt) >= time.Duration(*repeat)*time.Second) {
			alert.LastNotifiedAt = &now
			title, body := trafficReminderText(rule.Name, p, d, value, alert.StartedAt)
			ns = append(ns, e.notification(rule, alert, "reminder", title, body))
		}
		if err := storage.UpdateAlert(e.db, alert); err != nil {
			return ns, err
		}
	}

	for _, mac := range resolved {
		alert := open[mac]
		alert.State, alert.ResolvedAt, alert.LastEvaluatedAt = storage.AlertResolved, &now, now
		if rule.NotifyOnResolve {
			alert.LastNotifiedAt = &now
			title, body := trafficResolvedText(p, named[mac], now.Sub(alert.StartedAt))
			ns = append(ns, e.notification(rule, alert, "resolved", title, body))
		}
		if err := storage.UpdateAlert(e.db, alert); err != nil {
			return ns, err
		}
	}
	return ns, nil
}

// link is where a message about an alert points: the device's page.
func (e *Engine) link(alert storage.AlertRecord) string {
	if e.opts.PublicURL == "" {
		return ""
	}
	if mac := alert.Labels["mac"]; mac != "" {
		return e.opts.PublicURL + "/clients/" + mac
	}
	return e.opts.PublicURL + "/alerts"
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
