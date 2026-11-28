// loadgen makes many short connections through a simulated router, for
// load testing netmon's Linux collector.
//
//	loadgen server -servers 203.0.113.2-65
//	loadgen client -servers 203.0.113.2-65 -fin 500 -rst 200 -udp 1000 -duration 2m
package main

import (
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	portFIN = 8001 // TCP, closed normally
	portRST = 8002 // TCP, closed with a reset
	portUDP = 8003 // one datagram each way and more
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: loadgen server|client [flags]")
	}
	switch os.Args[1] {
	case "server":
		server(os.Args[2:])
	case "client":
		client(os.Args[2:])
	default:
		log.Fatalf("unknown command %q", os.Args[1])
	}
}

func server(args []string) {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	servers := fs.String("servers", "198.51.100.2", "addresses to serve UDP at, a.b.c.d or a.b.c.d-e")
	_ = fs.Parse(args)
	addrs := parseServers(*servers)

	for _, port := range []int{portFIN, portRST} {
		l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			log.Fatal(err)
		}
		go func() {
			for {
				c, err := l.Accept()
				if err != nil {
					log.Print(err)
					continue
				}
				go serveTCP(c)
			}
		}()
	}

	// One socket per address, so that replies come from the address asked.
	for _, addr := range addrs {
		pc, err := net.ListenPacket("udp", netip.AddrPortFrom(addr, portUDP).String())
		if err != nil {
			log.Fatal(err)
		}
		go serveUDP(pc)
	}
	select {}
}

func serveUDP(pc net.PacketConn) {
	_ = pc.(*net.UDPConn).SetReadBuffer(4 << 20)
	buf := make([]byte, 65536)
	reply := make([]byte, 65536)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil || n < 4 {
			continue
		}
		down := int(binary.BigEndian.Uint32(buf))
		for down > 0 {
			size := min(down, 1400)
			_, _ = pc.WriteTo(reply[:size], addr)
			down -= size
		}
	}
}

// serveTCP reads the sizes, the upload, then writes the download and waits
// for the client to close.
func serveTCP(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	var header [8]byte
	if _, err := io.ReadFull(c, header[:]); err != nil {
		return
	}
	up, down := binary.BigEndian.Uint32(header[:4]), binary.BigEndian.Uint32(header[4:])
	if _, err := io.CopyN(io.Discard, c, int64(up)); err != nil {
		return
	}
	if _, err := c.Write(make([]byte, down)); err != nil {
		return
	}
	_, _ = io.Copy(io.Discard, c)
}

type stats struct {
	ok, failed, upBytes, downBytes atomic.Int64
}

func (s *stats) String() string {
	return fmt.Sprintf("ok=%d failed=%d up=%d down=%d", s.ok.Load(), s.failed.Load(), s.upBytes.Load(), s.downBytes.Load())
}

func client(args []string) {
	fs := flag.NewFlagSet("client", flag.ExitOnError)
	servers := fs.String("servers", "198.51.100.2", "server addresses, a.b.c.d or a.b.c.d-e")
	fin := fs.Int("fin", 0, "TCP connections closed normally, per second")
	rst := fs.Int("rst", 0, "TCP connections reset, per second")
	udp := fs.Int("udp", 0, "UDP exchanges, per second")
	duration := fs.Duration("duration", time.Minute, "how long")
	concurrency := fs.Int("concurrency", 2000, "connections at once, at most")
	_ = fs.Parse(args)

	addrs := parseServers(*servers)
	sem := make(chan struct{}, *concurrency)
	var wg sync.WaitGroup
	var finStats, rstStats, udpStats stats
	var skipped atomic.Int64

	run := func(rate int, s *stats, do func(netip.Addr) (int, int, error)) {
		if rate == 0 {
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			interval := time.Second / time.Duration(rate)
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			end := time.Now().Add(*duration)
			for now := range ticker.C {
				if now.After(end) {
					return
				}
				select {
				case sem <- struct{}{}:
				default:
					skipped.Add(1)
					continue
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer func() { <-sem }()
					up, down, err := do(addrs[rand.IntN(len(addrs))])
					if err != nil {
						s.failed.Add(1)
						return
					}
					s.ok.Add(1)
					s.upBytes.Add(int64(up))
					s.downBytes.Add(int64(down))
				}()
			}
		}()
	}

	start := time.Now()
	run(*fin, &finStats, func(a netip.Addr) (int, int, error) { return exchangeTCP(a, portFIN, false) })
	run(*rst, &rstStats, func(a netip.Addr) (int, int, error) { return exchangeTCP(a, portRST, true) })
	run(*udp, &udpStats, exchangeUDP)

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-done:
			fmt.Printf("done in %s skipped=%d\nfin %s\nrst %s\nudp %s\n", time.Since(start).Round(time.Second), skipped.Load(), &finStats, &rstStats, &udpStats)
			return
		case <-tick.C:
			fmt.Fprintf(os.Stderr, "%s fin[%s] rst[%s] udp[%s] skipped=%d\n", time.Since(start).Round(time.Second), &finStats, &rstStats, &udpStats, skipped.Load())
		}
	}
}

func parseServers(s string) []netip.Addr {
	first, last, ranged := strings.Cut(s, "-")
	start := netip.MustParseAddr(first)
	if !ranged {
		return []netip.Addr{start}
	}
	var end byte
	if _, err := fmt.Sscan(last, &end); err != nil {
		log.Fatal(err)
	}
	var addrs []netip.Addr
	for a := start; a.As4()[3] <= end; a = a.Next() {
		addrs = append(addrs, a)
		if a.As4()[3] == 255 {
			break
		}
	}
	return addrs
}

func exchangeTCP(addr netip.Addr, port int, reset bool) (int, int, error) {
	up, down := 200+rand.IntN(1800), 1000+rand.IntN(63000)
	c, err := net.DialTimeout("tcp", netip.AddrPortFrom(addr, uint16(port)).String(), 5*time.Second)
	if err != nil {
		return 0, 0, err
	}
	tc := c.(*net.TCPConn)
	defer tc.Close()
	_ = tc.SetDeadline(time.Now().Add(20 * time.Second))

	msg := make([]byte, 8+up)
	binary.BigEndian.PutUint32(msg, uint32(up))
	binary.BigEndian.PutUint32(msg[4:], uint32(down))
	if _, err := tc.Write(msg); err != nil {
		return 0, 0, err
	}
	if _, err := io.CopyN(io.Discard, tc, int64(down)); err != nil {
		return 0, 0, err
	}
	if reset {
		_ = tc.SetLinger(0)
	}
	return up + 8, down, nil
}

func exchangeUDP(addr netip.Addr) (int, int, error) {
	up, down := 50+rand.IntN(450), 50+rand.IntN(2750)
	c, err := net.Dial("udp", netip.AddrPortFrom(addr, portUDP).String())
	if err != nil {
		return 0, 0, err
	}
	defer c.Close()
	msg := make([]byte, up)
	binary.BigEndian.PutUint32(msg, uint32(down))
	if _, err := c.Write(msg); err != nil {
		return 0, 0, err
	}
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 2048)
	got := 0
	for got < down {
		n, err := c.Read(buf)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				return up, got, fmt.Errorf("got %d of %d", got, down)
			}
			return up, got, err
		}
		got += n
	}
	return up, got, nil
}
