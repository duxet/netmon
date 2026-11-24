package main

import (
	"log"
	"net"
)

// serveNXDOMAIN answers every DNS query on a local UDP port with "no such
// name", so hostname lookups cost what netmon's own code costs, not what a
// real resolver's latency does. It returns the address to use as a server.
func serveNXDOMAIN() string {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		buf := make([]byte, 1500)
		for {
			n, addr, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			if n < 12 {
				continue
			}
			// Keep the header and the question, drop the rest (e.g. EDNS).
			end := 12
			for end < n && buf[end] != 0 {
				end += int(buf[end]) + 1
			}
			end += 5
			if end > n {
				continue
			}
			resp := append([]byte(nil), buf[:end]...)
			resp[2] = 0x80 | resp[2]&0x79 // a response, same opcode and RD
			resp[3] = 0x80 | 3            // recursion available, NXDOMAIN
			resp[4], resp[5] = 0, 1
			for i := 6; i < 12; i++ {
				resp[i] = 0
			}
			_, _ = conn.WriteTo(resp, addr)
		}
	}()
	return conn.LocalAddr().String()
}
