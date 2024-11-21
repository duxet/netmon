//go:build linux

package collector

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"github.com/duxet/netmon/common"
	"github.com/duxet/netmon/storage"
	"github.com/go-co-op/gocron/v2"
	"github.com/ti-mo/conntrack"
	"github.com/ti-mo/netfilter"
	"github.com/vishvananda/netlink"
	"log"
	"net/netip"
	"slices"
	"sync"
)

type FlowSnapshotKey struct {
	srcIP common.IPAddress
	dstIP common.IPAddress
	proto uint8
	port  uint16
}

type FlowSnapshot struct {
	srcMAC     *common.MACAddress
	dstMAC     *common.MACAddress
	inBytes    uint64
	inPackets  uint64
	outBytes   uint64
	outPackets uint64
}

var closedFlows = map[uint32]conntrack.Flow{}
var continuedFlows = map[uint32]conntrack.Flow{}
var lock = sync.RWMutex{}

func queryMacAddress(ipAddress netip.Addr) (*common.MACAddress, error) {
	var family int

	// log.Printf("Looking for MAC address of: %s\n", ipAddress.String())

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
			// log.Printf("%s MAC address is %s", ipAddress, neighbor.HardwareAddr)
			return &common.MACAddress{HardwareAddr: neighbor.HardwareAddr}, nil
		}
	}

	return nil, errors.New("no neighbor found with specified ip address")
}

func dumpFlows(db *sql.DB) error {
	lock.Lock()
	defer lock.Unlock()

	c, err := conntrack.Dial(nil)
	if err != nil {
		return err
	}

	currentFlows, err := c.Dump(nil)
	if err != nil {
		return err
	}

	var allFlows = map[uint32]conntrack.Flow{}

	for _, flow := range closedFlows {
		allFlows[flow.ID] = flow
	}

	for _, flow := range currentFlows {
		allFlows[flow.ID] = flow
	}

	var snapshots = map[FlowSnapshotKey]*FlowSnapshot{}

	for _, flow := range allFlows {
		var inBytes = flow.CountersReply.Bytes
		var inPackets = flow.CountersReply.Packets
		var outBytes = flow.CountersOrig.Bytes
		var outPackets = flow.CountersOrig.Packets

		if oldFlow, ok := continuedFlows[flow.ID]; ok {
			inBytes = inBytes - oldFlow.CountersReply.Bytes
			inPackets = inPackets - oldFlow.CountersReply.Packets
			outBytes = outBytes - oldFlow.CountersOrig.Bytes
			outPackets = outPackets - oldFlow.CountersOrig.Packets
		}

		key := FlowSnapshotKey{
			srcIP: common.IPAddress{Addr: &flow.TupleOrig.IP.SourceAddress},
			dstIP: common.IPAddress{Addr: &flow.TupleOrig.IP.DestinationAddress},
			proto: flow.TupleOrig.Proto.Protocol,
			port:  flow.TupleOrig.Proto.DestinationPort,
		}

		if snapshot, ok := snapshots[key]; ok {
			snapshots[key].inBytes = inBytes + snapshot.inBytes
			snapshots[key].inPackets = inPackets + snapshot.inPackets
			snapshots[key].outBytes = outBytes + snapshot.outBytes
			snapshots[key].outPackets = outPackets + snapshot.outPackets
		} else {
			srcMAC, _ := queryMacAddress(flow.TupleOrig.IP.SourceAddress)
			dstMAC, _ := queryMacAddress(flow.TupleOrig.IP.DestinationAddress)

			snapshots[key] = &FlowSnapshot{
				srcMAC,
				dstMAC,
				inBytes,
				inPackets,
				outBytes,
				outPackets,
			}
		}
	}

	for key, snapshot := range snapshots {
		saveSnapshot(db, key, *snapshot)
	}

	closedFlows = map[uint32]conntrack.Flow{}
	continuedFlows = map[uint32]conntrack.Flow{}

	return nil
}

func upsertClient(db *sql.DB, mac common.MACAddress, ip common.IPAddress) common.ClientID {
	var client, err = storage.GetClientByMAC(db, mac)

	if err != nil {
		log.Fatal(err)
	}

	if client != nil {
		if ip.IsGlobalUnicast() && !slices.Contains(client.IPAddresses, ip) {
			_, err := db.Exec("UPDATE clients SET ip_addresses = list_append(ip_addresses, ?) WHERE mac_address = ?", ip.AsSlice(), mac.HardwareAddr)
			if err != nil {
				log.Fatal(err)
			}
		}

		return client.ID
	}

	var ipAddress []byte

	if ip.IsGlobalUnicast() {
		ipAddress = ip.AsSlice()
	}

	hostname := common.GetHostname(ip)
	var clientId common.ClientID

	if err := db.QueryRow("INSERT INTO clients (mac_address, ip_addresses, hostname) VALUES (?, [?], ?) RETURNING id", mac.HardwareAddr, ipAddress, hostname).Scan(&clientId); err != nil {
		log.Fatal(err)
	}
	return clientId
}

func saveSnapshot(db *sql.DB, key FlowSnapshotKey, snapshot FlowSnapshot) {
	var mac common.MACAddress
	var ip common.IPAddress

	if snapshot.srcMAC != nil {
		mac = *snapshot.srcMAC
		ip = key.srcIP
	} else if snapshot.dstMAC != nil {
		mac = *snapshot.dstMAC
		ip = key.dstIP
	} else {
		log.Printf("Skipping flow %s -> %s: MAC address not found ", key.srcIP, key.dstIP)
		return
	}

	log.Println("Looking for client")

	clientId := upsertClient(db, mac, ip)

	log.Println("Saving flow")

	_, err := db.Exec(`INSERT INTO flows VALUES (?, ?, ?, ?, ?, ?, ?, ?, current_timestamp)`, clientId, ip.AsSlice(), key.proto, key.port, snapshot.inBytes, snapshot.inPackets, snapshot.outBytes, snapshot.outPackets)
	if err != nil {
		log.Fatal(err)
	}
}

func CollectTraffic(db *sql.DB) (*Collector, error) {
	shutdownChan := make(chan bool, 1)

	s, err := gocron.NewScheduler()
	if err != nil {
		log.Println("Failed to start scheduler:", err)
	}

	_, _ = s.NewJob(
		gocron.CronJob("*/1 * * * *", false),
		gocron.NewTask(func() {
			if err := dumpFlows(db); err != nil {
				log.Println("Failed to process flows:", err)
			}
		}),
	)

	s.Start()

	c, err := conntrack.Dial(nil)
	if err != nil {
		return nil, err
	}

	evChan := make(chan conntrack.Event)
	errChan, err := c.Listen(evChan, 1, []netfilter.NetlinkGroup{
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
			lock.Lock()
			closedFlows[ev.Flow.ID] = *ev.Flow
			lock.Unlock()
		}
	}()

	go func() {
		<-shutdownChan
		c.Close()
	}()

	return &Collector{shutdownChan}, nil
}
