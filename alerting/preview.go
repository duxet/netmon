package alerting

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/duxet/netmon/storage"
)

// PreviewWindow is how far back a preview of an event rule looks.
const PreviewWindow = 24 * time.Hour

// maxFindings bounds the findings a preview returns.
const maxFindings = 20

// Finding is something a rule finds, in a preview.
type Finding struct {
	Subject string
	// Label is who it is about: a device's name, or "the network".
	Label string
	// Value is what was measured: bytes for traffic and events, seconds
	// for quiet devices.
	Value *float64
	// Fires tells whether the rule would alert about it. Stateful rules also
	// report what they measured that doesn't meet the condition, to help
	// pick a threshold.
	Fires       bool
	Title, Body string
}

// Preview is what a rule would find now.
type Preview struct {
	// Findings are the first maxFindings, those that fire first.
	Findings []Finding
	// Total is the number of findings in all.
	Total int
	// Since is where an event rule's preview starts looking; zero for
	// stateful rules, which look at now.
	Since time.Time
}

// Preview evaluates a rule as if it ran now, without recording or sending
// anything: a stateful rule says what holds now, an event rule what it would
// have found in the last PreviewWindow (without its cooldown).
func (e *Engine) Preview(ctx context.Context, rule storage.RuleRecord) (Preview, error) {
	now := e.opts.Now()
	devices, err := e.devices()
	if err != nil {
		return Preview{}, err
	}

	var since time.Time
	if IsEvent(rule.Type) {
		since = now.Add(-PreviewWindow)
	}
	found, err := e.observe(ctx, rule, devices, now, since, nil)
	if err != nil {
		return Preview{}, err
	}

	// What fires first, then the biggest values: that's what matters for
	// choosing a threshold. Observers already order by value where it is
	// what they measure.
	slices.SortStableFunc(found, func(a, b candidate) int {
		if a.fires != b.fires {
			if a.fires {
				return -1
			}
			return 1
		}
		if a.value != nil && b.value != nil && rule.Type == DeviceInactive {
			return cmp.Compare(*b.value, *a.value)
		}
		return 0
	})

	preview := Preview{Total: len(found), Since: since}
	for _, c := range found[:min(len(found), maxFindings)] {
		preview.Findings = append(preview.Findings, Finding{
			Subject: c.subject, Label: c.label, Value: c.value, Fires: c.fires, Title: c.title, Body: c.body,
		})
	}
	return preview, nil
}
