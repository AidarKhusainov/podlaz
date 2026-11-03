//go:build linux

package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

const nmXrayMaxGeneratedConfigBytes = 4 << 20

func validNMXrayConfigDigest(expected string) bool {
	if len(expected) != sha256.Size*2 {
		return false
	}
	for _, character := range expected {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

// The broker and restricted child both inspect exact bytes on disk, not just
// file metadata. The root-owned generated configuration must match the bytes
// previously accepted by the official Xray pre-mutation compatibility gate.
// This never logs JSON contents or hashes of private provider configurations.
func verifyNMXrayConfigContent(path, expected string) error {
	if !validNMXrayConfigDigest(expected) {
		return errors.New("NM Xray request lacks exact official-preflight config digest")
	}
	rawFD, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return errors.New("NM Xray generated configuration unavailable")
	}
	file := os.NewFile(uintptr(rawFD), "NM Xray generated config")
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 ||
		info.Size() > nmXrayMaxGeneratedConfigBytes {
		return errors.New("NM Xray generated configuration has unsafe size or type")
	}
	data, err := io.ReadAll(io.LimitReader(file, nmXrayMaxGeneratedConfigBytes+1))
	if err != nil || len(data) == 0 || len(data) > nmXrayMaxGeneratedConfigBytes {
		return errors.New("NM Xray generated configuration cannot be verified")
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != expected {
		return errors.New("NM Xray generated configuration differs from official preflight")
	}
	return nil
}
