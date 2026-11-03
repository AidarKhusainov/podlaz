//go:build linux

package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// verifyNMXrayConfigChildAccess models the actual post-drop credentials: no
// supplementary groups, no DAC override and no root identity. A root broker
// must not reserve a generation that its restricted Xray cannot start.
// This is an admission guard, not a replacement for the final child-side
// verification or for holding an immutable config file descriptor.
func verifyNMXrayConfigChildAccess(path string, uid, gid int) error {
	if uid <= 0 || gid <= 0 || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("incomplete NM Xray restricted config authority")
	}
	file, err := os.Lstat(path)
	if err != nil {
		return errors.New("NM Xray restricted config is unavailable")
	}
	if !file.Mode().IsRegular() || !nmXrayPermissionAllows(file, uid, gid, 0o4) {
		return errors.New("NM Xray restricted config cannot be read by child")
	}
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
			!nmXrayPermissionAllows(info, uid, gid, 0o1) {
			return errors.New("NM Xray restricted config directory cannot be traversed by child")
		}
		if dir == "/" {
			break
		}
	}
	return nil
}

func nmXrayPermissionAllows(info os.FileInfo, uid, gid int, requested os.FileMode) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	mode := info.Mode().Perm()
	if stat.Uid == uint32(uid) {
		return (mode>>6)&requested == requested
	}
	if stat.Gid == uint32(gid) {
		return (mode>>3)&requested == requested
	}
	return mode&requested == requested
}
