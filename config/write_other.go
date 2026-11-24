//go:build !unix

package config

import (
	"io/fs"
	"os"
)

func keepOwner(*os.File, fs.FileInfo) {}
