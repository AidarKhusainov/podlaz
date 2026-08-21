package api

import (
	"errors"
	"strings"
)

const (
	AutostartConfigurePath = "/v1/autostart"
	AutostartStatusPath    = "/v1/autostart/status"
)

// AutostartConfigureRequest contains the validated connection material that the
// daemon needs to persist for a future boot. Handoff policy is intentionally not
// part of this contract; boot autostart uses the canonical Connect default.
type AutostartConfigureRequest struct {
	Mode    string          `json:"mode"`
	Profile ProfileSnapshot `json:"profile"`
}

// AutostartStatusResponse is the small, non-secret daemon projection used by the
// normal autostart/status UX. Detailed connection material is never returned.
type AutostartStatusResponse struct {
	Enabled     bool   `json:"enabled"`
	Mode        string `json:"mode,omitempty"`
	ProfileName string `json:"profile_name,omitempty"`
}

func ValidateAutostartConfigureRequest(req AutostartConfigureRequest) error {
	return ValidateConnectRequest(ConnectRequest{Mode: req.Mode, Profile: req.Profile})
}

func ValidateAutostartStatusResponse(status AutostartStatusResponse) error {
	if !status.Enabled {
		if strings.TrimSpace(status.Mode) != "" || strings.TrimSpace(status.ProfileName) != "" {
			return errors.New("disabled autostart status must not include connection metadata")
		}
		return nil
	}
	if strings.TrimSpace(status.Mode) == "" {
		return errors.New("enabled autostart status requires mode")
	}
	if strings.TrimSpace(status.ProfileName) == "" {
		return errors.New("enabled autostart status requires profile_name")
	}
	return nil
}
