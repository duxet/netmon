package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned for a record that doesn't exist.
var ErrNotFound = errors.New("not found")

// ChannelRecord is a notification channel: where notifications go.
type ChannelRecord struct {
	ID   int64
	Name string
	// Service is the scheme of URL, e.g. "telegram".
	Service string
	// URL is the shoutrrr service URL, credentials included.
	URL       string
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

const channelColumns = `id, name, service, url, enabled, created_at, updated_at`

func scanChannel(row interface{ Scan(...any) error }, c *ChannelRecord) error {
	return row.Scan(&c.ID, &c.Name, &c.Service, &c.URL, &c.Enabled, &c.CreatedAt, &c.UpdatedAt)
}

// ListChannels returns all notification channels, oldest first.
func ListChannels(db *sql.DB) ([]ChannelRecord, error) {
	rows, err := db.Query(`SELECT ` + channelColumns + ` FROM notification_channels ORDER BY id`)
	channels, err := collect(rows, err, func(rows *sql.Rows, c *ChannelRecord) error {
		return scanChannel(rows, c)
	})
	if err != nil {
		return nil, fmt.Errorf("query notification channels: %w", err)
	}
	return channels, nil
}

// GetChannel returns the notification channel with the given ID, or
// ErrNotFound.
func GetChannel(db *sql.DB, id int64) (ChannelRecord, error) {
	var c ChannelRecord
	err := scanChannel(db.QueryRow(`SELECT `+channelColumns+` FROM notification_channels WHERE id = ?`, id), &c)
	if errors.Is(err, sql.ErrNoRows) {
		return ChannelRecord{}, fmt.Errorf("notification channel %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return ChannelRecord{}, fmt.Errorf("query notification channel %d: %w", id, err)
	}
	return c, nil
}

// CreateChannel adds a notification channel, created at now, and returns it
// with its ID.
func CreateChannel(db *sql.DB, c ChannelRecord, now time.Time) (ChannelRecord, error) {
	var created ChannelRecord
	err := scanChannel(db.QueryRow(`
		INSERT INTO notification_channels (name, service, url, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		RETURNING `+channelColumns,
		c.Name, c.Service, c.URL, c.Enabled, now, now), &created)
	if err != nil {
		return ChannelRecord{}, fmt.Errorf("create notification channel: %w", err)
	}
	return created, nil
}

// UpdateChannel replaces the name, service, URL and enabled state of the
// channel with c's ID, updated at now, and returns it. It returns
// ErrNotFound when there is no such channel.
func UpdateChannel(db *sql.DB, c ChannelRecord, now time.Time) (ChannelRecord, error) {
	var updated ChannelRecord
	err := scanChannel(db.QueryRow(`
		UPDATE notification_channels
		SET name = ?, service = ?, url = ?, enabled = ?, updated_at = ?
		WHERE id = ?
		RETURNING `+channelColumns,
		c.Name, c.Service, c.URL, c.Enabled, now, c.ID), &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return ChannelRecord{}, fmt.Errorf("notification channel %d: %w", c.ID, ErrNotFound)
	}
	if err != nil {
		return ChannelRecord{}, fmt.Errorf("update notification channel %d: %w", c.ID, err)
	}
	return updated, nil
}

// DeleteChannel removes the notification channel with the given ID, or
// returns ErrNotFound.
func DeleteChannel(db *sql.DB, id int64) error {
	res, err := db.Exec(`DELETE FROM notification_channels WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete notification channel %d: %w", id, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("notification channel %d: %w", id, ErrNotFound)
	}
	return nil
}
