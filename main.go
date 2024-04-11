package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"fmt"
	"log"

	"github.com/davecgh/go-spew/spew"
	"github.com/marcboeker/go-duckdb"
	_ "github.com/marcboeker/go-duckdb"
	migrate "github.com/rubenv/sql-migrate"
	"github.com/ti-mo/conntrack"
	"github.com/ti-mo/netfilter"
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
		bootQueries := []string{
			"INSTALL 'inet'",
			"LOAD 'inet'",
		}

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

	c, err := conntrack.Dial(nil)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer c.Close()

	evChan := make(chan conntrack.Event)
	errChan, err := c.Listen(evChan, 1, []netfilter.NetlinkGroup{
		// netfilter.GroupCTNew,
		netfilter.GroupCTDestroy,
	})
	if err != nil {
		fmt.Println(err)
		return
	}

	go func() {
		err, ok := <-errChan
		if !ok {
			fmt.Println(err)
			return
		}
	}()
	defer close(errChan)

	fmt.Println("Listening for events...")

	go func() {
		for {
			ev := <-evChan
			spew.Dump(ev)
			// spew.Dump(ev.Flow)

			srcIp := ev.Flow.TupleOrig.IP.SourceAddress.String()
			dstIp := ev.Flow.TupleOrig.IP.DestinationAddress.String()
			ipProto := ev.Flow.TupleOrig.Proto.Protocol
			dstPort := ev.Flow.TupleOrig.Proto.DestinationPort
			inBytes := ev.Flow.CountersReply.Bytes
			inPackets := ev.Flow.CountersReply.Packets
			outBytes := ev.Flow.CountersOrig.Bytes
			outPackets := ev.Flow.CountersOrig.Packets

			_, err = db.Exec(`INSERT INTO flows VALUES (?::INET, ?::INET, ?, ?, ?, ?, ?, ?, current_timestamp)`, srcIp, dstIp, ipProto, dstPort, inBytes, inPackets, outBytes, outPackets)
			if err != nil {
				log.Fatal(err)
			}
		}
	}()

	runHTTPServer(db)
}
