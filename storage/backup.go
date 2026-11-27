package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
)

// Backup writes a copy of the whole database to a new DuckDB file at path,
// as it is when the copy starts, while netmon goes on using it. netmon can
// open the copy as its database.
func Backup(ctx context.Context, db *sql.DB, path string) error {
	// Attaching is done on one connection, though it is the whole database's.
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("back up database: %w", err)
	}
	defer conn.Close()

	var current string
	if err := conn.QueryRowContext(ctx, "SELECT current_database()").Scan(&current); err != nil {
		return fmt.Errorf("back up database: %w", err)
	}

	suffix := make([]byte, 8)
	_, _ = rand.Read(suffix)
	alias := "backup_" + hex.EncodeToString(suffix)

	quoted := "'" + strings.ReplaceAll(path, "'", "''") + "'"
	if _, err := conn.ExecContext(ctx, "ATTACH "+quoted+" AS "+alias); err != nil {
		return fmt.Errorf("back up database: %w", err)
	}
	_, err = conn.ExecContext(ctx, `COPY FROM DATABASE "`+strings.ReplaceAll(current, `"`, `""`)+`" TO `+alias)
	if _, detachErr := conn.ExecContext(context.Background(), "DETACH "+alias); err == nil && detachErr != nil {
		err = detachErr
	}
	if err != nil {
		return fmt.Errorf("back up database: %w", err)
	}
	return nil
}
