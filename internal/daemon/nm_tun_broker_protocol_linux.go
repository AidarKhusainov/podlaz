//go:build linux

package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
)

const (
	nmBrokerRequestSchema   = "podlaz.nm-tun-broker-request.v2"
	nmBrokerRequestMaxBytes = 4096
	nmBrokerSocket          = "/run/podlaz-nm-fd/broker.sock"
)

// The only accepted wire request is an exact authority selected by the
// privileged local daemon. The receiving broker independently repeats the
// durable NM L3, Privacy Envelope, D-Bus and kernel FD checks before
// reserving anything. A request's values grant no cleanup authority.
type nmBrokerGenerationRequest struct {
	Schema     string                `json:"schema"`
	SessionID  string                `json:"session_id"`
	EgressMark uint32                `json:"egress_mark"`
	ConfigPath string                `json:"config_path"`
	ConfigSHA256 string             `json:"config_sha256"`
	Kernel     nmTunFDKernelIdentity `json:"kernel"`
	NM         nmTunDBusAuthority    `json:"network_manager"`
}

func decodeNMBrokerGenerationRequest(data []byte) (nmBrokerGenerationRequest, error) {
	var request nmBrokerGenerationRequest
	if len(data) == 0 || len(data) > nmBrokerRequestMaxBytes {
		return request, errors.New("invalid bounded NM broker message")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&request); err != nil {
		return nmBrokerGenerationRequest{}, errors.New("invalid NM broker message")
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nmBrokerGenerationRequest{}, errors.New("trailing NM broker message data")
	}
	if request.Schema != nmBrokerRequestSchema || !networkSessionIDPattern.MatchString(request.SessionID) ||
		request.EgressMark == 0 || !validNMXrayConfigDigest(request.ConfigSHA256) ||
		len(request.ConfigPath) > 512 ||
		!filepath.IsAbs(request.ConfigPath) || filepath.Clean(request.ConfigPath) != request.ConfigPath ||
		!strings.HasPrefix(request.ConfigPath, "/run/podlaz/") ||
		request.Kernel.Name == "" || request.Kernel.Index <= 0 || request.Kernel.MTU <= 0 ||
		request.Kernel.NetNSDevice == 0 || request.Kernel.NetNSInode == 0 ||
		request.Kernel.Name != request.NM.Name || uint64(request.Kernel.MTU) != uint64(request.NM.MTU) ||
		!nmUUIDPattern.MatchString(request.NM.UUID) ||
		!validNMObjectPath(request.NM.DevicePath, "/org/freedesktop/NetworkManager/Devices/") ||
		!validNMObjectPath(request.NM.ActivePath, "/org/freedesktop/NetworkManager/ActiveConnection/") ||
		!validNMObjectPath(request.NM.SettingsPath, "/org/freedesktop/NetworkManager/Settings/") {
		return nmBrokerGenerationRequest{}, errors.New("NM broker request has incomplete authority")
	}
	return request, nil
}
