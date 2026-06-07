package api

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	DefaultRuntimeDir = "/run/tunwarden"
	RuntimeDirEnv     = "TUNWARDEN_RUNTIME_DIR"
	ServiceEnv        = "TUNWARDEN_SERVICE"
	SocketName        = "tunwardend.sock"
	LockName          = "tunwardend.lock"
	StatusPath        = "/v1/status"

	ServiceManual  = "manual"
	ServiceSystemd = "systemd"
)

type StatusResponse struct {
	Daemon            string   `json:"daemon"`
	Service           string   `json:"service"`
	Connection        string   `json:"connection"`
	Mode              string   `json:"mode,omitempty"`
	RuntimeDirectory  string   `json:"runtime_directory"`
	RuntimeConfigPath string   `json:"runtime_config_path,omitempty"`
	Proxy             string   `json:"proxy"`
	TUN               string   `json:"tun"`
	Routes            string   `json:"routes,omitempty"`
	DNS               string   `json:"dns,omitempty"`
	Firewall          string   `json:"firewall,omitempty"`
	Warnings          []string `json:"warnings,omitempty"`
}

func ValidateStatusResponse(s StatusResponse) error {
	switch {
	case s.Daemon == "":
		return errors.New("missing daemon field")
	case s.Service == "":
		return errors.New("missing service field")
	case !ValidService(s.Service):
		return fmt.Errorf("invalid service field %q", s.Service)
	case s.Connection == "":
		return errors.New("missing connection field")
	case s.RuntimeDirectory == "":
		return errors.New("missing runtime_directory field")
	case s.Proxy == "":
		return errors.New("missing proxy field")
	case s.TUN == "":
		return errors.New("missing tun field")
	default:
		return nil
	}
}

func RuntimeDirFromEnv() string {
	if dir := os.Getenv(RuntimeDirEnv); dir != "" {
		return dir
	}
	return DefaultRuntimeDir
}

func ServiceFromEnv() string {
	if os.Getenv(ServiceEnv) == ServiceSystemd {
		return ServiceSystemd
	}
	return ServiceManual
}

func ValidService(service string) bool {
	switch service {
	case ServiceManual, ServiceSystemd:
		return true
	default:
		return false
	}
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
