package common

import (
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"strconv"
)

type ClientID uint32

func (clientID ClientID) Value() int {
	return int(clientID)
}

func ParseClientID(text string) (ClientID, error) {
	value, err := strconv.ParseUint(text, 10, 32)

	if err != nil {
		return ClientID(0), err
	}

	uint32ID := uint32(value)
	return ClientID(uint32ID), nil
}

type MACAddress struct {
	net.HardwareAddr
}

type IPAddress struct {
	*netip.Addr
}

func ParseMACAddress(s string) (*MACAddress, error) {
	hwAddr, err := net.ParseMAC(s)
	if err != nil {
		return nil, err
	}

	return &MACAddress{hwAddr}, nil
}

func ParseIPAddress(s string) (*IPAddress, error) {
	ipAddr, err := netip.ParseAddr(s)
	if err != nil {
		return nil, err
	}

	return &IPAddress{&ipAddr}, nil
}

func (macAddress *MACAddress) Scan(value interface{}) error {
	switch value.(type) {
	case []byte:
		*macAddress = MACAddress{value.([]byte)}
		return nil
	case nil:
		return nil
	}

	return errors.New("invalid MAC address (must be []byte or nil)")
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
