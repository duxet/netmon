// Package vendors finds the manufacturer of a network interface from its MAC
// address, using the IEEE Registration Authority's public MA-L, MA-M and MA-S
// listings. The data is embedded; refresh it with `go generate ./vendors`.
package vendors

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
)

//go:generate go run ./gen

// oui.tsv.gz has one "prefix<TAB>name" line per assignment, the prefix in
// lowercase hex: 6 digits for MA-L (24 bits), 7 for MA-M and 9 for MA-S.
//
//go:embed oui.tsv.gz
var data []byte

type table struct {
	// by prefix length in hex digits
	byDigits map[int]map[uint64]string
}

var load = sync.OnceValue(func() table {
	t := table{byDigits: map[int]map[uint64]string{6: {}, 7: {}, 9: {}}}

	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		log.Printf("MAC vendor data is unreadable: %v", err)
		return t
	}
	scanner := bufio.NewScanner(zr)
	for scanner.Scan() {
		prefix, name, ok := strings.Cut(scanner.Text(), "\t")
		value, err := strconv.ParseUint(prefix, 16, 64)
		if !ok || err != nil || t.byDigits[len(prefix)] == nil {
			continue
		}
		t.byDigits[len(prefix)][value] = name
	}
	if err := scanner.Err(); err != nil {
		log.Printf("MAC vendor data is unreadable: %v", err)
	}
	return t
})

// IsRandomized reports whether mac is locally administered rather than
// assigned by a manufacturer, as the private, per-network addresses phones
// and laptops use. Such addresses have no vendor.
func IsRandomized(mac net.HardwareAddr) bool {
	return len(mac) > 0 && mac[0]&0x02 != 0
}

// Lookup returns the manufacturer the IEEE assigned mac's prefix to, or ""
// when it is unknown, unlisted or the address is randomized.
func Lookup(mac net.HardwareAddr) string {
	if len(mac) < 6 || IsRandomized(mac) {
		return ""
	}

	t := load()
	// The first 36 bits, as 9 hex digits; MA-S assignments are the most
	// specific, then MA-M (28 bits) and MA-L (24 bits).
	prefix := uint64(mac[0])<<28 | uint64(mac[1])<<20 | uint64(mac[2])<<12 | uint64(mac[3])<<4 | uint64(mac[4])>>4
	for _, digits := range []int{9, 7, 6} {
		if name, ok := t.byDigits[digits][prefix>>(4*(9-digits))]; ok {
			return name
		}
	}
	return ""
}
