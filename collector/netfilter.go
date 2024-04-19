//go:build linux

package collector

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"github.com/davecgh/go-spew/spew"
	"github.com/ti-mo/conntrack"
	"github.com/ti-mo/netfilter"
	"github.com/vishvananda/netlink"
	"log"
	"net"
	"net/netip"
)

func queryMacAddress(ipAddress netip.Addr) (net.HardwareAddr, error) {
	var family int

	fmt.Printf("Looking for MAC address of: %s\n", ipAddress.String())

	switch {
	case ipAddress.Is6():
		family = netlink.FAMILY_V6
	default:
		family = netlink.FAMILY_V4
	}

	neighbors, err := netlink.NeighListExecute(netlink.Ndmsg{
		Family: uint8(family),
	})
	if err != nil {
		fmt.Println(err)
		return nil, err
	}

	for _, neighbor := range neighbors {
		if bytes.Equal(neighbor.IP, ipAddress.AsSlice()) {
			return neighbor.HardwareAddr, nil
		}
	}

	return nil, errors.New("no neighbor found with specified ip address")
}

func CollectTraffic(db *sql.DB) {
	c, err := conntrack.Dial(nil)
	if err != nil {
		fmt.Println("Failed to connect to Netfilter", err)
		return
	}

	evChan := make(chan conntrack.Event)
	errChan, err := c.Listen(evChan, 1, []netfilter.NetlinkGroup{
		// netfilter.GroupCTNew,
		netfilter.GroupCTDestroy,
	})
	if err != nil {
		fmt.Println("Failed to subscribe for Netfilter events", err)
		return
	}

	go func() {
		err, ok := <-errChan
		if !ok {
			fmt.Println("Error while listening for Netfilter events", err)
			return
		}
	}()
	defer close(errChan)

	fmt.Println("Listening for events...")

	go func() {
		for {
			ev := <-evChan
			fmt.Println("Received event")
			spew.Dump(ev)
			// spew.Dump(ev.Flow)
			srcMAC, _ := queryMacAddress(ev.Flow.TupleOrig.IP.SourceAddress)
			dstMAC, _ := queryMacAddress(ev.Flow.TupleOrig.IP.DestinationAddress)
			srcIP := ev.Flow.TupleOrig.IP.SourceAddress.AsSlice()
			dstIP := ev.Flow.TupleOrig.IP.DestinationAddress.AsSlice()
			ipProto := ev.Flow.TupleOrig.Proto.Protocol
			dstPort := ev.Flow.TupleOrig.Proto.DestinationPort
			inBytes := ev.Flow.CountersReply.Bytes
			inPackets := ev.Flow.CountersReply.Packets
			outBytes := ev.Flow.CountersOrig.Bytes
			outPackets := ev.Flow.CountersOrig.Packets

			_, err = db.Exec(`INSERT INTO flows VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, current_timestamp)`, srcMAC, dstMAC, srcIP, dstIP, ipProto, dstPort, inBytes, inPackets, outBytes, outPackets)
			if err != nil {
				log.Fatal(err)
			}
		}
	}()
}
