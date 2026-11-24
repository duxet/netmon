package storage

import (
	"errors"
	"testing"
	"time"
)

func TestChannels(t *testing.T) {
	db := openTestDB(t)
	createdAt := time.Date(2026, 11, 24, 18, 0, 0, 0, time.UTC)

	channels, err := ListChannels(db)
	if err != nil || len(channels) != 0 {
		t.Fatalf("channels = %v, %v; want none", channels, err)
	}

	telegram, err := CreateChannel(db, ChannelRecord{
		Name: "Phone", Service: "telegram", URL: "telegram://token@telegram?chats=1", Enabled: true,
	}, createdAt)
	if err != nil {
		t.Fatal(err)
	}
	if telegram.ID == 0 || telegram.Name != "Phone" || !telegram.Enabled ||
		!telegram.CreatedAt.Equal(createdAt) || !telegram.UpdatedAt.Equal(createdAt) {
		t.Errorf("created = %+v", telegram)
	}
	ntfy, err := CreateChannel(db, ChannelRecord{Name: "ntfy", Service: "ntfy", URL: "ntfy://ntfy.sh/topic"}, createdAt)
	if err != nil {
		t.Fatal(err)
	}
	if ntfy.ID == telegram.ID || ntfy.Enabled {
		t.Errorf("second channel = %+v, want its own ID and disabled", ntfy)
	}

	got, err := GetChannel(db, telegram.ID)
	if err != nil || got != telegram {
		t.Errorf("GetChannel = %+v, %v; want %+v", got, err, telegram)
	}

	updatedAt := createdAt.Add(time.Hour)
	telegram.Name = "Anna's phone"
	telegram.Enabled = false
	updated, err := UpdateChannel(db, telegram, updatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Anna's phone" || updated.Enabled || updated.URL != telegram.URL ||
		!updated.CreatedAt.Equal(createdAt) || !updated.UpdatedAt.Equal(updatedAt) {
		t.Errorf("updated = %+v", updated)
	}

	channels, err = ListChannels(db)
	if err != nil || len(channels) != 2 || channels[0] != updated || channels[1] != ntfy {
		t.Errorf("channels = %+v, %v; want both, oldest first", channels, err)
	}

	if err := DeleteChannel(db, ntfy.ID); err != nil {
		t.Fatal(err)
	}
	if channels, _ := ListChannels(db); len(channels) != 1 {
		t.Errorf("channels after delete = %+v", channels)
	}
}

func TestMissingChannels(t *testing.T) {
	db := openTestDB(t)
	now := time.Now()

	if _, err := GetChannel(db, 42); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetChannel = %v, want ErrNotFound", err)
	}
	if _, err := UpdateChannel(db, ChannelRecord{ID: 42, Name: "x", Service: "ntfy", URL: "ntfy://x/y"}, now); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateChannel = %v, want ErrNotFound", err)
	}
	if err := DeleteChannel(db, 42); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteChannel = %v, want ErrNotFound", err)
	}
}
