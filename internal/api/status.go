package api

import (
	"os"
	"path/filepath"
)

const (
	DefaultRuntimeDir = "/run/tunwarden"
	RuntimeDirEnv     = "TUNWARDEN_RUNTIME_DIR"
	SocketName        = "tunwardend.sock"
	LockName          = "tunwardend.lock"
	StatusPath        = "/v1/status"
)

type StatusResponse struct {
	Daemon           string   `json:"daemon"`
	Connection       string   `json:"connection"`
	RuntimeDirectory string   `json:"runtime_directory"`
	Proxy            string   `json:"proxy"`
	TUN              string   `json:"tun"`
	Warnings         []string `json:"warnings,omitempty"`
}

func RuntimeDirFromEnv() string {
	if dir := os.Getenv(RuntimeDirEnv); dir != "" {
		return dir
	}
	return DefaultRuntimeDir
}

func SocketPath(runtimeDir string) string {
	if runtimeDir == "" {
		runtimeDir = RuntimeDirFromEnv()
	}
	return filepath.Join(runtimeDir, SocketName)
}

func LockPath(runtimeDir string) string {
	if runtimeDir == "" {
		runtimeDir = RuntimeDirFromEnv()
	}
	return filepath.Join(runtimeDir, LockName)
}
