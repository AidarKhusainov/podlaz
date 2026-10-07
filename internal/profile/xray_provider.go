package profile

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const fallbackProviderXrayConfigProfileName = "Xray JSON profile"

// NewProviderXrayConfig creates a profile that keeps a native Xray config object
// as sensitive source data. The stored config is canonicalized for deterministic
// IDs and stable profile-store diffs; generated runtime configs remain
// daemon-owned output at connect time.
func NewProviderXrayConfig(rawName string, source SourceType, content []byte) (Profile, bool, error) {
	name, accepted := SanitizeDisplayName(rawName)
	if !accepted {
		name = fallbackProviderXrayConfigProfileName
	}
	canonical, err := canonicalProviderXrayConfigJSON(content)
	if err != nil {
		return Profile{}, accepted, err
	}
	sum := sha256.Sum256(canonical)
	p := Profile{
		ID:             "xray-json-" + hex.EncodeToString(sum[:])[:12],
		Name:           name,
		Source:         source,
		Engine:         EngineXray,
		Protocol:       ProtocolXrayJSON,
		RealitySpiderX: string(canonical),
	}
	if err := Validate(p); err != nil {
		return Profile{}, accepted, err
	}
	return p, accepted, nil
}

func NewSubscriptionProviderXrayConfig(rawName string, content []byte) (Profile, bool, error) {
	return NewProviderXrayConfig(rawName, SourceSubscription, content)
}

func NewImportedFileProviderXrayConfig(rawName string, content []byte) (Profile, bool, error) {
	return NewProviderXrayConfig(rawName, SourceImportedFile, content)
}

// IsProviderXrayConfigProfile reports whether p is a provider-owned Xray config
// profile instead of a flat single-endpoint profile.
func IsProviderXrayConfigProfile(p Profile) bool {
	return strings.EqualFold(strings.TrimSpace(p.Protocol), ProtocolXrayJSON)
}

// ProviderXrayConfigJSON returns the stored provider Xray config source data.
// Stored user profiles keep provider grouped Xray config in RealitySpiderX so
// existing profile output redaction continues to treat it as sensitive source
// configuration.
func ProviderXrayConfigJSON(p Profile) string {
	if strings.EqualFold(strings.TrimSpace(p.Protocol), ProtocolXrayJSON) {
		return p.RealitySpiderX
	}
	return ""
}

func validProviderXrayConfigJSON(value string) bool {
	_, err := canonicalProviderXrayConfigJSON([]byte(value))
	return err == nil
}

func canonicalProviderXrayConfigJSON(content []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(content)))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return nil, fmt.Errorf("malformed provider Xray config: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("malformed provider Xray config: trailing data")
	}
	if object == nil {
		return nil, fmt.Errorf("provider Xray config must be a JSON object")
	}
	out, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("canonicalize provider Xray config: %w", err)
	}
	return out, nil
}
