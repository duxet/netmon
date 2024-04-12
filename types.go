package main

import (
	"errors"
	"net/netip"
)

type IpAddress struct {
	*netip.Addr
}

type Flow struct {
	SourceIPAddress      IpAddress
	DestinationIPAddress IpAddress
	IPProto              uint8
	Port                 uint16
	InBytes              uint64
	InPackets            uint64
	OutBytes             uint64
	OutPackets           uint64
}

func (ipAddress *IpAddress) Scan(value interface{}) error {
	switch value.(type) {
	case []byte:
		addr, ok := netip.AddrFromSlice(value.([]byte))

		if !ok {
			return errors.New("unable to parse IP address")
		}

		*ipAddress = IpAddress{&addr}
		return nil
	}

	return errors.New("invalid IP address (must be []byte)")
}
