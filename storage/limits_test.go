package storage

import (
	"testing"
)

func TestLimits(t *testing.T) {
	db := openTestDB(t)
	setting := func(name string) string {
		t.Helper()
		var value string
		if err := db.QueryRow("SELECT current_setting(?)::VARCHAR", name).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	defaults := setting("memory_limit") + " " + setting("threads")

	for _, query := range (Limits{Memory: "256MB", Threads: 1}).Statements() {
		if _, err := db.Exec(query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	if got := setting("memory_limit") + " " + setting("threads"); got != "244.1 MiB 1" {
		t.Errorf("limits = %s, want 244.1 MiB and 1 thread", got)
	}

	for _, query := range (Limits{}).Statements() {
		if _, err := db.Exec(query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	if got := setting("memory_limit") + " " + setting("threads"); got != defaults {
		t.Errorf("limits = %s, want the defaults %s", got, defaults)
	}

	for _, memory := range []string{"1.5GiB", "50%", "512 mb"} {
		if _, err := db.Exec((Limits{Memory: memory}).Statements()[0]); err != nil {
			t.Errorf("memory limit %s: %v", memory, err)
		}
	}
}
