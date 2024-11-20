package common

import (
	"context"
	"net"
)

func GetHostname(ipAddress IPAddress) *string {
	localResolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{}
			return d.DialContext(ctx, "udp", "192.168.1.1:54")
		},
	}
	globalResolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{}
			return d.DialContext(ctx, "udp", "8.8.8.8:53")
		},
	}

	var hostnames []string

	if ipAddress.IsPrivate() {
		hostnames, _ = localResolver.LookupAddr(context.Background(), ipAddress.String())
	} else {
		hostnames, _ = globalResolver.LookupAddr(context.Background(), ipAddress.String())
	}

	if len(hostnames) > 0 {
		return &hostnames[0]
	}

	return nil
}
