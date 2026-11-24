package collector

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os/exec"
	"strings"
)

// defaultRouteInterface returns the name of the interface the default route
// goes through, as reported by route(8) on BSD and macOS.
func defaultRouteInterface(ctx context.Context) (string, error) {
	var errs []error

	for _, args := range [][]string{
		{"-n", "get", "default"},
		{"-n", "get", "-inet6", "default"},
	} {
		out, err := exec.CommandContext(ctx, "/sbin/route", args...).Output()
		if err != nil {
			// Output captures what route complained about, such as "not in table".
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
				err = fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
			}

			errs = append(errs, err)
			continue
		}

		if name := parseRouteInterface(string(out)); name != "" {
			return name, nil
		}
	}

	if len(errs) == 0 {
		errs = append(errs, errors.New("route did not report an interface"))
	}

	return "", fmt.Errorf("no default route: %w", errors.Join(errs...))
}

// parseRouteInterface finds the interface in the output of `route get`:
//
//	   route to: default
//	destination: default
//	    gateway: 192.168.1.1
//	  interface: en0
func parseRouteInterface(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if name, ok := strings.CutPrefix(strings.TrimSpace(line), "interface:"); ok {
			return strings.TrimSpace(name)
		}
	}

	return ""
}

// localPrefixes returns the networks the interface is directly attached to.
func localPrefixes(name string) ([]netip.Prefix, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return nil, err
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return nil, err
	}

	var prefixes []netip.Prefix

	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}

		ip, ok := netip.AddrFromSlice(ipNet.IP)
		if !ok {
			continue
		}

		// IPv4 addresses may come in their 16 byte form, but the mask only
		// has 4 bytes then, and packets carry plain IPv4 addresses.
		ones, bits := ipNet.Mask.Size()
		if bits == 8*net.IPv4len {
			ip = ip.Unmap()
		}

		prefixes = append(prefixes, netip.PrefixFrom(ip, ones).Masked())
	}

	return prefixes, nil
}
