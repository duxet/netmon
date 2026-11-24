package storage

import (
	"errors"
	"testing"
	"time"
)

func TestSessions(t *testing.T) {
	db := openTestDB(t)
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	hash := []byte("hash-of-token-1")

	if _, err := GetSession(db, hash, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSession of an unknown token = %v, want ErrNotFound", err)
	}

	want := SessionRecord{
		Subject:   "user-1",
		Name:      "Anna",
		Email:     "anna@example.com",
		CreatedAt: now,
		ExpiresAt: now.Add(time.Hour),
	}
	if err := CreateSession(db, hash, want); err != nil {
		t.Fatal(err)
	}

	got, err := GetSession(db, hash, now.Add(30*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject != want.Subject || got.Name != want.Name || got.Email != want.Email || !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Errorf("GetSession = %+v, want %+v", got, want)
	}

	// Only the exact token hash finds it.
	if _, err := GetSession(db, []byte("hash-of-token-2"), now); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetSession of another token = %v, want ErrNotFound", err)
	}

	// A session is over at its expiry time, not after it.
	if _, err := GetSession(db, hash, want.ExpiresAt); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetSession at its expiry = %v, want ErrNotFound", err)
	}

	if err := DeleteSession(db, hash); err != nil {
		t.Fatal(err)
	}
	if _, err := GetSession(db, hash, now); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetSession after DeleteSession = %v, want ErrNotFound", err)
	}
	if err := DeleteSession(db, hash); err != nil {
		t.Errorf("deleting a missing session: %v", err)
	}
}

func TestExpireSessions(t *testing.T) {
	db := openTestDB(t)
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

	for hash, expires := range map[string]time.Time{
		"old":     now.Add(-time.Hour),
		"ending":  now,
		"current": now.Add(time.Hour),
	} {
		err := CreateSession(db, []byte(hash), SessionRecord{Subject: hash, CreatedAt: now.Add(-24 * time.Hour), ExpiresAt: expires})
		if err != nil {
			t.Fatal(err)
		}
	}

	n, err := ExpireSessions(db, now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("ExpireSessions = %d, want the 2 that are over", n)
	}

	var remaining []string
	rows, err := db.Query(`SELECT subject FROM sessions`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var subject string
		if err := rows.Scan(&subject); err != nil {
			t.Fatal(err)
		}
		remaining = append(remaining, subject)
	}
	if len(remaining) != 1 || remaining[0] != "current" {
		t.Errorf("remaining sessions = %v, want only the current one", remaining)
	}
}
