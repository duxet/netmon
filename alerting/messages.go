package alerting

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/duxet/netmon/blocklists"
)

// device is what is known about a device, for messages; or, with network
// set, the whole network.
type device struct {
	mac       string
	ip        string
	name      string
	hostname  string
	vendor    string
	firstSeen time.Time
	lastSeen  time.Time
	// networkID and networkName are of the network the device's address is
	// in, if any; for the whole network, networkName names the networks a
	// rule is limited to.
	networkID   int64
	networkName string
	network     bool
}

// theNetwork stands for all devices together.
var theNetwork = device{network: true}

// deviceFromLabels is the device an alert's labels describe.
func deviceFromLabels(labels map[string]string) device {
	if labels["scope"] == "network" {
		d := theNetwork
		d.networkName = labels["network"]
		return d
	}
	d := device{
		mac: labels["mac"], ip: labels["ip"], name: labels["device_name"],
		hostname: labels["hostname"], vendor: labels["vendor"], networkName: labels["network"],
	}
	d.networkID, _ = strconv.ParseInt(labels["network_id"], 10, 64)
	return d
}

// label is how messages call a device: the name the user gave it, else its
// hostname, else who made it, else its MAC address.
func (d device) label() string {
	switch {
	case d.network && d.networkName != "":
		return "the " + d.networkName + " network"
	case d.network:
		return "the network"
	case d.name != "":
		return d.name
	case d.hostname != "":
		return strings.TrimSuffix(d.hostname, ".")
	case d.vendor != "":
		return d.vendor + " device"
	default:
		return d.mac
	}
}

// details are what identifies a device besides its label, e.g.
// "Samsung, 3c:22:fb:12:34:56, 192.168.1.40".
func (d device) details() string {
	if d.network {
		return ""
	}
	var parts []string
	if d.vendor != "" && d.name+d.hostname != "" {
		parts = append(parts, d.vendor)
	}
	if d.label() != d.mac {
		parts = append(parts, d.mac)
	}
	switch {
	case d.ip != "" && d.networkName != "":
		parts = append(parts, d.ip+" on "+d.networkName)
	case d.ip != "":
		parts = append(parts, d.ip)
	case d.networkName != "":
		parts = append(parts, "on "+d.networkName)
	}
	return strings.Join(parts, ", ")
}

// labelled is the label with the details in brackets.
func (d device) labelled() string {
	if details := d.details(); details != "" {
		return d.label() + " (" + details + ")"
	}
	return d.label()
}

// subject is labelled, to start a sentence with.
func (d device) subject() string {
	if d.network {
		label := d.label()
		return strings.ToUpper(label[:1]) + label[1:]
	}
	return d.labelled()
}

// labels describe a device in an alert record and webhook payloads.
func (d device) labels() map[string]string {
	if d.network {
		labels := map[string]string{"scope": "network"}
		if d.networkName != "" {
			labels["network"] = d.networkName
		}
		return labels
	}
	labels := map[string]string{"mac": d.mac}
	if d.networkID != 0 {
		labels["network_id"] = strconv.FormatInt(d.networkID, 10)
	}
	for key, value := range map[string]string{
		"ip": d.ip, "device_name": d.name, "hostname": d.hostname, "vendor": d.vendor, "network": d.networkName,
	} {
		if value != "" {
			labels[key] = value
		}
	}
	return labels
}

// joinAnd joins words as a sentence lists them: "a", "a and b", "a, b and c".
func joinAnd(words []string) string {
	if len(words) <= 1 {
		return strings.Join(words, "")
	}
	return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
}

// formatBytes formats a number of bytes with decimal units and three
// significant digits, as the web UI does: 999 B, 1.23 kB, 6.66 GB.
func formatBytes(n float64) string {
	units := []string{"B", "kB", "MB", "GB", "TB", "PB"}
	unit := 0
	for n >= 999.5 && unit < len(units)-1 {
		n /= 1000
		unit++
	}
	if unit == 0 {
		return strconv.FormatFloat(math.Round(n), 'f', 0, 64) + " B"
	}

	decimals := 2
	switch {
	case n >= 99.95:
		decimals = 0
	case n >= 9.995:
		decimals = 1
	}
	s := strconv.FormatFloat(n, 'f', decimals, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s + " " + units[unit]
}

// lastWindow describes a window of seconds ending now: "the last hour",
// "the last 15 minutes".
func lastWindow(seconds int64) string {
	switch {
	case seconds == 3600:
		return "the last hour"
	case seconds%3600 == 0:
		return fmt.Sprintf("the last %d hours", seconds/3600)
	default:
		return fmt.Sprintf("the last %d minutes", seconds/60)
	}
}

// formatDuration describes how long something lasted, to the minute.
func formatDuration(d time.Duration) string {
	minutes := int(d.Round(time.Minute) / time.Minute)
	if minutes < 1 {
		return "less than a minute"
	}

	plural := func(n int, unit string) string {
		if n == 1 {
			return "1 " + unit
		}
		return fmt.Sprintf("%d %ss", n, unit)
	}

	days, hours, minutes := minutes/(24*60), minutes/60%24, minutes%60
	var parts []string
	if days > 0 {
		parts = append(parts, plural(days, "day"))
	}
	if hours > 0 {
		parts = append(parts, plural(hours, "hour"))
	}
	if minutes > 0 && days == 0 {
		parts = append(parts, plural(minutes, "minute"))
	}
	return strings.Join(parts, " ")
}

// clock formats a time of day in the server's time zone.
func clock(t time.Time) string {
	return t.Local().Format("15:04")
}

func newDeviceText(d device) (title, body string) {
	title = "New device: " + d.label()
	joined := "the network"
	if d.networkName != "" {
		joined = "the " + d.networkName + " network"
	}
	body = fmt.Sprintf("%s joined %s, first seen at %s.", d.labelled(), joined, clock(d.firstSeen))
	return title, body
}

// trafficTitle names the alert, e.g. "Large download on Living room TV" or
// "High YouTube traffic on Tablet".
func trafficTitle(p TrafficParams, d device) string {
	app := ""
	if p.App != "" {
		app = p.App + " "
	}
	switch p.Direction {
	case Download:
		return fmt.Sprintf("Large %sdownload on %s", app, d.label())
	case Upload:
		return fmt.Sprintf("Large %supload from %s", app, d.label())
	default:
		return fmt.Sprintf("High %straffic on %s", app, d.label())
	}
}

// trafficAmount says what a device did, e.g. "downloaded 6.2 GB from YouTube
// in the last hour".
func trafficAmount(p TrafficParams, value float64) string {
	verb := map[string]string{Download: "downloaded", Upload: "uploaded", Total: "used"}[p.Direction]
	preposition := map[string]string{Download: " from ", Upload: " to ", Total: " with "}[p.Direction]
	app := ""
	switch {
	case p.App != "":
		app = preposition + p.App
	case p.Network:
		app = preposition + "the internet"
	}
	return fmt.Sprintf("%s %s%s in %s", verb, formatBytes(value), app, lastWindow(p.WindowSeconds))
}

func trafficFiringText(ruleName string, p TrafficParams, d device, value float64) (title, body string) {
	title = trafficTitle(p, d)
	body = fmt.Sprintf("%s %s, over the %s of the rule “%s”.",
		d.subject(), trafficAmount(p, value), formatBytes(float64(p.ThresholdBytes)), ruleName)
	return title, body
}

func trafficReminderText(ruleName string, p TrafficParams, d device, value float64, since time.Time) (title, body string) {
	title = "Still: " + trafficTitle(p, d)
	body = fmt.Sprintf("%s %s, over the %s of the rule “%s” since %s.",
		d.subject(), trafficAmount(p, value), formatBytes(float64(p.ThresholdBytes)), ruleName, clock(since))
	return title, body
}

func trafficResolvedText(p TrafficParams, d device, lasted time.Duration) (title, body string) {
	title = "Resolved: " + trafficTitle(p, d)
	label := d.label()
	if d.network {
		label = d.subject()
	}
	body = fmt.Sprintf("%s is back under %s in %s, after %s.",
		label, formatBytes(float64(p.ThresholdBytes)), lastWindow(p.WindowSeconds), formatDuration(lasted))
	return title, body
}

// quietSince says since when a device has had no traffic, as far as netmon
// knows: it can't tell about the time it wasn't running.
func quietSince(d device, since, started time.Time) string {
	if d.lastSeen.Before(started) {
		return "since netmon started at " + clock(since)
	}
	return "since " + clock(since)
}

func inactiveText(ruleName string, p InactiveParams, d device, since, started time.Time, now time.Time) (title, body string) {
	title = "No traffic from " + d.label()
	body = fmt.Sprintf("%s has had no traffic %s, for %s. The rule “%s” expects some at least every %s.",
		d.labelled(), quietSince(d, since, started), formatDuration(now.Sub(since)), ruleName,
		formatDuration(time.Duration(p.InactiveSeconds)*time.Second))
	return title, body
}

// inactiveResolvedText is about a device that was quiet for as long as the
// alert's last value says: an alert starts only once the device has been
// quiet for a while.
func inactiveResolvedText(d device, lasted time.Duration) (title, body string) {
	title = "Traffic again from " + d.label()
	body = fmt.Sprintf("%s has traffic again, after %s without.", d.label(), formatDuration(lasted))
	return title, body
}

// others says how many more addresses there were besides the example.
func others(peers int) string {
	switch peers {
	case 0, 1:
		return ""
	case 2:
		return " and 1 other address"
	default:
		return fmt.Sprintf(" and %d other addresses", peers-1)
	}
}

func portText(ruleName string, p PortParams, d device, port string, peer string, peers int, bytes uint64) (title, body string) {
	title = fmt.Sprintf("%s connected to port %s", d.label(), port)
	watched := make([]string, 0, len(p.Ports))
	for _, port := range p.Ports {
		watched = append(watched, strconv.Itoa(port))
	}
	body = fmt.Sprintf("%s connected to port %s of %s%s, %s in all. The rule “%s” watches ports %s.",
		d.labelled(), port, peer, others(peers), formatBytes(float64(bytes)), ruleName, strings.Join(watched, ", "))
	return title, body
}

func countryText(ruleName string, p CountryParams, d device, code, name, peer string, peers int, bytes uint64) (title, body string) {
	title = fmt.Sprintf("%s exchanged traffic with %s", d.label(), name)
	reason := fmt.Sprintf("The rule “%s” alerts on traffic with %s.", ruleName, strings.Join(p.Countries, ", "))
	if p.Mode == Allow {
		reason = fmt.Sprintf("The rule “%s” allows only %s.", ruleName, strings.Join(p.Countries, ", "))
	}
	body = fmt.Sprintf("%s exchanged %s with %s%s in %s (%s). %s",
		d.labelled(), formatBytes(float64(bytes)), peer, others(peers), name, code, reason)
	return title, body
}

func onlineText(ruleName string, p OnlineParams, d device, back time.Time, quietFor time.Duration) (title, body string) {
	title = d.label() + " is back online"
	body = fmt.Sprintf("%s has traffic again since %s, after %s without. The rule “%s” watches for devices quiet for %s or more.",
		d.labelled(), clock(back), formatDuration(quietFor), ruleName, formatDuration(time.Duration(p.InactiveSeconds)*time.Second))
	return title, body
}

func destinationText(ruleName string, p DestinationParams, d device, key, name, peer string, peers int, bytes uint64) (title, body string) {
	where := fmt.Sprintf("%s (%s)", name, key)
	if p.Kind == ByASN {
		where = fmt.Sprintf("%s %s", key, name)
	}
	title = fmt.Sprintf("%s exchanged traffic with %s for the first time", d.label(), map[string]string{ByCountry: name, ByASN: where}[p.Kind])
	body = fmt.Sprintf("%s exchanged %s with %s%s in %s, which it hadn't in the %d days before. The rule “%s” alerts on new %s.",
		d.labelled(), formatBytes(float64(bytes)), peer, others(peers), where, p.BaselineDays, ruleName,
		map[string]string{ByCountry: "countries", ByASN: "networks"}[p.Kind])
	return title, body
}

func blocklistText(ruleName string, d device, list blocklists.List, peer string, peers int, bytes uint64) (title, body string) {
	title = fmt.Sprintf("%s talked to an address on %s", d.label(), list.Name)
	body = fmt.Sprintf("%s exchanged %s with %s%s, which %s on %s: %s It may be infected, or tricked into it. The rule “%s” checks it.",
		d.labelled(), formatBytes(float64(bytes)), peer, others(peers), map[bool]string{true: "is", false: "are"}[peers <= 1],
		list.Name, list.About, ruleName)
	return title, body
}
