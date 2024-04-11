package main

type Flow struct {
	SourceIPAddress      string
	DestinationIPAddress string
	IPProto              uint8
	Port                 uint16
	InBytes              uint64
	InPackets            uint64
	OutBytes             uint64
	OutPackets           uint64
}
