package sub

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	clientIDFileName                = "client-id"
	subscriptionClientIDPlaceholder = "{podlaz-client-id}"
)

var errUnsupportedClientIDPlaceholder = errors.New("client identity placeholder must be the complete value of an HTTP query parameter")
var errInvalidClientID = errors.New("invalid client-id")

// DefaultClientIDPath returns the user-owned subscription client identity path.
func DefaultClientIDPath() (string, error) {
	storePath, err := DefaultStorePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(storePath), clientIDFileName), nil
}

// LoadOrCreateClientID returns the stable podlaz subscription client identity.
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

	id, err := readClientID(path)
	if err == nil {
		return id, nil
	}
	if errors.Is(err, errInvalidClientID) {
		return readClientIDAfterConcurrentCreate(path)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	id, err = generateClientID()
	if err != nil {
		return "", fmt.Errorf("generate subscription client identity: %w", err)
	}
	if err := createClientID(path, id); err != nil {
		if errors.Is(err, os.ErrExist) {
			return readClientIDAfterConcurrentCreate(path)
		}
		return "", err
	}
	return id, nil
}

func readClientID(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read subscription client identity %s: %w", path, err)
	}
	id := strings.TrimSpace(string(data))
	if !validClientID(id) {
		return "", fmt.Errorf("read subscription client identity %s: %w", path, errInvalidClientID)
	}
	return id, nil
}

func readClientIDAfterConcurrentCreate(path string) (string, error) {
	var lastErr error
	for range 100 {
		id, err := readClientID(path)
		if err == nil {
			return id, nil
		}
		lastErr = err
		if !errors.Is(err, errInvalidClientID) && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		time.Sleep(time.Millisecond)
	}
	return "", lastErr
}

func subscriptionRequestURL(raw string) (string, string, error) {
	if !strings.Contains(raw, subscriptionClientIDPlaceholder) {
		return raw, "", nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("parse subscription URL: %w", err)
	}
	if placeholderInUnsupportedURLPart(u) {
		return "", "", errUnsupportedClientIDPlaceholder
	}

	type replacement struct {
		key   string
		index int
	}
	query := u.Query()
	replacements := []replacement{}
	for key, values := range query {
		if strings.Contains(key, subscriptionClientIDPlaceholder) {
			return "", "", errUnsupportedClientIDPlaceholder
		}
		for i, value := range values {
			switch {
			case value == subscriptionClientIDPlaceholder:
				replacements = append(replacements, replacement{key: key, index: i})
			case strings.Contains(value, subscriptionClientIDPlaceholder):
				return "", "", errUnsupportedClientIDPlaceholder
			}
		}
	}
	if len(replacements) == 0 {
		return "", "", errUnsupportedClientIDPlaceholder
	}

	id, err := LoadOrCreateClientID("")
	if err != nil {
		return "", "", fmt.Errorf("prepare subscription client identity: %w", err)
	}
	for _, replacement := range replacements {
		query[replacement.key][replacement.index] = id
	}
	u.RawQuery = query.Encode()
	return u.String(), id, nil
}

func placeholderInUnsupportedURLPart(u *url.URL) bool {
	if strings.Contains(u.Scheme, subscriptionClientIDPlaceholder) ||
		strings.Contains(u.Opaque, subscriptionClientIDPlaceholder) ||
		strings.Contains(u.Host, subscriptionClientIDPlaceholder) ||
		strings.Contains(u.Path, subscriptionClientIDPlaceholder) ||
		strings.Contains(u.RawPath, subscriptionClientIDPlaceholder) ||
		strings.Contains(u.Fragment, subscriptionClientIDPlaceholder) {
		return true
	}
	if u.User == nil {
		return false
	}
	if strings.Contains(u.User.Username(), subscriptionClientIDPlaceholder) {
		return true
	}
	password, ok := u.User.Password()
	return ok && strings.Contains(password, subscriptionClientIDPlaceholder)
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

func createClientID(path, id string) error {
	if !validClientID(id) {
		return fmt.Errorf("write subscription client identity %s: invalid client-id", path)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create subscription client identity directory: %w", err)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create subscription client identity %s: %w", path, err)
	}
	success := false
	defer func() {
		if !success {
			_ = os.Remove(path)
		}
	}()

	if _, err := io.WriteString(file, id+"\n"); err != nil {
		_ = file.Close()
		return fmt.Errorf("write subscription client identity %s: %w", path, err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync subscription client identity %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close subscription client identity %s: %w", path, err)
	}
	success = true
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
