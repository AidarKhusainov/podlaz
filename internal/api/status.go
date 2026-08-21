package api

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	DefaultRuntimeDir = "/" + "run" + "/podlaz"
	RuntimeDirEnv     = "PODLAZ_" + "RUNTIME_DIR"
	ServiceEnv        = "PODLAZ_" + "SERVICE"
	SocketName        = "podlazd" + ".sock"
	// AbstractSocketName is the Linux abstract namespace fallback used by the
	// packaged daemon so ordinary local users can reach the polkit authorization
	// boundary without broadening filesystem socket permissions.
	AbstractSocketName = "podlazd"
	LockName           = "podlazd" + ".lock"
	StatusPath         = "/v1" + "/status"

	ServiceManual  = "manual"
	ServiceSystemd = "systemd"

	ConnectionCoreExited = "error (core exited)"
	LifecycleConnecting  = "connecting"

	StartupScanStatusClean           = "clean"
	StartupScanStatusStale           = "stale"
	StartupScanStatusIncomplete      = "incomplete"
	StartupScanStatusStaleIncomplete = "stale_incomplete"
)

type StatusResponse struct {
	Daemon              string              `json:"daemon"`
	Service             string              `json:"service"`
	Connection          string              `json:"connection"`
	LifecyclePhase      string              `json:"lifecycle_phase,omitempty"`
	Mode                string              `json:"mode,omitempty"`
	ProfileID           string              `json:"profile_id,omitempty"`
	ProfileName         string              `json:"profile_name,omitempty"`
	RuntimeDirectory    string              `json:"runtime_directory"`
	RuntimeConfigPath   string              `json:"runtime_config_path,omitempty"`
	ActiveTransactionID string              `json:"active_transaction_id,omitempty"`
	Proxy               string              `json:"proxy"`
	TUN                 string              `json:"tun"`
	TunHealth           *TunHealthStatus    `json:"tun_health,omitempty"`
	Routes              string              `json:"routes,omitempty"`
	DNS                 string              `json:"dns,omitempty"`
	Firewall            string              `json:"firewall,omitempty"`
	Transactions        []TransactionStatus `json:"transactions,omitempty"`
	StartupScan         *StartupScanStatus  `json:"startup_scan,omitempty"`
	Warnings            []string            `json:"warnings,omitempty"`
	InspectionWarnings  []RecoveryWarning   `json:"inspection_warnings,omitempty"`
}

type TransactionStatus struct {
	ID                string `json:"id"`
	State             string `json:"state"`
	RollbackAvailable bool   `json:"rollback_available"`
	RequiresCleanup   bool   `json:"requires_cleanup"`
	Path              string `json:"path"`
}

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
	case s.LifecyclePhase != "" && s.LifecyclePhase != LifecycleConnecting:
		return fmt.Errorf("invalid lifecycle_phase %q", s.LifecyclePhase)
	case s.LifecyclePhase == LifecycleConnecting && s.Mode == "":
		return errors.New("connecting lifecycle_phase requires mode")
	case s.LifecyclePhase == LifecycleConnecting && s.ProfileName == "":
		return errors.New("connecting lifecycle_phase requires profile_name")
	case s.RuntimeDirectory == "":
		return errors.New("missing runtime_directory field")
	case s.Proxy == "":
		return errors.New("missing proxy field")
	case s.TUN == "":
		return errors.New("missing tun field")
	case s.ActiveTransactionID != "" && s.Connection != "active":
		return fmt.Errorf("active_transaction_id requires active connection, got %q", s.Connection)
	case s.ActiveTransactionID != "" && s.Mode != "tun":
		return fmt.Errorf("active_transaction_id requires TUN mode, got %q", s.Mode)
	case s.ActiveTransactionID != "" && s.ProfileID == "":
		return errors.New("active_transaction_id requires profile_id")
	case s.ActiveTransactionID != "" && s.RuntimeConfigPath == "":
		return errors.New("active_transaction_id requires runtime_config_path")
	case s.TunHealth != nil && s.Mode != "tun":
		return fmt.Errorf("tun_health requires TUN mode, got %q", s.Mode)
	case s.TunHealth != nil && s.Connection != "active" && s.Connection != ConnectionCoreExited:
		return fmt.Errorf("tun_health requires active or bounded core-exit reconciliation, got %q", s.Connection)
	case s.TunHealth != nil && s.Connection == ConnectionCoreExited && s.TunHealth.State == TunHealthVerified:
		return errors.New("verified tun_health is invalid after supervised core exit")
	}
	if s.TunHealth != nil {
		if err := ValidateTunHealthStatus(*s.TunHealth); err != nil {
			return err
		}
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
	for _, warning := range s.InspectionWarnings {
		if warning.Target == "" {
			return errors.New("missing inspection warning target")
		}
		if warning.Message == "" {
			return errors.New("missing inspection warning message")
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

func AbstractSocketAddress() string {
	return "\x00" + AbstractSocketName
}

func LockPath(runtimeDir string) string {
	if runtimeDir == "" {
		runtimeDir = RuntimeDirFromEnv()
	}
	return filepath.Join(runtimeDir, LockName)
}
