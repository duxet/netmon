//go:build linux

package collector

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"github.com/ti-mo/conntrack"
	"github.com/ti-mo/netfilter"
	"github.com/vishvananda/netlink"
	"log"
	"net"
	"net/netip"
)

func queryMacAddress(ipAddress netip.Addr) (net.HardwareAddr, error) {
	var family int

	log.Printf("Looking for MAC address of: %s\n", ipAddress.String())

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
			log.Printf("%s MAC address is %s", ipAddress, neighbor.HardwareAddr)
			return neighbor.HardwareAddr, nil
		}
	}

	return nil, errors.New("no neighbor found with specified ip address")
}

func CollectTraffic(db *sql.DB) (*Collector, error) {
	shutdownChan := make(chan bool, 1)

	c, err := conntrack.Dial(nil)
	if err != nil {
		return nil, err
	}

	evChan := make(chan conntrack.Event)
	errChan, err := c.Listen(evChan, 1, []netfilter.NetlinkGroup{
		// netfilter.GroupCTNew,
		netfilter.GroupCTDestroy,
	})
	if err != nil {
		return nil, err
	}

	go func() {
		err, ok := <-errChan
		if !ok {
			log.Println("Error while listening for Netfilter events", err)
			return
		}
	}()

	log.Println("Listening for Netfilter events...")

	go func() {
		for {
			ev := <-evChan
			// spew.Dump(ev)
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

	go func() {
		<-shutdownChan
		c.Close()
	}()

	return &Collector{shutdownChan}, nil
}
