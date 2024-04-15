//go:build linux

package collector

import (
	"database/sql"
	"fmt"
	"github.com/davecgh/go-spew/spew"
	"github.com/ti-mo/conntrack"
	"github.com/ti-mo/netfilter"
	"log"
)

func CollectTraffic(db *sql.DB) {

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

			srcIp := ev.Flow.TupleOrig.IP.SourceAddress.AsSlice()
			dstIp := ev.Flow.TupleOrig.IP.DestinationAddress.AsSlice()
			ipProto := ev.Flow.TupleOrig.Proto.Protocol
			dstPort := ev.Flow.TupleOrig.Proto.DestinationPort
			inBytes := ev.Flow.CountersReply.Bytes
			inPackets := ev.Flow.CountersReply.Packets
			outBytes := ev.Flow.CountersOrig.Bytes
			outPackets := ev.Flow.CountersOrig.Packets

			_, err = db.Exec(`INSERT INTO flows VALUES (?, ?, ?, ?, ?, ?, ?, ?, current_timestamp)`, srcIp, dstIp, ipProto, dstPort, inBytes, inPackets, outBytes, outPackets)
			if err != nil {
				log.Fatal(err)
			}
		}
	}()

}
