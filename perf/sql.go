package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
)

// sqlCommand runs queries against a database and prints the results, to
// look at what seed made.
func sqlCommand(args []string) {
	fs := flag.NewFlagSet("sql", flag.ExitOnError)
	path := fs.String("db", "flows.db", "database file")
	_ = fs.Parse(args)

	db, err := sql.Open("duckdb", *path+"?access_mode=read_only")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	for _, query := range fs.Args() {
		start := time.Now()
		rows, err := db.Query(query)
		if err != nil {
			log.Fatalf("%s: %v", query, err)
		}
		columns, _ := rows.Columns()
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, strings.Join(columns, "\t"))
		for rows.Next() {
			values := make([]any, len(columns))
			ptrs := make([]any, len(columns))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				log.Fatal(err)
			}
			cells := make([]string, len(values))
			for i, v := range values {
				cells[i] = fmt.Sprint(v)
			}
			fmt.Fprintln(w, strings.Join(cells, "\t"))
		}
		_ = w.Flush()
		rows.Close()
		fmt.Printf("(%.1f ms)\n\n", float64(time.Since(start).Microseconds())/1000)
	}
}
