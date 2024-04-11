package main

import (
	"database/sql"
	"embed"
	"fmt"
	"log"

	"github.com/davecgh/go-spew/spew"
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

	db, err := sql.Open("duckdb", "flows.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// _, err = db.Exec(`INSTALL inet; LOAD inet;`)
	// if err != nil {
	// 	log.Fatal(err)
	// }

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
