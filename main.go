package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"github.com/duxet/netmon/api"
	"github.com/duxet/netmon/collector"
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

//go:embed all:client/dist/*
var clientAssets embed.FS

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	connector, err := duckdb.NewConnector("flows.db", func(execer driver.ExecerContext) error {
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
	log.Printf("Applied %d migrations", n)

	coll, err := collector.CollectTraffic(db)
	if err != nil {
		log.Println("Collector failed to start:", err)
	}

	app := api.CreateHTTPApp(db, clientAssets)

	go func() {
		<-ctx.Done()
		log.Println("Shutting down gracefully")

		if coll != nil {
			coll.Shutdown()
		}

		_ = app.ShutdownWithContext(context.Background())
	}()

	log.Fatal(app.Listen(":2137"))
}
