package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// TokenRecord is a read-only API token. The token itself isn't stored, only
// its hash.
type TokenRecord struct {
	ID   int64
	Name string
	// Prefix is the token's start, to tell tokens apart.
	Prefix string
	// CreatedBy is who made it.
	CreatedBy  string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

const tokenColumns = `id, name, prefix, created_by, created_at, last_used_at`

func scanToken(row interface{ Scan(...any) error }, t *TokenRecord) error {
	var lastUsed sql.NullTime
	if err := row.Scan(&t.ID, &t.Name, &t.Prefix, &t.CreatedBy, &t.CreatedAt, &lastUsed); err != nil {
		return err
	}
	if lastUsed.Valid {
		t.LastUsedAt = &lastUsed.Time
	}
	return nil
}

// CreateToken stores a token under the hash of its secret.
func CreateToken(db *sql.DB, t TokenRecord, tokenHash []byte) (TokenRecord, error) {
	row := db.QueryRow(`
		INSERT INTO api_tokens (name, token_hash, prefix, created_by, created_at)
		VALUES (?, ?, ?, ?, ?)
		RETURNING `+tokenColumns,
		t.Name, tokenHash, t.Prefix, t.CreatedBy, t.CreatedAt)
	var created TokenRecord
	if err := scanToken(row, &created); err != nil {
		return TokenRecord{}, fmt.Errorf("create token: %w", err)
	}
	return created, nil
}

// ListTokens returns every token, newest first.
func ListTokens(db *sql.DB) ([]TokenRecord, error) {
	rows, err := db.Query(`SELECT ` + tokenColumns + ` FROM api_tokens ORDER BY created_at DESC, id DESC`)
	tokens, err := collect(rows, err, func(rows *sql.Rows, t *TokenRecord) error { return scanToken(rows, t) })
	if err != nil {
		return nil, fmt.Errorf("query tokens: %w", err)
	}
	return tokens, nil
}

// GetTokenByHash returns the token stored under tokenHash, or ErrNotFound.
func GetTokenByHash(db *sql.DB, tokenHash []byte) (TokenRecord, error) {
	var t TokenRecord
	err := scanToken(db.QueryRow(`SELECT `+tokenColumns+` FROM api_tokens WHERE token_hash = ?`, tokenHash), &t)
	if errors.Is(err, sql.ErrNoRows) {
		return TokenRecord{}, fmt.Errorf("token: %w", ErrNotFound)
	}
	if err != nil {
		return TokenRecord{}, fmt.Errorf("query token: %w", err)
	}
	return t, nil
}

// TouchToken records that a token was used at a time.
func TouchToken(db *sql.DB, id int64, at time.Time) error {
	if _, err := db.Exec(`UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, at, id); err != nil {
		return fmt.Errorf("touch token: %w", err)
	}
	return nil
}

// DeleteToken revokes a token, or answers ErrNotFound when there is none.
func DeleteToken(db *sql.DB, id int64) error {
	res, err := db.Exec(`DELETE FROM api_tokens WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete token: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("token %d: %w", id, ErrNotFound)
	}
	return nil
}
