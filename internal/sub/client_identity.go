package sub

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	clientIDFileName                  = "client-id"
	subscriptionClientIDPlaceholder   = "{tunwarden-client-id}"
)

// DefaultClientIDPath returns the user-owned subscription client identity path.
func DefaultClientIDPath() (string, error) {
	storePath, err := DefaultStorePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(storePath), clientIDFileName), nil
}

// LoadOrCreateClientID returns the stable TunWarden subscription client identity.
// The identity is generated randomly and stored under user-owned XDG state; raw
// hardware identifiers are never read.
func LoadOrCreateClientID(path string) (string, error) {
	if path == "" {
		defaultPath, err := DefaultClientIDPath()
		if err != nil {
			return "", err
		}
		path = defaultPath
	}

	data, err := os.ReadFile(path)
	if err == nil {
		id := strings.TrimSpace(string(data))
		if !validClientID(id) {
			return "", fmt.Errorf("read subscription client identity %s: invalid client-id", path)
		}
		return id, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read subscription client identity %s: %w", path, err)
	}

	id, err := generateClientID()
	if err != nil {
		return "", fmt.Errorf("generate subscription client identity: %w", err)
	}
	if err := writeClientID(path, id); err != nil {
		return "", err
	}
	return id, nil
}

func subscriptionRequestURL(raw string) (string, error) {
	if !strings.Contains(raw, subscriptionClientIDPlaceholder) {
		return raw, nil
	}
	id, err := LoadOrCreateClientID("")
	if err != nil {
		return "", fmt.Errorf("prepare subscription client identity: %w", err)
	}
	return strings.ReplaceAll(raw, subscriptionClientIDPlaceholder, id), nil
}

func generateClientID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func writeClientID(path, id string) error {
	if !validClientID(id) {
		return fmt.Errorf("write subscription client identity %s: invalid client-id", path)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create subscription client identity directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".client-id-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary subscription client identity: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure temporary subscription client identity: %w", err)
	}
	if _, err := io.WriteString(tmp, id+"\n"); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary subscription client identity: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temporary subscription client identity: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary subscription client identity: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace subscription client identity atomically: %w", err)
	}
	return nil
}

func validClientID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, r := range id {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
				return false
			}
		}
	}
	return true
}
