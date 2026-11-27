package notify

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/duxet/netmon/storage"
)

// Delivery statuses, as recorded in the delivery log.
const (
	Sent       = "sent"
	Failed     = "failed"
	Suppressed = "suppressed"
)

// Notification is a message about an alert, for the channels of its rule.
type Notification struct {
	AlertID int64
	// Kind is firing, reminder or resolved.
	Kind       string
	Message    Message
	ChannelIDs []int64
}

// DispatcherOptions tunes a Dispatcher; zero values pick the defaults.
type DispatcherOptions struct {
	// Workers send messages in parallel, so a slow service doesn't hold up
	// the others. Default 4.
	Workers int
	// QueueSize is how many messages can wait. Default 256.
	QueueSize int
	// Retries are the waits before each retry of a failed send. Default:
	// two retries, after 5 and 30 seconds.
	Retries []time.Duration
	// RateLimit is how many messages a channel gets per RateWindow at most;
	// more are not sent, and the next message says how many were held back.
	// Defaults 20 and 5 minutes.
	RateLimit  int
	RateWindow time.Duration
	// GroupAbove is how many notifications for one channel from one
	// evaluation are sent as they are; more are combined into one message.
	// Webhooks get every one. Default 3.
	GroupAbove int
	// Send sends one message; tests replace it. Default Send.
	Send func(ctx context.Context, url string, msg Message) error
	// Now tells the time; tests replace it. Default time.Now.
	Now func() time.Time
}

func (o DispatcherOptions) withDefaults() DispatcherOptions {
	if o.Workers <= 0 {
		o.Workers = 4
	}
	if o.QueueSize <= 0 {
		o.QueueSize = 256
	}
	if o.Retries == nil {
		o.Retries = []time.Duration{5 * time.Second, 30 * time.Second}
	}
	if o.RateLimit <= 0 {
		o.RateLimit = 20
	}
	if o.RateWindow <= 0 {
		o.RateWindow = 5 * time.Minute
	}
	if o.GroupAbove <= 0 {
		o.GroupAbove = 3
	}
	if o.Send == nil {
		o.Send = Send
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

// alertRef is one alert a queued message is about.
type alertRef struct {
	id   int64
	kind string
}

type job struct {
	channel storage.ChannelRecord
	message Message
	alerts  []alertRef
}

// Dispatcher sends notifications about alerts in the background, retrying
// failed sends and recording every delivery in the database. Messages wait
// in memory: those still queued when netmon stops are not sent, but recorded
// as failed.
type Dispatcher struct {
	db   *sql.DB
	opts DispatcherOptions

	queue  chan job
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu     sync.Mutex
	closed bool
	// sends are the times messages went to each channel in the rate window.
	sends map[int64][]time.Time
	// held counts the messages not sent to each channel because of the
	// rate limit since the last one that was.
	held map[int64]int
	// deliveries counts the deliveries recorded since the dispatcher
	// started, by status.
	deliveries map[string]uint64
}

// NewDispatcher starts a dispatcher's workers. Close stops them.
func NewDispatcher(db *sql.DB, opts DispatcherOptions) *Dispatcher {
	opts = opts.withDefaults()
	ctx, cancel := context.WithCancel(context.Background())
	d := &Dispatcher{
		db:         db,
		opts:       opts,
		queue:      make(chan job, opts.QueueSize),
		ctx:        ctx,
		cancel:     cancel,
		sends:      map[int64][]time.Time{},
		held:       map[int64]int{},
		deliveries: map[string]uint64{},
	}

	d.wg.Add(opts.Workers)
	for range opts.Workers {
		go func() {
			defer d.wg.Done()
			for j := range d.queue {
				d.deliver(j)
			}
		}()
	}
	return d
}

// Dispatch queues the notifications of one evaluation. Disabled and deleted
// channels are skipped. It doesn't block: when the queue is full, the
// notifications are recorded as failed.
func (d *Dispatcher) Dispatch(notifications []Notification) {
	if len(notifications) == 0 {
		return
	}

	channels, err := storage.ListChannels(d.db)
	if err != nil {
		slog.Error("Failed to load notification channels", "err", err)
		return
	}

	byChannel := map[int64][]Notification{}
	for _, n := range notifications {
		for _, id := range n.ChannelIDs {
			byChannel[id] = append(byChannel[id], n)
		}
	}

	for _, channel := range channels {
		pending := byChannel[channel.ID]
		if len(pending) == 0 || !channel.Enabled {
			continue
		}

		// People get one message instead of a burst; webhooks get each one.
		if len(pending) > d.opts.GroupAbove && channel.Service != webhookScheme {
			d.enqueue(job{channel: channel, message: combine(pending), alerts: refs(pending)})
			continue
		}
		for _, n := range pending {
			d.enqueue(job{channel: channel, message: n.Message, alerts: refs([]Notification{n})})
		}
	}
}

func refs(notifications []Notification) []alertRef {
	alerts := make([]alertRef, 0, len(notifications))
	for _, n := range notifications {
		alerts = append(alerts, alertRef{id: n.AlertID, kind: n.Kind})
	}
	return alerts
}

// combine turns several notifications into one message listing them, as
// urgent as the most urgent of them.
func combine(notifications []Notification) Message {
	var body strings.Builder
	severity := Info
	for i, n := range notifications {
		if i > 0 {
			body.WriteString("\n")
		}
		body.WriteString("• " + n.Message.Title)
		if severityRank(n.Message.Severity) > severityRank(severity) {
			severity = n.Message.Severity
		}
	}

	return Message{
		Title:    fmt.Sprintf("%d alerts from netmon", len(notifications)),
		Body:     body.String(),
		Severity: severity,
	}
}

func severityRank(s Severity) int {
	return slices.Index([]Severity{Info, Warning, Critical}, s)
}

func (d *Dispatcher) enqueue(j job) {
	d.mu.Lock()
	closed := d.closed
	if !closed {
		select {
		case d.queue <- j:
			d.mu.Unlock()
			return
		default:
		}
	}
	d.mu.Unlock()

	reason := "the notification queue is full"
	if closed {
		reason = "netmon was shutting down"
	}
	slog.Info("Not sending to notification channel", "channel", j.channel.ID, "reason", reason)
	d.record(j, Failed, 0, reason)
}

// allow reports whether a message may go to a channel now under the rate
// limit, and if so counts it.
func (d *Dispatcher) allow(channelID int64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := d.opts.Now()
	recent := slices.DeleteFunc(d.sends[channelID], func(t time.Time) bool {
		return now.Sub(t) >= d.opts.RateWindow
	})
	if len(recent) >= d.opts.RateLimit {
		d.sends[channelID] = recent
		return false
	}
	d.sends[channelID] = append(recent, now)
	return true
}

// holdBack counts notifications not sent to a channel for the rate limit.
func (d *Dispatcher) holdBack(channelID int64, n int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.held[channelID] += n
}

// takeHeld returns, and resets, the number of notifications held back from
// a channel.
func (d *Dispatcher) takeHeld(channelID int64) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := d.held[channelID]
	delete(d.held, channelID)
	return n
}

func (d *Dispatcher) deliver(j job) {
	if d.ctx.Err() != nil {
		d.record(j, Failed, 0, "netmon was shutting down")
		return
	}
	if !d.allow(j.channel.ID) {
		d.holdBack(j.channel.ID, len(j.alerts))
		d.record(j, Suppressed, 0, "")
		return
	}

	msg := j.message
	if held := d.takeHeld(j.channel.ID); held > 0 {
		msg.Body += fmt.Sprintf("\n\n(netmon held back %d other notifications to this channel in the last few minutes, to stay under its rate limit.)", held)
	}

	attempts := 0
	var err error
	for {
		attempts++
		if err = d.opts.Send(d.ctx, j.channel.URL, msg); err == nil {
			break
		}
		if errors.Is(err, ErrInvalidURL) || attempts > len(d.opts.Retries) {
			break
		}
		select {
		case <-time.After(d.opts.Retries[attempts-1]):
		case <-d.ctx.Done():
		}
		if d.ctx.Err() != nil {
			break
		}
	}

	if err != nil {
		slog.Error("Failed to notify channel", "channel", j.channel.ID, "service", j.channel.Service, "attempts", attempts, "err", err)
		d.record(j, Failed, attempts, err.Error())
		return
	}
	d.record(j, Sent, attempts, "")
}

// Deliveries counts the deliveries recorded since the dispatcher started, one
// per alert and channel, by status: sent, failed or suppressed.
func (d *Dispatcher) Deliveries() map[string]uint64 {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	counts := make(map[string]uint64, len(d.deliveries))
	for status, n := range d.deliveries {
		counts[status] = n
	}
	return counts
}

// record writes one delivery per alert of a job.
func (d *Dispatcher) record(j job, status string, attempts int, reason string) {
	d.mu.Lock()
	d.deliveries[status] += uint64(len(j.alerts))
	d.mu.Unlock()

	var errText *string
	if reason != "" {
		errText = &reason
	}
	now := d.opts.Now()

	for _, alert := range j.alerts {
		err := storage.InsertDelivery(d.db, storage.DeliveryRecord{
			AlertID:   alert.id,
			ChannelID: j.channel.ID,
			Kind:      alert.kind,
			Status:    status,
			Attempts:  attempts,
			Error:     errText,
			CreatedAt: now,
		})
		if err != nil {
			slog.Error("Failed to record a notification", "err", err)
		}
	}
}

// Close stops taking notifications and waits up to timeout for the queued
// ones to be sent. Those left are recorded as failed.
func (d *Dispatcher) Close(timeout time.Duration) {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.closed = true
	close(d.queue)
	d.mu.Unlock()

	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(timeout):
		// Stop retrying and sending; the workers record what is left.
		d.cancel()
		<-done
	}
	d.cancel()
}
