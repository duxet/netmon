package common

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
