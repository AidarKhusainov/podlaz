package daemon

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/AidarKhusainov/podlaz/internal/render"
)

func logCoreStarted(pid int, profileID string) {
	log.Printf("podlazd: core xray started pid=%d profile=%s", pid, render.Redact(profileID))
}

func logCoreStartFailed(profileID string, err error) {
	log.Printf("podlazd: core xray start failed profile=%s error=%s", render.Redact(profileID), render.Redact(err.Error()))
}

func logCoreStopped(pid int, profileID string) {
	log.Printf("podlazd: core xray stopped pid=%d profile=%s", pid, render.Redact(profileID))
}

func logCoreExited(pid int, profileID, message string) {
	log.Printf("podlazd: core xray exited pid=%d profile=%s error=%s", pid, render.Redact(profileID), render.Redact(message))
}

func writeRuntimeConfig(path string, content []byte, permissions runtimeConfigPermissions) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, permissions.DirMode); err != nil {
		return fmt.Errorf("create generated runtime config directory: %w", err)
	}
	if err := applyRuntimeConfigOwnership(dir, permissions); err != nil {
		return fmt.Errorf("own generated runtime config directory: %w", err)
	}
	if err := os.Chmod(dir, permissions.DirMode); err != nil {
		return fmt.Errorf("secure generated runtime config directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".xray-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary generated Xray config: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := applyRuntimeConfigOwnership(tmpName, permissions); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("own temporary generated Xray config: %w", err)
	}
	if err := tmp.Chmod(permissions.FileMode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure temporary generated Xray config: %w", err)
	}
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary generated Xray config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temporary generated Xray config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary generated Xray config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace generated Xray config atomically: %w", err)
	}
	if err := syncDirectory(dir); err != nil {
		return fmt.Errorf("sync generated Xray config directory: %w", err)
	}
	return nil
}

func applyRuntimeConfigOwnership(path string, permissions runtimeConfigPermissions) error {
	if !permissions.Chown {
		return nil
	}
	return os.Chown(path, permissions.UID, permissions.GID)
}

func removeGeneratedConfig(path string) {
	if path == "" {
		return
	}
	_ = os.Remove(path)
	_ = os.Remove(filepath.Dir(path))
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
