package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"fmt"
	"github.com/duxet/netmon/collector"
	"log"

	"github.com/marcboeker/go-duckdb"
	_ "github.com/marcboeker/go-duckdb"
	migrate "github.com/rubenv/sql-migrate"
)

//go:embed migrations/*
var dbMigrations embed.FS

func main() {
	// done := make(chan bool, 1)
	// sigs := make(chan os.Signal, 1)
	// signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	// go func() {
	// 	sig := <-sigs
	// 	fmt.Println(sig)
	// 	done <- true
	// }()

	connector, err := duckdb.NewConnector("flows.db?allow_unsigned_extensions=true", func(execer driver.ExecerContext) error {
		var bootQueries []string

		for _, query := range bootQueries {
			_, err := execer.ExecContext(context.Background(), query, nil)
			if err != nil {
				log.Fatal(err)
			}
		}
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	// db, err := sql.Open("duckdb", "flows.db")
	db := sql.OpenDB(connector)
	defer db.Close()

	migrations := migrate.EmbedFileSystemMigrationSource{
		FileSystem: dbMigrations,
		Root:       "migrations",
	}

	n, err := migrate.Exec(db, "sqlite3", migrations, migrate.Up)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Applied %d migrations!\n", n)

	// connector, err := duckdb.NewConnector("test.db", nil)
	// if err != nil {
	// 	fmt.Println(err)
	// 	return
	// }

	// conn, err := connector.Connect(context.Background())
	// if err != nil {
	// 	fmt.Println(err)
	// 	return
	// }

	// appender, err := NewAppenderFromConn(conn, "", "test")
	// if err != nil {
	// 	fmt.Println(err)
	// 	return
	// }
	// defer appender.Close()

	collector.CollectTraffic(db)
	runHTTPServer(db)
}
