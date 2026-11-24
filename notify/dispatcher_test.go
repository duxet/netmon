package notify

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/duxet/netmon/storage"
	migrate "github.com/rubenv/sql-migrate"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrate.Exec(db, "sqlite3", &migrate.FileMigrationSource{Dir: "../migrations"}, migrate.Up); err != nil {
		t.Fatal(err)
	}
	return db
}

func addChannel(t *testing.T, db *sql.DB, name, url string, enabled bool) storage.ChannelRecord {
	t.Helper()
	service, err := Inspect(url)
	if err != nil {
		t.Fatal(err)
	}
	c, err := storage.CreateChannel(db, storage.ChannelRecord{Name: name, Service: service.Scheme, URL: url, Enabled: enabled}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// fakeSend records messages, failing the first failures[url] sends to a URL
// (or every send, for -1).
type fakeSend struct {
	mu       sync.Mutex
	sent     map[string][]Message
	calls    map[string]int
	failures map[string]int
}

func newFakeSend() *fakeSend {
	return &fakeSend{sent: map[string][]Message{}, calls: map[string]int{}, failures: map[string]int{}}
}

func (f *fakeSend) send(ctx context.Context, url string, msg Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[url]++
	if fail := f.failures[url]; fail < 0 || f.calls[url] <= fail {
		return fmt.Errorf("service unavailable (attempt %d)", f.calls[url])
	}
	f.sent[url] = append(f.sent[url], msg)
	return nil
}

func (f *fakeSend) messages(url string) []Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Message(nil), f.sent[url]...)
}

func deliveries(t *testing.T, db *sql.DB, alertIDs ...int64) map[int64][]storage.DeliveryRecord {
	t.Helper()
	d, err := storage.Deliveries(db, alertIDs)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func notification(alertID int64, title string, channels ...int64) Notification {
	return Notification{
		AlertID:    alertID,
		Kind:       "firing",
		Message:    Message{Title: title, Body: title + " happened.", Severity: Warning},
		ChannelIDs: channels,
	}
}

const (
	phoneURL   = "ntfy://ntfy.sh/phone"
	webhookURL = "generic://example.com/hook?template=json"
)

func TestDispatch(t *testing.T) {
	db := openTestDB(t)
	phone := addChannel(t, db, "phone", phoneURL, true)
	off := addChannel(t, db, "off", "ntfy://ntfy.sh/off", false)
	fake := newFakeSend()
	d := NewDispatcher(db, DispatcherOptions{Send: fake.send})

	d.Dispatch([]Notification{notification(1, "Large download", phone.ID, off.ID, 999)})
	d.Close(time.Second)

	if got := fake.messages(phoneURL); len(got) != 1 || got[0].Title != "Large download" {
		t.Errorf("phone got %+v, want the message", got)
	}
	// Disabled and deleted channels get nothing, and there is nothing to record.
	if fake.calls["ntfy://ntfy.sh/off"] != 0 {
		t.Error("the disabled channel was sent to")
	}
	got := deliveries(t, db, 1)[1]
	if len(got) != 1 || got[0].ChannelID != phone.ID || got[0].Status != Sent || got[0].Attempts != 1 || got[0].Kind != "firing" {
		t.Errorf("deliveries = %+v, want one sent to the phone", got)
	}

	// After Close, notifications are recorded as failed.
	d.Dispatch([]Notification{notification(2, "Late", phone.ID)})
	if got := deliveries(t, db, 2)[2]; len(got) != 1 || got[0].Status != Failed {
		t.Errorf("deliveries after Close = %+v, want one failed", got)
	}
}

func TestDispatchRetries(t *testing.T) {
	db := openTestDB(t)
	flaky := addChannel(t, db, "flaky", "ntfy://ntfy.sh/flaky", true)
	down := addChannel(t, db, "down", "ntfy://ntfy.sh/down", true)
	fake := newFakeSend()
	fake.failures["ntfy://ntfy.sh/flaky"] = 2
	fake.failures["ntfy://ntfy.sh/down"] = -1
	d := NewDispatcher(db, DispatcherOptions{Send: fake.send, Retries: []time.Duration{time.Millisecond, time.Millisecond}})

	d.Dispatch([]Notification{notification(1, "Disk full", flaky.ID), notification(2, "Disk full", down.ID)})
	d.Close(time.Second)

	byAlert := deliveries(t, db, 1, 2)
	if got := byAlert[1]; len(got) != 1 || got[0].Status != Sent || got[0].Attempts != 3 {
		t.Errorf("flaky channel = %+v, want sent at the third attempt", got)
	}
	if got := byAlert[2]; len(got) != 1 || got[0].Status != Failed || got[0].Attempts != 3 ||
		got[0].Error == nil || !strings.Contains(*got[0].Error, "service unavailable") {
		t.Errorf("down channel = %+v, want failed after 3 attempts with the reason", got)
	}

	// Invalid URLs aren't retried.
	broken := addChannel(t, db, "broken", "ntfy://ntfy.sh/broken", true)
	calls := 0
	d = NewDispatcher(db, DispatcherOptions{
		Retries: []time.Duration{time.Millisecond},
		Send: func(context.Context, string, Message) error {
			calls++
			return fmt.Errorf("%w: nope", ErrInvalidURL)
		},
	})
	d.Dispatch([]Notification{notification(3, "x", broken.ID)})
	d.Close(time.Second)
	if got := deliveries(t, db, 3)[3]; calls != 1 || len(got) != 1 || got[0].Status != Failed || got[0].Attempts != 1 {
		t.Errorf("invalid URL: %d calls, deliveries %+v; want one failed attempt", calls, got)
	}
}

// Many alerts at once reach a phone as one message, but a webhook as many.
func TestDispatchGroups(t *testing.T) {
	db := openTestDB(t)
	phone := addChannel(t, db, "phone", phoneURL, true)
	webhook := addChannel(t, db, "webhook", webhookURL, true)
	fake := newFakeSend()
	d := NewDispatcher(db, DispatcherOptions{Send: fake.send})

	var batch []Notification
	for i := range 5 {
		n := notification(int64(i+1), fmt.Sprintf("New device %d", i+1), phone.ID, webhook.ID)
		if i == 2 {
			n.Message.Severity = Critical
		}
		batch = append(batch, n)
	}
	d.Dispatch(batch)
	d.Close(time.Second)

	got := fake.messages(phoneURL)
	if len(got) != 1 || got[0].Title != "5 alerts from netmon" || got[0].Severity != Critical ||
		!strings.Contains(got[0].Body, "• New device 1\n• New device 2") {
		t.Errorf("phone got %+v, want one message listing all five, critical", got)
	}
	if got := fake.messages(webhookURL); len(got) != 5 {
		t.Errorf("webhook got %d messages, want 5", len(got))
	}
	for id, records := range deliveries(t, db, 1, 2, 3, 4, 5) {
		if len(records) != 2 {
			t.Errorf("alert %d has %d deliveries, want one per channel", id, len(records))
		}
	}
}

func TestDispatchRateLimit(t *testing.T) {
	db := openTestDB(t)
	phone := addChannel(t, db, "phone", phoneURL, true)
	fake := newFakeSend()

	var mu sync.Mutex
	now := time.Date(2026, 11, 24, 18, 0, 0, 0, time.UTC)
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	d := NewDispatcher(db, DispatcherOptions{
		Send: fake.send, Now: clock, Workers: 1, RateLimit: 2, RateWindow: time.Hour, GroupAbove: 10,
	})
	defer d.Close(time.Second)

	d.Dispatch([]Notification{
		notification(1, "a", phone.ID), notification(2, "b", phone.ID),
		notification(3, "c", phone.ID), notification(4, "d", phone.ID),
	})
	waitFor(t, func() bool { return len(deliveries(t, db, 1, 2, 3, 4)) == 4 })

	statuses := map[string]int{}
	for _, records := range deliveries(t, db, 1, 2, 3, 4) {
		statuses[records[0].Status]++
	}
	if statuses[Sent] != 2 || statuses[Suppressed] != 2 {
		t.Errorf("statuses = %v, want 2 sent and 2 suppressed", statuses)
	}

	// Once the window has passed, the next message says what was held back.
	mu.Lock()
	now = now.Add(time.Hour)
	mu.Unlock()
	d.Dispatch([]Notification{notification(5, "e", phone.ID)})
	waitFor(t, func() bool { return len(fake.messages(phoneURL)) == 3 })
	if last := fake.messages(phoneURL)[2]; !strings.Contains(last.Body, "held back 2 other notifications") {
		t.Errorf("message after the limit = %q, want the held back count", last.Body)
	}
}

// Messages still queued when the dispatcher closes are recorded as failed.
func TestCloseGivesUp(t *testing.T) {
	db := openTestDB(t)
	phone := addChannel(t, db, "phone", phoneURL, true)
	d := NewDispatcher(db, DispatcherOptions{
		Workers: 1,
		Send: func(ctx context.Context, _ string, _ Message) error {
			<-ctx.Done() // a service that never answers
			return errors.New("timed out")
		},
	})

	d.Dispatch([]Notification{notification(1, "a", phone.ID), notification(2, "b", phone.ID)})
	start := time.Now()
	d.Close(50 * time.Millisecond)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Close took %v", elapsed)
	}

	for id, records := range deliveries(t, db, 1, 2) {
		if len(records) != 1 || records[0].Status != Failed {
			t.Errorf("alert %d deliveries = %+v, want failed", id, records)
		}
	}
	if n := len(deliveries(t, db, 1, 2)); n != 2 {
		t.Errorf("%d alerts recorded, want both", n)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
