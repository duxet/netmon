package model

import (
	"encoding/json"
	"errors"
	"net"
	"net/netip"
)

type MACAddress struct {
	net.HardwareAddr
}

type IPAddress struct {
	*netip.Addr
}

type Flow struct {
	SourceMACAddress      MACAddress
	DestinationMACAddress MACAddress
	SourceIPAddress       IPAddress
	DestinationIPAddress  IPAddress
	IPProto               uint8
	Port                  uint16
	InBytes               uint64
	InPackets             uint64
	OutBytes              uint64
	OutPackets            uint64
}

type Client struct {
	SourceIPAddress IPAddress
	InBytes         uint64
	InPackets       uint64
	OutBytes        uint64
	OutPackets      uint64
}

type Stats struct {
	TotalClients uint64
	TotalBytes   *uint64
	TotalPackets *uint64
}

func (macAddress *MACAddress) Scan(value interface{}) error {
	switch value.(type) {
	case []byte:
		*macAddress = MACAddress{value.([]byte)}
		return nil
	}

	return errors.New("invalid IP address (must be []byte)")
}

func (macAddress *MACAddress) MarshalJSON() ([]byte, error) {
	return json.Marshal(macAddress.String())
}

func (ipAddress *IPAddress) Scan(value interface{}) error {
	switch value.(type) {
	case []byte:
		addr, ok := netip.AddrFromSlice(value.([]byte))

		if !ok {
			return errors.New("unable to parse IP address")
		}

		*ipAddress = IPAddress{&addr}
		return nil
	}

	return errors.New("invalid IP address (must be []byte)")
}
