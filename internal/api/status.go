package api

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	DefaultRuntimeDir = "/run/podlaz"
	RuntimeDirEnv     = "PODLAZ_RUNTIME_DIR"
	ServiceEnv        = "PODLAZ_SERVICE"
	SocketName        = "podlazd.sock"
	LockName          = "podlazd.lock"
	StatusPath        = "/v1/status"

	ServiceManual  = "manual"
	ServiceSystemd = "systemd"

	StartupScanStatusClean           = "clean"
	StartupScanStatusStale           = "stale"
	StartupScanStatusIncomplete      = "incomplete"
	StartupScanStatusStaleIncomplete = "stale_incomplete"
)

type StatusResponse struct {
	Daemon            string              `json:"daemon"`
	Service           string              `json:"service"`
	Connection        string              `json:"connection"`
	Mode              string              `json:"mode,omitempty"`
	RuntimeDirectory  string              `json:"runtime_directory"`
	RuntimeConfigPath string              `json:"runtime_config_path,omitempty"`
	Proxy             string              `json:"proxy"`
	TUN               string              `json:"tun"`
	Routes            string              `json:"routes,omitempty"`
	DNS               string              `json:"dns,omitempty"`
	Firewall          string              `json:"firewall,omitempty"`
	Transactions      []TransactionStatus `json:"transactions,omitempty"`
	StartupScan       *StartupScanStatus  `json:"startup_scan,omitempty"`
	Warnings          []string            `json:"warnings,omitempty"`
}

// TransactionStatus is the daemon API's redacted transaction summary. It
// exposes facts only; human-readable status text is rendered by clients.
type TransactionStatus struct {
	ID                string `json:"id"`
	State             string `json:"state"`
	RollbackAvailable bool   `json:"rollback_available"`
	RequiresCleanup   bool   `json:"requires_cleanup"`
	Path              string `json:"path"`
}

// StartupScanStatus is the daemon API's redacted startup recovery scan summary.
// It is captured once during daemon startup and is not an execution result.
type StartupScanStatus struct {
	Status          string              `json:"status"`
	Candidates      []RecoveryCandidate `json:"candidates,omitempty"`
	Warnings        []RecoveryWarning   `json:"warnings,omitempty"`
	SuggestedAction string              `json:"suggested_action,omitempty"`
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
	}
	for _, tx := range s.Transactions {
		if err := ValidateTransactionStatus(tx); err != nil {
			return err
		}
	}
	if s.StartupScan != nil {
		if err := ValidateStartupScanStatus(*s.StartupScan); err != nil {
			return err
		}
	}
	return nil
}

func ValidateTransactionStatus(tx TransactionStatus) error {
	switch {
	case tx.ID == "":
		return errors.New("missing transaction id")
	case tx.State == "":
		return errors.New("missing transaction state")
	case !validTransactionState(tx.State):
		return fmt.Errorf("invalid transaction state %q", tx.State)
	case tx.Path == "":
		return errors.New("missing transaction path")
	default:
		return nil
	}
}

func ValidateStartupScanStatus(scan StartupScanStatus) error {
	if !validStartupScanStatus(scan.Status) {
		return fmt.Errorf("invalid startup scan status %q", scan.Status)
	}
	for _, candidate := range scan.Candidates {
		if err := ValidateRecoveryCandidate(candidate); err != nil {
			return err
		}
	}
	for _, warning := range scan.Warnings {
		if warning.Target == "" {
			return errors.New("missing startup scan warning target")
		}
		if warning.Message == "" {
			return errors.New("missing startup scan warning message")
		}
	}
	return nil
}

func validTransactionState(state string) bool {
	switch state {
	case "planned", "applying", "applied", "verifying", "committed", "rolling_back", "rolled_back", "failed":
		return true
	default:
		return false
	}
}

func validStartupScanStatus(status string) bool {
	switch status {
	case StartupScanStatusClean, StartupScanStatusStale, StartupScanStatusIncomplete, StartupScanStatusStaleIncomplete:
		return true
	default:
		return false
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
