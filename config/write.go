package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// replaceFile replaces the file at path with data, so that it is never seen
// half written: the new contents go to a file next to it first, which then
// takes its place. A link is followed, and the file keeps its permissions
// and, where netmon may change it, its owner. A new file can only be read
// by netmon's user, as it may come to hold secrets.
func replaceFile(path string, data []byte) error {
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	}

	mode := fs.FileMode(0o600)
	info, err := os.Stat(path)
	switch {
	case err == nil:
		mode = info.Mode().Perm()
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if errors.Is(err, fs.ErrPermission) && info != nil {
		// The file can be written, but not its directory: write it in place.
		return os.WriteFile(path, data, mode)
	}
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if info != nil {
		keepOwner(tmp, info)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
