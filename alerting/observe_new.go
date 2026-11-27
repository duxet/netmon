package alerting

import (
	"context"
	"strconv"
	"time"

	"github.com/duxet/netmon/apps"
	"github.com/duxet/netmon/blocklists"
	"github.com/duxet/netmon/storage"
)

func (e *Engine) observeOnline(ctx context.Context, rule storage.RuleRecord, p OnlineParams, devices map[string]device, since time.Time) ([]candidate, error) {
	quiet := time.Duration(p.InactiveSeconds) * time.Second
	comebacks, err := storage.GetComebacks(e.db, since, quiet)
	if err != nil {
		return nil, err
	}

	back := map[string]storage.ComebackRecord{}
	var macs []string
	for _, c := range comebacks {
		mac := c.MACAddress.String()
		d, known := devices[mac]
		// A device first seen while it would have been quiet is new, not back.
		if !inScope(p.MACs, mac) || !known || !d.firstSeen.Before(since.Add(-quiet)) {
			continue
		}
		back[mac] = c
		macs = append(macs, mac)
	}

	named := e.withHostnames(ctx, devices, macs)
	found := make([]candidate, 0, len(macs))
	for _, mac := range macs {
		c, d := back[mac], named[mac]
		quietFor := quiet
		if last, ok, err := storage.LastTrafficBefore(e.db, c.MACAddress.HardwareAddr, since); err != nil {
			return nil, err
		} else if ok {
			quietFor = c.Back.Sub(last)
		}

		title, body := onlineText(rule.Name, p, d, c.Back, quietFor)
		labels := d.labels()
		labels["back_at"] = c.Back.UTC().Format(time.RFC3339)
		labels["quiet_s"] = strconv.FormatInt(int64(quietFor.Seconds()), 10)
		seconds := quietFor.Seconds()
		found = append(found, candidate{subject: mac, label: d.label(), value: &seconds, fires: true, labels: labels, title: title, body: body})
	}
	return found, nil
}

// learningTime is how long a device has to be known before new_destination
// rules alert about it: until then, everything is new.
const learningTime = 24 * time.Hour

// baseline is what a new_destination rule learned devices talk to.
type baseline struct {
	// rule is the rule's last change and days its baseline, which it was
	// learned for.
	rule time.Time
	days int
	// until is how far the traffic it learned from goes.
	until time.Time
	// seen are the countries or networks, by device.
	seen map[string]map[string]bool
}

func (b *baseline) learn(mac, key string) {
	if b.seen[mac] == nil {
		b.seen[mac] = map[string]bool{}
	}
	b.seen[mac][key] = true
}

// destinationOf returns what a new_destination rule groups remote addresses
// by: their country code or AS number, and its name.
func (e *Engine) destinationOf(kind string) (func(r storage.DeviceTrafficRecord) (key, name string, ok bool), error) {
	switch kind {
	case ByCountry:
		if e.opts.Countries == nil || !e.opts.Countries.Available() {
			return nil, ErrNoGeoIP
		}
		return func(r storage.DeviceTrafficRecord) (string, string, bool) {
			return e.opts.Countries.Lookup(*r.Peer.Addr)
		}, nil
	default:
		if e.opts.ASNs == nil || !e.opts.ASNs.Available() {
			return nil, ErrNoASNs
		}
		return func(r storage.DeviceTrafficRecord) (string, string, bool) {
			number, organization, ok := e.opts.ASNs.Lookup(*r.Peer.Addr)
			return "AS" + strconv.FormatUint(uint64(number), 10), organization, ok
		}, nil
	}
}

// learned returns the baseline of a rule up to since, learning what is
// missing from the database. Rules that aren't saved yet, as for previews,
// start from scratch every time.
func (e *Engine) learned(rule storage.RuleRecord, p DestinationParams, since time.Time,
	keyOf func(storage.DeviceTrafficRecord) (string, string, bool)) (*baseline, error) {
	e.baselinesMu.Lock()
	defer e.baselinesMu.Unlock()
	if e.baselines == nil {
		e.baselines = map[int64]*baseline{}
	}

	b := e.baselines[rule.ID]
	if rule.ID == 0 || b == nil || !b.rule.Equal(rule.UpdatedAt) || b.days != p.BaselineDays {
		b = &baseline{rule: rule.UpdatedAt, days: p.BaselineDays, until: since.Add(-time.Duration(p.BaselineDays) * 24 * time.Hour),
			seen: map[string]map[string]bool{}}
	}
	if since.After(b.until) {
		records, err := storage.GetDevicePeers(e.db, b.until, since)
		if err != nil {
			return nil, err
		}
		for _, r := range records {
			if r.Peer.Addr == nil || !apps.IsPublic(*r.Peer.Addr) {
				continue
			}
			if key, _, ok := keyOf(r); ok {
				b.learn(r.MACAddress.String(), key)
			}
		}
		b.until = since
	}
	if rule.ID != 0 {
		e.baselines[rule.ID] = b
	}
	return b, nil
}

func (e *Engine) observeDestinations(ctx context.Context, rule storage.RuleRecord, p DestinationParams, devices map[string]device, now, since time.Time) ([]candidate, error) {
	keyOf, err := e.destinationOf(p.Kind)
	if err != nil {
		return nil, err
	}
	b, err := e.learned(rule, p, since, keyOf)
	if err != nil {
		return nil, err
	}

	records, err := storage.GetDeviceTraffic(e.db, since)
	if err != nil {
		return nil, err
	}

	events := map[string]*destinations{}
	names := map[string]string{}
	var learnt [][2]string
	for _, r := range records {
		mac := r.MACAddress.String()
		if !inScope(p.MACs, mac) || r.Peer.Addr == nil || !apps.IsPublic(*r.Peer.Addr) {
			continue
		}
		key, name, ok := keyOf(r)
		if !ok {
			continue
		}
		learnt = append(learnt, [2]string{mac, key})
		if b.seen[mac][key] {
			continue
		}
		if d, known := devices[mac]; !known || d.firstSeen.After(now.Add(-learningTime)) {
			continue
		}

		subject := mac + "|" + key
		if events[subject] == nil {
			events[subject] = &destinations{mac: mac, peers: map[string]uint64{}}
		}
		events[subject].add(r.Peer.String(), r.Download+r.Upload)
		names[key] = name
	}

	// What it talks to now is known from now on, for saved rules.
	if rule.ID != 0 {
		e.baselinesMu.Lock()
		for _, pair := range learnt {
			b.learn(pair[0], pair[1])
		}
		e.baselinesMu.Unlock()
	}

	return e.eventCandidates(ctx, devices, events, func(subject string, d device, dest *destinations) candidate {
		_, key, _ := cutLast(subject, "|")
		peer := dest.biggest()
		title, body := destinationText(rule.Name, p, d, key, names[key], peer, len(dest.peers), dest.bytes)
		labels := d.labels()
		labels["peer"] = peer
		if p.Kind == ByCountry {
			labels["country"], labels["country_name"] = key, names[key]
		} else {
			labels["asn"], labels["organization"] = key, names[key]
		}
		return candidate{title: title, body: body, labels: labels}
	}), nil
}

func (e *Engine) observeBlocklists(ctx context.Context, rule storage.RuleRecord, p BlocklistParams, devices map[string]device, since time.Time) ([]candidate, error) {
	if e.opts.Blocklists == nil {
		return nil, nil
	}
	records, err := storage.GetDeviceTraffic(e.db, since)
	if err != nil {
		return nil, err
	}

	lists := p.lists()
	events := map[string]*destinations{}
	for _, r := range records {
		mac := r.MACAddress.String()
		if !inScope(p.MACs, mac) || r.Peer.Addr == nil || !apps.IsPublic(*r.Peer.Addr) {
			continue
		}
		for _, id := range e.opts.Blocklists.Match(*r.Peer.Addr, lists) {
			subject := mac + "|" + id
			if events[subject] == nil {
				events[subject] = &destinations{mac: mac, peers: map[string]uint64{}}
			}
			events[subject].add(r.Peer.String(), r.Download+r.Upload)
		}
	}

	return e.eventCandidates(ctx, devices, events, func(subject string, d device, dest *destinations) candidate {
		_, id, _ := cutLast(subject, "|")
		list := listByID(id)
		peer := dest.biggest()
		title, body := blocklistText(rule.Name, d, list, peer, len(dest.peers), dest.bytes)
		labels := d.labels()
		labels["list"], labels["list_name"], labels["peer"] = id, list.Name, peer
		return candidate{title: title, body: body, labels: labels}
	}), nil
}

func listByID(id string) blocklists.List {
	for _, l := range blocklists.Lists {
		if l.ID == id {
			return l
		}
	}
	return blocklists.List{ID: id, Name: id}
}
