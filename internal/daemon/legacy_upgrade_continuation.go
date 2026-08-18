package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/engine"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

const (
	legacyUpgradeMarkerFileName = "legacy-upgrade-continuation"
	maxLegacyRuntimeConfigBytes = 256 * 1024
	recoveredLegacyProfileName  = "Recovered active session"
)

type legacyXrayTunConfig struct {
	Inbounds  []legacyXrayTunInbound  `json:"inbounds"`
	Outbounds []legacyXrayTunOutbound `json:"outbounds"`
}

type legacyXrayTunInbound struct {
	Tag      string                       `json:"tag"`
	Protocol string                       `json:"protocol"`
	Settings legacyXrayTunInboundSettings `json:"settings"`
}

type legacyXrayTunInboundSettings struct {
	Name      string `json:"name"`
	MTU       int    `json:"MTU"`
	UserLevel int    `json:"userLevel"`
}

type legacyXrayTunOutbound struct {
	Tag            string                        `json:"tag"`
	Protocol       string                        `json:"protocol"`
	Settings       legacyXrayTunOutboundSettings `json:"settings"`
	StreamSettings map[string]json.RawMessage    `json:"streamSettings"`
}

type legacyXrayTunOutboundSettings struct {
	VNext []legacyXrayVNext `json:"vnext"`
}

type legacyXrayVNext struct {
	Address string           `json:"address"`
	Port    uint16           `json:"port"`
	Users   []legacyXrayUser `json:"users"`
}

type legacyXrayUser struct {
	ID         string `json:"id"`
	Encryption string `json:"encryption"`
	Flow       string `json:"flow,omitempty"`
	Level      int    `json:"level"`
}

// migrateLegacyUpgradeContinuation is a deliberately narrow one-time bridge
// for upgrades from releases that predate the continuation record. A package
// maintainer-script marker only authorizes trying the migration in the current
// boot; exact transaction ownership remains the authority for every byte used
// to reconstruct the connect intent.
func migrateLegacyUpgradeContinuation(runtimeDir string, continuation networkSessionContinuationStore) (bool, error) {
	_, exists, err := continuation.LoadCurrent()
	if err != nil {
		return false, err
	}
	if exists {
		_ = removeLegacyUpgradeMarker(runtimeDir)
		return false, nil
	}

	currentMarker, err := legacyUpgradeMarkerIsCurrent(runtimeDir, continuation)
	if err != nil || !currentMarker {
		return false, err
	}

	tx, found, err := exactLegacyUpgradeTransaction(runtimeDir)
	if err != nil {
		return false, err
	}
	if !found {
		if err := removeLegacyUpgradeMarker(runtimeDir); err != nil {
			return false, err
		}
		return false, nil
	}

	request, err := reconstructLegacyTunConnectRequest(runtimeDir, tx)
	if err != nil {
		return false, err
	}
	if err := continuation.Save(request); err != nil {
		return false, fmt.Errorf("persist migrated legacy network session continuation: %w", err)
	}
	if err := removeLegacyUpgradeMarker(runtimeDir); err != nil {
		return false, fmt.Errorf("consume legacy upgrade continuation marker: %w", err)
	}
	return true, nil
}

func legacyUpgradeMarkerIsCurrent(runtimeDir string, continuation networkSessionContinuationStore) (bool, error) {
	path := filepath.Join(runtimeDir, legacyUpgradeMarkerFileName)
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open legacy upgrade continuation marker: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return false, fmt.Errorf("stat legacy upgrade continuation marker: %w", err)
	}
	if info.Mode().Perm() != 0o600 {
		return false, fmt.Errorf("legacy upgrade continuation marker permissions are %o, want 600", info.Mode().Perm())
	}
	data, err := io.ReadAll(io.LimitReader(file, 257))
	if err != nil {
		return false, fmt.Errorf("read legacy upgrade continuation marker: %w", err)
	}
	if len(data) > 256 {
		return false, errors.New("legacy upgrade continuation marker is too large")
	}
	markerBootID := strings.TrimSpace(string(data))
	currentBootID, err := continuation.currentBootID()
	if err != nil {
		return false, err
	}
	if markerBootID == "" || markerBootID != currentBootID {
		if err := removeLegacyUpgradeMarker(runtimeDir); err != nil {
			return false, fmt.Errorf("discard previous-boot legacy upgrade continuation marker: %w", err)
		}
		return false, nil
	}
	return true, nil
}

func removeLegacyUpgradeMarker(runtimeDir string) error {
	path := filepath.Join(runtimeDir, legacyUpgradeMarkerFileName)
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("remove legacy upgrade continuation marker: %w", err)
	}
	if err := syncFilesystemDirectory(runtimeDir); err != nil {
		return fmt.Errorf("sync runtime directory after legacy upgrade marker removal: %w", err)
	}
	return nil
}

func exactLegacyUpgradeTransaction(runtimeDir string) (txstate.Transaction, bool, error) {
	summaries, warnings := txstate.ScanTransactions(runtimeDir)
	if len(warnings) != 0 {
		return txstate.Transaction{}, false, fmt.Errorf("legacy upgrade transaction inspection is incomplete")
	}
	var recoverable []txstate.TransactionSummary
	for _, summary := range summaries {
		if summary.RequiresRecovery {
			recoverable = append(recoverable, summary)
		}
	}
	if len(recoverable) == 0 {
		return txstate.Transaction{}, false, nil
	}
	if len(recoverable) != 1 {
		return txstate.Transaction{}, false, fmt.Errorf("legacy upgrade continuation requires exactly one recoverable transaction, found %d", len(recoverable))
	}
	summary := recoverable[0]
	if summary.State != txstate.TransactionCommitted {
		return txstate.Transaction{}, false, fmt.Errorf("legacy upgrade continuation requires a committed transaction, found %s", summary.State)
	}
	tx, err := txstate.LoadTransactionFile(summary.Path)
	if err != nil {
		return txstate.Transaction{}, false, fmt.Errorf("load legacy upgrade transaction: %w", err)
	}
	if tx.Mode != planner.ModeTun || strings.TrimSpace(tx.ProfileID) == "" {
		return txstate.Transaction{}, false, fmt.Errorf("legacy upgrade continuation requires an exact TUN transaction with profile identity")
	}
	return tx, true, nil
}

func reconstructLegacyTunConnectRequest(runtimeDir string, tx txstate.Transaction) (api.ConnectRequest, error) {
	configPath, err := exactLegacyGeneratedConfigPath(runtimeDir, tx)
	if err != nil {
		return api.ConnectRequest{}, err
	}
	config, err := readPrivateLegacyRuntimeConfig(configPath)
	if err != nil {
		return api.ConnectRequest{}, err
	}
	var doc legacyXrayTunConfig
	if err := json.Unmarshal(config, &doc); err != nil {
		return api.ConnectRequest{}, fmt.Errorf("decode legacy TUN runtime config: %w", err)
	}
	if len(doc.Inbounds) != 1 || len(doc.Outbounds) != 1 {
		return api.ConnectRequest{}, errors.New("legacy TUN runtime config does not have one exact inbound and outbound")
	}
	inbound := doc.Inbounds[0]
	outbound := doc.Outbounds[0]
	if inbound.Tag != "podlaz-tun" || inbound.Protocol != "tun" || outbound.Tag != "podlaz-tun-proxy" || outbound.Protocol != "vless" {
		return api.ConnectRequest{}, errors.New("legacy TUN runtime config does not match the Podlaz-owned Xray schema")
	}
	if inbound.Settings.Name == "" || inbound.Settings.MTU <= 0 || inbound.Settings.UserLevel != 0 {
		return api.ConnectRequest{}, errors.New("legacy TUN runtime config has invalid Podlaz TUN settings")
	}
	if tx.DesiredPlan.TUN.InterfaceName != "" && tx.DesiredPlan.TUN.InterfaceName != inbound.Settings.Name {
		return api.ConnectRequest{}, errors.New("legacy TUN runtime config interface does not match exact transaction plan")
	}
	if tx.DesiredPlan.TUN.MTU > 0 && tx.DesiredPlan.TUN.MTU != inbound.Settings.MTU {
		return api.ConnectRequest{}, errors.New("legacy TUN runtime config MTU does not match exact transaction plan")
	}
	if len(outbound.Settings.VNext) != 1 || len(outbound.Settings.VNext[0].Users) != 1 {
		return api.ConnectRequest{}, errors.New("legacy TUN runtime config does not have one exact VLESS server and user")
	}
	vnext := outbound.Settings.VNext[0]
	user := vnext.Users[0]
	if net.ParseIP(strings.TrimSpace(vnext.Address)).To4() == nil || vnext.Port == 0 || strings.TrimSpace(user.ID) == "" || user.Level != 0 {
		return api.ConnectRequest{}, errors.New("legacy TUN runtime config has invalid VLESS server or user identity")
	}

	server, port, err := exactLegacyServerEndpoint(tx)
	if err != nil {
		return api.ConnectRequest{}, err
	}
	if port != vnext.Port {
		return api.ConnectRequest{}, errors.New("legacy TUN runtime config port does not match exact transaction server metadata")
	}
	p := api.ProfileSnapshot{
		ID:           tx.ProfileID,
		Name:         recoveredLegacyProfileName,
		Source:       string(profile.SourceManual),
		Engine:       string(profile.EngineXray),
		Server:       server,
		Port:         port,
		Protocol:     "vless",
		UserIdentity: user.ID,
		Encryption:   user.Encryption,
		Flow:         user.Flow,
	}
	if err := decodeLegacyStreamSettings(outbound.StreamSettings, &p); err != nil {
		return api.ConnectRequest{}, err
	}
	profileModel := profileFromSnapshot(p)
	if err := engine.ValidateXrayTunProfile(profileModel); err != nil {
		return api.ConnectRequest{}, fmt.Errorf("validate reconstructed legacy TUN profile: %w", err)
	}

	opts := engine.DefaultXrayTunConfigOptions()
	opts.Name = inbound.Settings.Name
	opts.MTU = inbound.Settings.MTU
	opts.OutboundAddressOverride = strings.TrimSpace(vnext.Address)
	regenerated, err := engine.GenerateXrayTunConfig(profileModel, opts)
	if err != nil {
		return api.ConnectRequest{}, fmt.Errorf("regenerate legacy TUN runtime config: %w", err)
	}
	equal, err := canonicalJSONEqual(config, regenerated)
	if err != nil {
		return api.ConnectRequest{}, fmt.Errorf("compare legacy TUN runtime config: %w", err)
	}
	if !equal {
		return api.ConnectRequest{}, errors.New("legacy TUN runtime config cannot be reproduced exactly from transaction-owned continuation data")
	}
	return api.ConnectRequest{Mode: planner.ModeTun, Profile: p, Handoff: api.HandoffBlock}, nil
}

func exactLegacyGeneratedConfigPath(runtimeDir string, tx txstate.Transaction) (string, error) {
	path := filepath.Clean(strings.TrimSpace(tx.DesiredPlan.Core.RuntimeConfigPath))
	if path == "." || path == "" {
		return "", errors.New("legacy committed transaction has no runtime config path")
	}
	generatedRoot := filepath.Clean(filepath.Join(runtimeDir, generatedDirName))
	if path != generatedRoot && !strings.HasPrefix(path, generatedRoot+string(os.PathSeparator)) {
		return "", errors.New("legacy runtime config path is outside Podlaz generated runtime state")
	}
	owned := false
	for _, generated := range tx.Rollback.GeneratedConfigs {
		if generated.Owner == txstate.TransactionOwner && filepath.Clean(generated.Path) == path {
			owned = true
			break
		}
	}
	if !owned {
		return "", errors.New("legacy runtime config is not bound to exact transaction rollback ownership")
	}
	return path, nil
}

func readPrivateLegacyRuntimeConfig(path string) ([]byte, error) {
	linkInfo, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("lstat legacy TUN runtime config: %w", err)
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 || !linkInfo.Mode().IsRegular() {
		return nil, errors.New("legacy TUN runtime config must be a regular non-symlink file")
	}
	if linkInfo.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("legacy TUN runtime config permissions are %o, want 600", linkInfo.Mode().Perm())
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open legacy TUN runtime config: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat legacy TUN runtime config: %w", err)
	}
	if !info.Mode().IsRegular() || !os.SameFile(linkInfo, info) {
		return nil, errors.New("legacy TUN runtime config changed during validation")
	}
	if info.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("legacy TUN runtime config permissions are %o, want 600", info.Mode().Perm())
	}
	data, err := io.ReadAll(io.LimitReader(file, maxLegacyRuntimeConfigBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read legacy TUN runtime config: %w", err)
	}
	if len(data) > maxLegacyRuntimeConfigBytes {
		return nil, fmt.Errorf("legacy TUN runtime config exceeds %d bytes", maxLegacyRuntimeConfigBytes)
	}
	return data, nil
}

func exactLegacyServerEndpoint(tx txstate.Transaction) (string, uint16, error) {
	endpoint, _ := tunDiagnosticServerMetadata(tx)
	host, portText, err := net.SplitHostPort(strings.TrimSpace(endpoint))
	if err != nil || strings.TrimSpace(host) == "" {
		return "", 0, errors.New("legacy committed transaction has no exact diagnostic server endpoint")
	}
	port64, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port64 == 0 {
		return "", 0, errors.New("legacy committed transaction has invalid diagnostic server port")
	}
	return host, uint16(port64), nil
}

func decodeLegacyStreamSettings(settings map[string]json.RawMessage, p *api.ProfileSnapshot) error {
	if p == nil {
		return errors.New("nil legacy profile snapshot")
	}
	network, err := requiredRawString(settings, "network")
	if err != nil {
		return fmt.Errorf("decode legacy VLESS network: %w", err)
	}
	security, err := requiredRawString(settings, "security")
	if err != nil {
		return fmt.Errorf("decode legacy VLESS security: %w", err)
	}
	p.Transport = network
	p.Security = security

	switch network {
	case "raw":
	case "websocket":
		var ws struct {
			Path string `json:"path"`
			Host string `json:"host"`
		}
		if err := optionalRawObject(settings, "wsSettings", &ws); err != nil {
			return err
		}
		p.Path, p.HostHeader = ws.Path, ws.Host
	case "grpc":
		var grpc struct {
			ServiceName string `json:"serviceName"`
			Authority   string `json:"authority"`
		}
		if err := optionalRawObject(settings, "grpcSettings", &grpc); err != nil {
			return err
		}
		p.ServiceName, p.HostHeader = grpc.ServiceName, grpc.Authority
	case "httpupgrade":
		var upgrade struct {
			Path string `json:"path"`
			Host string `json:"host"`
		}
		if err := optionalRawObject(settings, "httpupgradeSettings", &upgrade); err != nil {
			return err
		}
		p.Path, p.HostHeader = upgrade.Path, upgrade.Host
	default:
		return fmt.Errorf("unsupported legacy VLESS transport %q", network)
	}

	switch security {
	case "none":
	case "tls":
		if raw, ok := settings["tlsSettings"]; ok {
			var tls struct {
				ServerName  string   `json:"serverName"`
				Fingerprint string   `json:"fingerprint"`
				ALPN        []string `json:"alpn"`
			}
			if err := json.Unmarshal(raw, &tls); err != nil {
				return fmt.Errorf("decode legacy TLS settings: %w", err)
			}
			p.ServerName, p.Fingerprint, p.ALPN = tls.ServerName, tls.Fingerprint, strings.Join(tls.ALPN, ",")
		}
	case "reality":
		var reality struct {
			ServerName  string `json:"serverName"`
			PublicKey   string `json:"publicKey"`
			Fingerprint string `json:"fingerprint"`
			ShortID     string `json:"shortId"`
			SpiderX     string `json:"spiderX"`
		}
		if err := requiredRawObject(settings, "realitySettings", &reality); err != nil {
			return err
		}
		p.ServerName = reality.ServerName
		p.RealityPublicKey = reality.PublicKey
		p.Fingerprint = reality.Fingerprint
		p.RealityShortID = reality.ShortID
		p.RealitySpiderX = reality.SpiderX
	default:
		return fmt.Errorf("unsupported legacy VLESS security %q", security)
	}
	return nil
}

func requiredRawString(values map[string]json.RawMessage, key string) (string, error) {
	raw, ok := values[key]
	if !ok {
		return "", fmt.Errorf("missing %s", key)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("empty %s", key)
	}
	return strings.TrimSpace(value), nil
}

func optionalRawObject(values map[string]json.RawMessage, key string, target any) error {
	raw, ok := values[key]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("decode legacy %s: %w", key, err)
	}
	return nil
}

func requiredRawObject(values map[string]json.RawMessage, key string, target any) error {
	raw, ok := values[key]
	if !ok {
		return fmt.Errorf("missing legacy %s", key)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("decode legacy %s: %w", key, err)
	}
	return nil
}

func canonicalJSONEqual(left, right []byte) (bool, error) {
	canonicalize := func(data []byte) ([]byte, error) {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			return nil, errors.New("trailing JSON data")
		}
		return json.Marshal(value)
	}
	leftCanonical, err := canonicalize(left)
	if err != nil {
		return false, err
	}
	rightCanonical, err := canonicalize(right)
	if err != nil {
		return false, err
	}
	return bytes.Equal(leftCanonical, rightCanonical), nil
}
