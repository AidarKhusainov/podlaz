package daemon

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

var errDaemonLockHeld = errors.New("daemon lock is held by another podlazd process")

type daemonLock struct {
	file *os.File
}

func acquireDaemonLock(path string) (*daemonLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open daemon lock %s: %w", path, err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("set daemon lock permissions: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, errDaemonLockHeld
		}
		return nil, fmt.Errorf("acquire daemon lock: %w", err)
	}
	return &daemonLock{file: file}, nil
}

func (l *daemonLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	l.file = nil
	return errors.Join(unlockErr, closeErr)
}
