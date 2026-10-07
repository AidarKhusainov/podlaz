package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/profile"
)

type providerXrayConfigDocument map[string]json.RawMessage

// ValidateProviderXrayProxyOnlyProfile checks whether a provider-owned grouped
// Xray config can be rendered with podlaz proxy-only inbounds while preserving
// provider outbounds and routing/selection semantics.
func ValidateProviderXrayProxyOnlyProfile(p profile.Profile) error {
	_, err := validateProviderXrayConfigProfile(p, "proxy-only")
	return err
}

// GenerateProviderXrayProxyOnlyConfig builds a runtime Xray config from a stored
// provider config object. Provider outbounds and routing stay provider-owned;
// provider inbounds are replaced with podlaz local proxy listeners.
func GenerateProviderXrayProxyOnlyConfig(p profile.Profile, opts XrayProxyOnlyConfigOptions) ([]byte, error) {
	if opts.SOCKSListen == "" {
		return nil, fmt.Errorf("proxy-only Xray config requires a SOCKS listen address")
	}
	if opts.SOCKSPort == 0 {
		return nil, fmt.Errorf("proxy-only Xray config requires a SOCKS listen port")
	}
	if opts.HTTPListen == "" {
		return nil, fmt.Errorf("proxy-only Xray config requires an HTTP listen address")
	}
	if opts.HTTPPort == 0 {
		return nil, fmt.Errorf("proxy-only Xray config requires an HTTP listen port")
	}
	doc, err := validateProviderXrayConfigProfile(p, "proxy-only")
	if err != nil {
		return nil, err
	}
	return renderProviderXrayConfig(doc, []xrayInbound{
		{
			Tag:      "podlaz-socks",
			Listen:   opts.SOCKSListen,
			Port:     opts.SOCKSPort,
			Protocol: "socks",
			Settings: xraySOCKSInboundSettings{Auth: "noauth", UDP: false, UserLevel: 0},
		},
		{
			Tag:      "podlaz-http",
			Listen:   opts.HTTPListen,
			Port:     opts.HTTPPort,
			Protocol: "http",
			Settings: xrayHTTPInboundSettings{AllowTransparent: false, UserLevel: 0},
		},
	})
}

func validateProviderXrayConfigProfile(p profile.Profile, modeName string) (providerXrayConfigDocument, error) {
	if err := profile.Validate(p); err != nil {
		return nil, err
	}
	if p.Engine != profile.EngineXray {
		return nil, fmt.Errorf("%s grouped Xray config requires engine %q, got %q", modeName, profile.EngineXray, p.Engine)
	}
	if !profile.IsProviderXrayConfigProfile(p) {
		return nil, fmt.Errorf("%s grouped Xray config requires protocol %q", modeName, profile.ProtocolXrayJSON)
	}
	doc, err := decodeProviderXrayConfig(profile.ProviderXrayConfigJSON(p))
	if err != nil {
		return nil, err
	}
	if err := validateProviderXrayOutbounds(doc, modeName); err != nil {
		return nil, err
	}
	if err := rejectProviderXrayRoutingInboundTags(doc, modeName); err != nil {
		return nil, err
	}
	return doc, nil
}

func decodeProviderXrayConfig(raw string) (providerXrayConfigDocument, error) {
	decoder := json.NewDecoder(bytes.NewReader([]byte(strings.TrimSpace(raw))))
	decoder.UseNumber()
	var doc providerXrayConfigDocument
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("malformed provider Xray config: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("malformed provider Xray config: trailing data")
	}
	if doc == nil {
		return nil, fmt.Errorf("provider Xray config must be a JSON object")
	}
	return doc, nil
}

func validateProviderXrayOutbounds(doc providerXrayConfigDocument, modeName string) error {
	raw, ok := doc["outbounds"]
	if !ok || len(bytes.TrimSpace(raw)) == 0 {
		return fmt.Errorf("%s grouped Xray config requires provider outbounds", modeName)
	}
	var outbounds []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &outbounds); err != nil {
		return fmt.Errorf("malformed %s grouped Xray outbounds: %w", modeName, err)
	}
	if len(outbounds) == 0 {
		return fmt.Errorf("%s grouped Xray config requires at least one provider outbound", modeName)
	}
	return nil
}

func rejectProviderXrayRoutingInboundTags(doc providerXrayConfigDocument, modeName string) error {
	raw, ok := doc["routing"]
	if !ok || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var routing struct {
		Rules []map[string]json.RawMessage `json:"rules"`
	}
	if err := json.Unmarshal(raw, &routing); err != nil {
		return fmt.Errorf("malformed %s grouped Xray routing: %w", modeName, err)
	}
	for i, rule := range routing.Rules {
		if nonEmptyJSONField(rule["inboundTag"]) || nonEmptyJSONField(rule["inboundTags"]) {
			return fmt.Errorf("unsupported %s grouped Xray routing rule %d: inboundTag is not supported because podlaz replaces provider inbounds", modeName, i+1)
		}
	}
	return nil
}

func nonEmptyJSONField(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || bytes.Equal(trimmed, []byte("[]")) || bytes.Equal(trimmed, []byte(`""`)) {
		return false
	}
	return true
}

func renderProviderXrayConfig(doc providerXrayConfigDocument, inbounds []xrayInbound) ([]byte, error) {
	rendered := make(providerXrayConfigDocument, len(doc)+2)
	for key, raw := range doc {
		rendered[key] = append(json.RawMessage(nil), raw...)
	}
	logRaw, err := json.Marshal(xrayLog{LogLevel: "warning"})
	if err != nil {
		return nil, fmt.Errorf("encode grouped Xray log settings: %w", err)
	}
	inboundsRaw, err := json.Marshal(inbounds)
	if err != nil {
		return nil, fmt.Errorf("encode grouped Xray inbounds: %w", err)
	}
	rendered["log"] = logRaw
	rendered["inbounds"] = inboundsRaw

	out, err := json.MarshalIndent(rendered, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode grouped Xray config: %w", err)
	}
	return append(out, '\n'), nil
}

func unsupportedProviderXrayTunModeError() error {
	return fmt.Errorf("TUN-mode native Xray profiles are not supported yet: endpoint-independent Xray egress is not integrated")
}
