//go:build unix

package config

import (
	"io/fs"
	"os"
	"syscall"
)

// keepOwner gives f the owner of the file it replaces, when netmon may, such
// as when it runs as root and the file is someone else's.
func keepOwner(f *os.File, info fs.FileInfo) {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		_ = f.Chown(int(stat.Uid), int(stat.Gid))
	}
}
