package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/AidarKhusainov/podlaz/internal/storejson"
)

// lockForMutation serializes the entire read-modify-write transaction across
// CLI processes. Locking profiles.json itself would fail across atomic rename:
// each replacement has a new inode, so use a persistent adjacent lock file.
// Callers must hold the lock from before loadState until saveState completes.
func (s Store) lockForMutation() (*os.File, error) {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, storejson.DefaultDirectoryMode); err != nil {
		return nil, fmt.Errorf("create profile store lock directory: %w", err)
	}
	path := s.path + ".lock"
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CREAT|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, uint32(storejson.DefaultFileMode))
	if err != nil {
		return nil, fmt.Errorf("open profile store lock: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	if err := syscall.Flock(fd, syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock profile store: %w", err)
	}
	// Closing this descriptor releases the advisory lock, including on early
	// returns. Never unlink the lock file: doing so would split lock authority.
	return file, nil
}
