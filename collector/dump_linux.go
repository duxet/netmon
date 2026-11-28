//go:build linux

package collector

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"syscall"

	"github.com/mdlayher/netlink"
	"github.com/ti-mo/conntrack"
	"github.com/ti-mo/netfilter"
	"golang.org/x/sys/unix"
)

const (
	// ctGet asks conntrack for connections (IPCTNL_MSG_CT_GET).
	ctGet = 1

	// dumpBuffer is how much of a dump is read at a time. The kernel puts
	// as many connections in as fit, a few hundred.
	dumpBuffer = 64 << 10
)

// dumpConnections returns the connections conntrack tracks, but those being
// destroyed. It reads them a batch at a time and keeps only what netmon needs
// of each, rather than holding conntrack's whole answer and its full records
// at once, as conntrack.Conn.Dump does: on a busy router, with a hundred
// thousand connections, those take over a hundred megabytes for a moment.
func dumpConnections() ([]tracked, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_NETFILTER)
	if err != nil {
		return nil, os.NewSyscallError("socket", err)
	}
	defer unix.Close(fd)

	kernel := &unix.SockaddrNetlink{Family: unix.AF_NETLINK}
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return nil, os.NewSyscallError("bind", err)
	}

	req, err := netfilter.MarshalNetlink(netfilter.Header{
		SubsystemID: netfilter.NFSubsysCTNetlink,
		MessageType: ctGet,
		// Both IPv4 and IPv6.
		Family: netfilter.ProtoUnspec,
		Flags:  netlink.Request | netlink.Dump,
	}, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Length = uint32(unix.NLMSG_HDRLEN + len(req.Data))
	data, err := req.MarshalBinary()
	if err != nil {
		return nil, err
	}
	if err := unix.Sendto(fd, data, 0, kernel); err != nil {
		return nil, os.NewSyscallError("sendto", err)
	}

	var connections []tracked
	buf := make([]byte, dumpBuffer)
	for {
		n, _, flags, _, err := unix.Recvmsg(fd, buf, nil, 0)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return nil, os.NewSyscallError("recvmsg", err)
		}
		if flags&unix.MSG_TRUNC != 0 {
			return nil, errors.New("conntrack dump: message larger than the buffer")
		}

		msgs, err := syscall.ParseNetlinkMessage(buf[:n])
		if err != nil {
			return nil, fmt.Errorf("conntrack dump: %w", err)
		}
		for _, m := range msgs {
			switch m.Header.Type {
			case unix.NLMSG_DONE, unix.NLMSG_ERROR:
				// Both carry an error number, 0 when there is none.
				if len(m.Data) >= 4 {
					if code := int32(binary.NativeEndian.Uint32(m.Data)); code != 0 {
						return nil, fmt.Errorf("conntrack dump: %w", unix.Errno(-code))
					}
				}
				if m.Header.Type == unix.NLMSG_DONE {
					connections, repeated := unique(connections)
					if repeated > 0 {
						slog.Debug("Conntrack's dump returned connections more than once", "connections", repeated)
					}
					return connections, nil
				}
				continue
			}

			var event conntrack.Event
			err := event.Unmarshal(netlink.Message{
				Header: netlink.Header{
					Length:   m.Header.Len,
					Type:     netlink.HeaderType(m.Header.Type),
					Flags:    netlink.HeaderFlags(m.Header.Flags),
					Sequence: m.Header.Seq,
					PID:      m.Header.Pid,
				},
				Data: m.Data,
			})
			if err != nil {
				return nil, fmt.Errorf("conntrack dump: %w", err)
			}
			if event.Flow == nil || event.Flow.Status.Dying() {
				continue
			}
			connections = append(connections, track(*event.Flow))
		}
	}
}
