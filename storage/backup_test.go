package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	migrate "github.com/rubenv/sql-migrate"
)

func TestBackup(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("duckdb", filepath.Join(dir, "flows.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := migrate.Exec(db, "sqlite3", &migrate.FileMigrationSource{Dir: "../migrations"}, migrate.Up); err != nil {
		t.Fatal(err)
	}
	seed(t, db)
	if _, err := Compact(db, seedAt.Add(3*time.Hour), 0, 0); err != nil {
		t.Fatal(err)
	}

	backup := filepath.Join(dir, "backup.db")
	if err := Backup(context.Background(), db, backup); err != nil {
		t.Fatal(err)
	}
	// netmon carries on with its database.
	insert(t, db, row{mustMAC(t, laptopMAC), nil, mustIP("192.168.1.23"), mustIP("1.1.1.1"), 17, 53, 90, 70, seedAt.Add(4 * time.Hour)})

	copied, err := sql.Open("duckdb", backup)
	if err != nil {
		t.Fatal(err)
	}
	defer copied.Close()
	stats, err := GetStats(copied, FlowsFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalBytes == nil || *stats.TotalBytes != 3670 {
		t.Errorf("backup has %v bytes, want the 3670 there were when it was made", stats.TotalBytes)
	}
	var migrations int
	if err := copied.QueryRow("SELECT count(*) FROM gorp_migrations").Scan(&migrations); err != nil || migrations == 0 {
		t.Errorf("backup has %d migrations (%v), want them recorded so netmon can open it", migrations, err)
	}

	if err := Backup(context.Background(), db, backup); err == nil {
		t.Error("backing up over an existing database file worked, want an error")
	}
}
