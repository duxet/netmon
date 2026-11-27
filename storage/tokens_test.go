package storage

import (
	"errors"
	"testing"
	"time"
)

func TestTokens(t *testing.T) {
	db := openTestDB(t)
	now := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)

	created, err := CreateToken(db, TokenRecord{Name: "Grafana", Prefix: "netmon_abcd", CreatedBy: "Ada", CreatedAt: now}, []byte("hash"))
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == 0 || created.Name != "Grafana" || created.LastUsedAt != nil {
		t.Errorf("created = %+v", created)
	}

	if _, err := CreateToken(db, TokenRecord{Name: "Again", Prefix: "x", CreatedBy: "Ada", CreatedAt: now}, []byte("hash")); err == nil {
		t.Error("a second token with the same hash was stored")
	}

	if err := TouchToken(db, created.ID, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	found, err := GetTokenByHash(db, []byte("hash"))
	if err != nil || found.ID != created.ID || found.LastUsedAt == nil || !found.LastUsedAt.Equal(now.Add(time.Hour)) {
		t.Errorf("found %+v (%v), want the token, used an hour later", found, err)
	}
	if _, err := GetTokenByHash(db, []byte("other")); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown hash: %v, want ErrNotFound", err)
	}

	if tokens, err := ListTokens(db); err != nil || len(tokens) != 1 {
		t.Errorf("tokens = %+v (%v)", tokens, err)
	}
	if err := DeleteToken(db, created.ID); err != nil {
		t.Fatal(err)
	}
	if err := DeleteToken(db, created.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting twice: %v, want ErrNotFound", err)
	}
	if _, err := GetTokenByHash(db, []byte("hash")); !errors.Is(err, ErrNotFound) {
		t.Errorf("a revoked token: %v, want ErrNotFound", err)
	}
}
