package alerting

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// device is what is known about a device, for messages.
type device struct {
	mac       string
	ip        string
	name      string
	hostname  string
	vendor    string
	firstSeen time.Time
}

// label is how messages call a device: the name the user gave it, else its
// hostname, else who made it, else its MAC address.
func (d device) label() string {
	switch {
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
	var parts []string
	if d.vendor != "" && d.name+d.hostname != "" {
		parts = append(parts, d.vendor)
	}
	if d.label() != d.mac {
		parts = append(parts, d.mac)
	}
	if d.ip != "" {
		parts = append(parts, d.ip)
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

// labels describe a device in an alert record and webhook payloads.
func (d device) labels() map[string]string {
	labels := map[string]string{"mac": d.mac}
	for key, value := range map[string]string{
		"ip": d.ip, "device_name": d.name, "hostname": d.hostname, "vendor": d.vendor,
	} {
		if value != "" {
			labels[key] = value
		}
	}
	return labels
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
	body = fmt.Sprintf("%s joined the network, first seen at %s.", d.labelled(), clock(d.firstSeen))
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
	app := ""
	if p.App != "" {
		preposition := map[string]string{Download: " from ", Upload: " to ", Total: " with "}[p.Direction]
		app = preposition + p.App
	}
	return fmt.Sprintf("%s %s%s in %s", verb, formatBytes(value), app, lastWindow(p.WindowSeconds))
}

func trafficFiringText(ruleName string, p TrafficParams, d device, value float64) (title, body string) {
	title = trafficTitle(p, d)
	body = fmt.Sprintf("%s %s, over the %s of the rule “%s”.",
		d.labelled(), trafficAmount(p, value), formatBytes(float64(p.ThresholdBytes)), ruleName)
	return title, body
}

func trafficReminderText(ruleName string, p TrafficParams, d device, value float64, since time.Time) (title, body string) {
	title = "Still: " + trafficTitle(p, d)
	body = fmt.Sprintf("%s %s, over the %s of the rule “%s” since %s.",
		d.labelled(), trafficAmount(p, value), formatBytes(float64(p.ThresholdBytes)), ruleName, clock(since))
	return title, body
}

func trafficResolvedText(p TrafficParams, d device, lasted time.Duration) (title, body string) {
	title = "Resolved: " + trafficTitle(p, d)
	body = fmt.Sprintf("%s is back under %s in %s, after %s.",
		d.label(), formatBytes(float64(p.ThresholdBytes)), lastWindow(p.WindowSeconds), formatDuration(lasted))
	return title, body
}
