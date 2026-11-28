package collector

import (
	"maps"
	"slices"
	"sync"
	"time"
)

// Health keeps track of how a collector is doing, for the web UI and the
// metrics. A nil Health ignores everything. It is safe for concurrent use.
type Health struct {
	mu       sync.Mutex
	status   HealthStatus
	warnings map[string]string
}

// HealthStatus is how a collector is doing.
type HealthStatus struct {
	// Running is whether the collector started, and StartedAt when.
	Running   bool
	StartedAt time.Time
	// Error is why the collector didn't start, or why the last round of
	// recording failed; empty when the last one succeeded.
	Error string
	// LastRecorded is when traffic was last written to the database, zero
	// before the first round.
	LastRecorded time.Time
	// Rounds and FailedRounds count the rounds of recording since netmon
	// started, and Rows the flows they wrote.
	Rounds       uint64
	FailedRounds uint64
	Rows         uint64
	// EventOverflows counts the times the kernel dropped events about
	// connections closing, which the Linux collector then misses some of.
	EventOverflows uint64
	// DroppedConnections counts the closed connections the Linux collector
	// left out because it held too many already, while it couldn't read
	// conntrack.
	DroppedConnections uint64
	// DroppedFlows counts the flows whose traffic the Linux collector left
	// out because it held too many already, while writing to the database
	// failed.
	DroppedFlows uint64
	// Warnings are problems that keep traffic from being recorded in full,
	// such as connection tracking not counting bytes.
	Warnings []string
}

// NewHealth returns the health of a collector that hasn't started yet.
func NewHealth() *Health {
	return &Health{warnings: map[string]string{}}
}

// Status returns how the collector is doing.
func (h *Health) Status() HealthStatus {
	if h == nil {
		return HealthStatus{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	status := h.status
	status.Warnings = slices.Sorted(maps.Values(h.warnings))
	return status
}

func (h *Health) update(change func(*HealthStatus)) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	change(&h.status)
}

// Started records that the collector is running.
func (h *Health) Started() {
	h.update(func(s *HealthStatus) { s.Running, s.Error, s.StartedAt = true, "", time.Now() })
}

// Failed records why the collector couldn't start.
func (h *Health) Failed(err error) {
	h.update(func(s *HealthStatus) { s.Running, s.Error = false, err.Error() })
}

// recorded records a round that wrote rows to the database.
func (h *Health) recorded(at time.Time, rows int) {
	h.update(func(s *HealthStatus) {
		s.Error = ""
		s.LastRecorded = at
		s.Rounds++
		s.Rows += uint64(rows)
	})
}

// roundFailed records a round that couldn't record the traffic.
func (h *Health) roundFailed(err error) {
	h.update(func(s *HealthStatus) {
		s.Error = err.Error()
		s.Rounds++
		s.FailedRounds++
	})
}

func (h *Health) eventsOverflowed() {
	h.update(func(s *HealthStatus) { s.EventOverflows++ })
}

func (h *Health) droppedConnections(n int) {
	h.update(func(s *HealthStatus) { s.DroppedConnections += uint64(n) })
}

func (h *Health) droppedFlows(n int) {
	h.update(func(s *HealthStatus) { s.DroppedFlows += uint64(n) })
}

// warn sets the warning about something, or with an empty message takes it
// back.
func (h *Health) warn(about, message string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	if message == "" {
		delete(h.warnings, about)
	} else {
		h.warnings[about] = message
	}
}
