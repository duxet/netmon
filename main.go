package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"fmt"
	"github.com/duxet/netmon/collector"
	"golang.org/x/sync/errgroup"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/marcboeker/go-duckdb"
	_ "github.com/marcboeker/go-duckdb"
	migrate "github.com/rubenv/sql-migrate"
)

//go:embed migrations/*
var dbMigrations embed.FS

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	g, gCtx := errgroup.WithContext(ctx)

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

	coll, _ := collector.CollectTraffic(db)
	app := runHTTPServer(db)

	g.Go(func() error {
		<-gCtx.Done()

		log.Println("Shutting down gracefully")

		coll.Shutdown()
		return app.ShutdownWithContext(context.Background())
	})

	if err := g.Wait(); err != nil {
		fmt.Printf("Exit reason: %s \n", err)
	}
}
