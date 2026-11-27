package alerting

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"net"
	"slices"
	"strconv"
	"time"

	"github.com/duxet/netmon/apps"
	"github.com/duxet/netmon/storage"
)

// networkSubject is the subject of alerts about the whole network.
const networkSubject = "network"

// candidate is a subject a rule looked at. Stateful rules report every
// subject they measured, with fires telling whether the condition holds;
// event rules only the events they found.
type candidate struct {
	subject string
	// label is who the candidate is about, e.g. a device's name.
	label  string
	value  *float64
	fires  bool
	labels map[string]string
	// title and body are the message as the alert starts.
	title, body string
	// reminder is the message of a repeat while the condition lasts, for
	// stateful rules.
	reminder func(since time.Time) (title, body string)
}

// observe measures what a rule looks at, now. Event rules look at what
// happened since the given time. Stateful rules keep an open alert firing
// until its condition is well over.
func (e *Engine) observe(ctx context.Context, rule storage.RuleRecord, devices map[string]device, now, since time.Time, open map[string]storage.AlertRecord) ([]candidate, error) {
	switch rule.Type {
	case NewDevice:
		p, err := parseParams[NewDeviceParams](rule.Params)
		if err != nil {
			return nil, err
		}
		return e.observeNewDevices(ctx, p, devices, since), nil
	case TrafficThreshold:
		p, err := parseParams[TrafficParams](rule.Params)
		if err != nil {
			return nil, err
		}
		return e.observeTraffic(ctx, rule, p, devices, now, open)
	case DeviceInactive:
		p, err := parseParams[InactiveParams](rule.Params)
		if err != nil {
			return nil, err
		}
		return e.observeInactive(ctx, rule, p, devices, now), nil
	case DestinationPort:
		p, err := parseParams[PortParams](rule.Params)
		if err != nil {
			return nil, err
		}
		return e.observePorts(ctx, rule, p, devices, since)
	case DestinationCountry:
		p, err := parseParams[CountryParams](rule.Params)
		if err != nil {
			return nil, err
		}
		return e.observeCountries(ctx, rule, p, devices, since)
	case DeviceOnline:
		p, err := parseParams[OnlineParams](rule.Params)
		if err != nil {
			return nil, err
		}
		return e.observeOnline(ctx, rule, p, devices, since)
	case NewDestination:
		p, err := parseParams[DestinationParams](rule.Params)
		if err != nil {
			return nil, err
		}
		return e.observeDestinations(ctx, rule, p, devices, now, since)
	case Blocklist:
		p, err := parseParams[BlocklistParams](rule.Params)
		if err != nil {
			return nil, err
		}
		return e.observeBlocklists(ctx, rule, p, devices, since)
	default:
		return nil, fmt.Errorf("unknown rule type %q", rule.Type)
	}
}

// isRandomMAC reports whether a MAC address is locally administered, as the
// private addresses phones use on Wi-Fi are.
func isRandomMAC(mac string) bool {
	hw, err := net.ParseMAC(mac)
	return err == nil && len(hw) > 0 && hw[0]&0x02 != 0
}

func inScope(macs []string, mac string) bool {
	return len(macs) == 0 || slices.Contains(macs, mac)
}

func (e *Engine) observeNewDevices(ctx context.Context, p NewDeviceParams, devices map[string]device, since time.Time) []candidate {
	var fresh []string
	for mac, d := range devices {
		if d.firstSeen.Before(since) || (p.IgnoreRandomMACs && isRandomMAC(mac)) {
			continue
		}
		fresh = append(fresh, mac)
	}
	slices.SortFunc(fresh, func(a, b string) int {
		return cmp.Or(devices[a].firstSeen.Compare(devices[b].firstSeen), cmp.Compare(a, b))
	})

	named := e.withHostnames(ctx, devices, fresh)
	found := make([]candidate, 0, len(fresh))
	for _, mac := range fresh {
		d := named[mac]
		title, body := newDeviceText(d)
		labels := d.labels()
		labels["first_seen"] = d.firstSeen.UTC().Format(time.RFC3339)
		found = append(found, candidate{subject: mac, label: d.label(), fires: true, labels: labels, title: title, body: body})
	}
	return found
}

// trafficUsage sums the traffic in a rule's window: per device, or with
// Network the internet traffic of all of them together.
func (e *Engine) trafficUsage(p TrafficParams, now time.Time) (map[string]float64, error) {
	records, err := storage.GetDeviceTraffic(e.db, now.Add(-time.Duration(p.WindowSeconds)*time.Second))
	if err != nil {
		return nil, err
	}

	usage := map[string]float64{}
	for _, r := range records {
		mac := r.MACAddress.String()
		if !inScope(p.MACs, mac) {
			continue
		}
		public := r.Peer.Addr != nil && apps.IsPublic(*r.Peer.Addr)
		if p.App != "" && (!public || e.opts.Classifier.Classify(*r.Peer.Addr) != p.App) {
			continue
		}

		subject := mac
		if p.Network {
			// Traffic between two local devices would count twice, and
			// isn't what a data cap counts either.
			if !public {
				continue
			}
			subject = networkSubject
		}

		switch p.Direction {
		case Download:
			usage[subject] += float64(r.Download)
		case Upload:
			usage[subject] += float64(r.Upload)
		default:
			usage[subject] += float64(r.Download + r.Upload)
		}
	}
	return usage, nil
}

func (e *Engine) observeTraffic(ctx context.Context, rule storage.RuleRecord, p TrafficParams, devices map[string]device, now time.Time, open map[string]storage.AlertRecord) ([]candidate, error) {
	usage, err := e.trafficUsage(p, now)
	if err != nil {
		return nil, err
	}

	// Most traffic first.
	subjects := slices.Collect(maps.Keys(usage))
	slices.SortFunc(subjects, func(a, b string) int {
		return cmp.Or(cmp.Compare(usage[b], usage[a]), cmp.Compare(a, b))
	})
	named := e.withHostnames(ctx, devices, slices.DeleteFunc(slices.Clone(subjects), func(s string) bool { return s == networkSubject }))

	// An alert keeps firing until the traffic is well under the threshold.
	threshold := float64(p.ThresholdBytes)
	found := make([]candidate, 0, len(subjects))
	for _, subject := range subjects {
		value := usage[subject]
		d := named[subject]
		if subject == networkSubject {
			d = theNetwork
		}
		_, isOpen := open[subject]

		title, body := trafficFiringText(rule.Name, p, d, value)
		found = append(found, candidate{
			subject: subject, label: d.label(), value: &value, labels: d.labels(),
			fires: value > threshold || (isOpen && value >= ResolveFraction*threshold),
			title: title, body: body,
			reminder: func(since time.Time) (string, string) {
				return trafficReminderText(rule.Name, p, d, value, since)
			},
		})
	}
	return found, nil
}

func (e *Engine) observeInactive(ctx context.Context, rule storage.RuleRecord, p InactiveParams, devices map[string]device, now time.Time) []candidate {
	// Devices never seen can't be told to be quiet: there is nothing to go by.
	var known []string
	for _, mac := range p.MACs {
		if _, ok := devices[mac]; ok {
			known = append(known, mac)
		}
	}
	named := e.withHostnames(ctx, devices, known)

	found := make([]candidate, 0, len(known))
	for _, mac := range known {
		d := named[mac]
		since := d.lastSeen
		if since.Before(e.started) {
			since = e.started
		}
		quiet := now.Sub(since)
		seconds := quiet.Seconds()

		title, body := inactiveText(rule.Name, p, d, since, e.started, now)
		found = append(found, candidate{
			subject: mac, label: d.label(), value: &seconds, labels: d.labels(),
			fires: quiet > time.Duration(p.InactiveSeconds)*time.Second,
			title: title, body: body,
			reminder: func(time.Time) (string, string) {
				return "Still: " + title, body
			},
		})
	}
	return found
}

// destinations are the addresses a device exchanged traffic with, for one
// event, e.g. a port or a country.
type destinations struct {
	mac   string
	bytes uint64
	peers map[string]uint64
}

func (d *destinations) add(peer string, bytes uint64) {
	d.bytes += bytes
	d.peers[peer] += bytes
}

// biggest is the address with the most traffic.
func (d *destinations) biggest() string {
	var best string
	for peer, bytes := range d.peers {
		if best == "" || bytes > d.peers[best] || (bytes == d.peers[best] && peer < best) {
			best = peer
		}
	}
	return best
}

func protocolName(proto uint8) string {
	switch proto {
	case 6:
		return TCP
	case 17:
		return UDP
	default:
		return strconv.Itoa(int(proto))
	}
}

func (e *Engine) observePorts(ctx context.Context, rule storage.RuleRecord, p PortParams, devices map[string]device, since time.Time) ([]candidate, error) {
	ports := make([]uint16, 0, len(p.Ports))
	for _, port := range p.Ports {
		ports = append(ports, uint16(port))
	}
	protocols := map[string][]int{TCP: {6}, UDP: {17}}[p.Protocol]

	records, err := storage.GetPortTraffic(e.db, since, ports, protocols)
	if err != nil {
		return nil, err
	}

	events := map[string]*destinations{}
	portOf := map[string]string{}
	for _, r := range records {
		mac := r.MACAddress.String()
		if !inScope(p.MACs, mac) {
			continue
		}
		port := strconv.Itoa(int(r.Port)) + "/" + protocolName(r.IPProto)
		subject := mac + "|" + port
		if events[subject] == nil {
			events[subject] = &destinations{mac: mac, peers: map[string]uint64{}}
			portOf[subject] = port
		}
		events[subject].add(r.Peer.String(), r.Bytes)
	}

	return e.eventCandidates(ctx, devices, events, func(subject string, d device, dest *destinations) candidate {
		peer := dest.biggest()
		title, body := portText(rule.Name, p, d, portOf[subject], peer, len(dest.peers), dest.bytes)
		labels := d.labels()
		port, protocol, _ := cutLast(portOf[subject], "/")
		labels["port"], labels["protocol"], labels["peer"] = port, protocol, peer
		return candidate{title: title, body: body, labels: labels}
	}), nil
}

func (e *Engine) observeCountries(ctx context.Context, rule storage.RuleRecord, p CountryParams, devices map[string]device, since time.Time) ([]candidate, error) {
	if e.opts.Countries == nil || !e.opts.Countries.Available() {
		return nil, ErrNoGeoIP
	}

	records, err := storage.GetDeviceTraffic(e.db, since)
	if err != nil {
		return nil, err
	}

	events := map[string]*destinations{}
	names := map[string]string{}
	for _, r := range records {
		mac := r.MACAddress.String()
		if !inScope(p.MACs, mac) || r.Peer.Addr == nil || !apps.IsPublic(*r.Peer.Addr) {
			continue
		}
		code, name, ok := e.opts.Countries.Lookup(*r.Peer.Addr)
		if !ok {
			continue
		}
		if listed := slices.Contains(p.Countries, code); listed != (p.Mode == Deny) {
			continue
		}

		subject := mac + "|" + code
		if events[subject] == nil {
			events[subject] = &destinations{mac: mac, peers: map[string]uint64{}}
		}
		events[subject].add(r.Peer.String(), r.Download+r.Upload)
		names[code] = name
	}

	return e.eventCandidates(ctx, devices, events, func(subject string, d device, dest *destinations) candidate {
		_, code, _ := cutLast(subject, "|")
		peer := dest.biggest()
		title, body := countryText(rule.Name, p, d, code, names[code], peer, len(dest.peers), dest.bytes)
		labels := d.labels()
		labels["country"], labels["country_name"], labels["peer"] = code, names[code], peer
		return candidate{title: title, body: body, labels: labels}
	}), nil
}

// eventCandidates turns what an event rule found into candidates, in order
// of subject, with the devices' hostnames looked up.
func (e *Engine) eventCandidates(ctx context.Context, devices map[string]device, events map[string]*destinations,
	describe func(subject string, d device, dest *destinations) candidate) []candidate {
	subjects := slices.Sorted(maps.Keys(events))
	var macs []string
	for _, subject := range subjects {
		macs = append(macs, events[subject].mac)
	}
	named := e.withHostnames(ctx, devices, slices.Compact(slices.Sorted(slices.Values(macs))))

	found := make([]candidate, 0, len(subjects))
	for _, subject := range subjects {
		dest := events[subject]
		d := named[dest.mac]
		c := describe(subject, d, dest)
		bytes := float64(dest.bytes)
		c.subject, c.label, c.value, c.fires = subject, d.label(), &bytes, true
		c.labels["peers"] = strconv.Itoa(len(dest.peers))
		found = append(found, c)
	}
	return found
}

// cutLast splits s around the last sep.
func cutLast(s, sep string) (before, after string, found bool) {
	for i := len(s) - len(sep); i >= 0; i-- {
		if s[i:i+len(sep)] == sep {
			return s[:i], s[i+len(sep):], true
		}
	}
	return s, "", false
}
