package storejson

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	DefaultDirectoryMode fs.FileMode = 0o700
	DefaultFileMode      fs.FileMode = 0o600
)

type Operation string

const (
	OperationEncodeJSON          Operation = "encode JSON"
	OperationCreateDirectory     Operation = "create directory"
	OperationCreateTempFile      Operation = "create temporary file"
	OperationSetTempPermissions  Operation = "set temporary file permissions"
	OperationWriteTempFile       Operation = "write temporary file"
	OperationSyncTempFile        Operation = "sync temporary file"
	OperationCloseTempFile       Operation = "close temporary file"
	OperationRenameTempFile      Operation = "rename temporary file"
	OperationSyncParentDirectory Operation = "sync parent directory"
)

type WriteError struct {
	Operation Operation
	Err       error
}

func (e *WriteError) Error() string {
	return fmt.Sprintf("%s: %v", e.Operation, e.Err)
}

func (e *WriteError) Unwrap() error {
	return e.Err
}

type Options struct {
	DirectoryMode fs.FileMode
	FileMode      fs.FileMode
	TempPattern   string
	SyncParentDir func(string) error
}

func WriteFile(path string, value any, opts Options) error {
	if opts.DirectoryMode == 0 {
		opts.DirectoryMode = DefaultDirectoryMode
	}
	if opts.FileMode == 0 {
		opts.FileMode = DefaultFileMode
	}
	if opts.TempPattern == "" {
		opts.TempPattern = "." + filepath.Base(path) + "-*.tmp"
	}
	if opts.SyncParentDir == nil {
		opts.SyncParentDir = SyncDir
	}

	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return writeError(OperationEncodeJSON, err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, opts.DirectoryMode); err != nil {
		return writeError(OperationCreateDirectory, err)
	}

	tmp, err := os.CreateTemp(dir, opts.TempPattern)
	if err != nil {
		return writeError(OperationCreateTempFile, err)
	}
	tmpName := tmp.Name()
	closed := false
	defer func() { _ = os.Remove(tmpName) }()
	closeTmp := func() {
		if !closed {
			_ = tmp.Close()
			closed = true
		}
	}

	if err := tmp.Chmod(opts.FileMode); err != nil {
		closeTmp()
		return writeError(OperationSetTempPermissions, err)
	}
	if _, err := tmp.Write(data); err != nil {
		closeTmp()
		return writeError(OperationWriteTempFile, err)
	}
	if err := tmp.Sync(); err != nil {
		closeTmp()
		return writeError(OperationSyncTempFile, err)
	}
	if err := tmp.Close(); err != nil {
		closed = true
		return writeError(OperationCloseTempFile, err)
	}
	closed = true
	if err := os.Rename(tmpName, path); err != nil {
		return writeError(OperationRenameTempFile, err)
	}
	if err := opts.SyncParentDir(dir); err != nil {
		return writeError(OperationSyncParentDirectory, err)
	}
	return nil
}

func SyncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open parent directory %s for sync: %w", path, err)
	}
	if err := dir.Sync(); err != nil {
		_ = dir.Close()
		return fmt.Errorf("sync parent directory %s: %w", path, err)
	}
	if err := dir.Close(); err != nil {
		return fmt.Errorf("close parent directory %s after sync: %w", path, err)
	}
	return nil
}

func writeError(op Operation, err error) error {
	if err == nil {
		return nil
	}
	return &WriteError{Operation: op, Err: err}
}
