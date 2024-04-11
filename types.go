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

func (ipAddress *IpAddress) Scan(value []uint8) error {
	// // if value is nil, false
	// if value == nil {
	// 	// set the value of the pointer yne to YesNoEnum(false)
	// 	*yne = YesNoEnum(false)
	// 	return nil
	// }
	// if bv, err := driver.Bool.ConvertValue(value); err == nil {
	// 	// if this is a bool type
	// 	if v, ok := bv.(bool); ok {
	// 		// set the value of the pointer yne to YesNoEnum(v)
	// 		*yne = YesNoEnum(v)
	// 		return nil
	// 	}
	// }
	// otherwise, return an error
	return errors.New("failed to scan IpAddress")
}
