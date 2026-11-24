// Command perf measures how netmon copes with a lot of data: it seeds a
// database with synthetic traffic of a home network and then times the API
// requests the web UI makes, the evaluation of alert rules, and the work the
// collector and compaction do every minute and hour.
//
//	go run ./perf seed -devices 50 -days 365 -out /tmp/large.db
//	go run ./perf run -db /tmp/large.db -geoip GeoLite2-Country.mmdb
//
// See docs/performance.md for the results.
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

func usage() {
	fmt.Fprintln(os.Stderr, "usage: perf seed [flags] | perf run [flags]  (-h for flags)")
	os.Exit(2)
}

func main() {
	log.SetFlags(log.Ltime)
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "seed":
		seedCommand(os.Args[2:])
	case "run":
		runCommand(os.Args[2:])
	case "sql":
		sqlCommand(os.Args[2:])
	default:
		usage()
	}
}

// migrationsDir finds the migrations from the repository root or perf/.
func migrationsDir() string {
	for _, dir := range []string{"migrations", "../migrations"} {
		if _, err := os.Stat(filepath.Join(dir, "001_init.sql")); err == nil {
			return dir
		}
	}
	log.Fatal("run from the repository root: migrations not found")
	return ""
}
