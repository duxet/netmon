package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// SessionRecord is a person signed in through single sign-on.
type SessionRecord struct {
	// Subject is the provider's stable identifier of the person.
	Subject string
	// Name and Email are what the provider told when they signed in. Either
	// may be empty.
	Name      string
	Email     string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// CreateSession stores a session under tokenHash, the hash of its secret
// token: the token itself is never stored.
func CreateSession(db *sql.DB, tokenHash []byte, s SessionRecord) error {
	_, err := db.Exec(`
		INSERT INTO sessions (token_hash, subject, name, email, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		tokenHash, s.Subject, s.Name, s.Email, s.CreatedAt, s.ExpiresAt)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// GetSession returns the session stored under tokenHash, or ErrNotFound when
// there is none or it has expired by now.
func GetSession(db *sql.DB, tokenHash []byte, now time.Time) (SessionRecord, error) {
	var s SessionRecord
	err := db.QueryRow(`
		SELECT subject, name, email, created_at, expires_at
		FROM sessions
		WHERE token_hash = ? AND expires_at > ?::TIMESTAMPTZ`,
		tokenHash, now).Scan(&s.Subject, &s.Name, &s.Email, &s.CreatedAt, &s.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionRecord{}, fmt.Errorf("session: %w", ErrNotFound)
	}
	if err != nil {
		return SessionRecord{}, fmt.Errorf("query session: %w", err)
	}
	return s, nil
}

// DeleteSession ends the session stored under tokenHash. A missing session is
// not an error, as signing out twice should work.
func DeleteSession(db *sql.DB, tokenHash []byte) error {
	if _, err := db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, tokenHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// ExpireSessions deletes the sessions that have expired by now and returns how
// many there were.
func ExpireSessions(db *sql.DB, now time.Time) (int64, error) {
	res, err := db.Exec(`DELETE FROM sessions WHERE expires_at <= ?::TIMESTAMPTZ`, now)
	if err != nil {
		return 0, fmt.Errorf("expire sessions: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
