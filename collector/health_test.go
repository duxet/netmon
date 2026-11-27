package collector

import (
	"errors"
	"testing"
	"time"
)

func TestHealth(t *testing.T) {
	h := NewHealth()
	if s := h.Status(); s.Running || s.Error != "" {
		t.Errorf("before starting: %+v", s)
	}

	h.Failed(errors.New("operation not permitted"))
	if s := h.Status(); s.Running || s.Error != "operation not permitted" {
		t.Errorf("after failing to start: %+v", s)
	}

	h.Started()
	at := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	h.recorded(at, 12)
	h.roundFailed(errors.New("database is locked"))
	if s := h.Status(); !s.Running || s.Error != "database is locked" || s.Rounds != 2 || s.FailedRounds != 1 || s.Rows != 12 || !s.LastRecorded.Equal(at) {
		t.Errorf("after a round and a failed one: %+v", s)
	}
	h.recorded(at.Add(time.Minute), 3)
	if s := h.Status(); s.Error != "" || s.Rows != 15 {
		t.Errorf("after recovering: %+v", s)
	}

	h.warn("b", "second")
	h.warn("a", "first")
	if s := h.Status(); len(s.Warnings) != 2 || s.Warnings[0] != "first" {
		t.Errorf("warnings = %q, want both, sorted", s.Warnings)
	}
	h.warn("a", "")
	if s := h.Status(); len(s.Warnings) != 1 || s.Warnings[0] != "second" {
		t.Errorf("warnings = %q, want the one left", s.Warnings)
	}

	var none *Health
	none.recorded(at, 1)
	none.warn("a", "b")
	if s := none.Status(); s.Running || s.Rounds != 0 {
		t.Errorf("a nil Health = %+v, want nothing", s)
	}
}
