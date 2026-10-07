package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
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

// ValidateProviderXrayTunProfile checks the structural surface Podlaz must own
// before composing a native TUN inbound. Provider egress details remain opaque.
func ValidateProviderXrayTunProfile(p profile.Profile) error {
	doc, err := validateProviderXrayConfigProfile(p, "TUN-mode")
	if err != nil {
		return err
	}
	return validateProviderXrayOutboundMarks(doc)
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

// GenerateProviderXrayTunConfig composes only Podlaz-owned runtime fields onto
// schema-opaque provider material. Provider outbound/routing/balancer authority
// is retained while every outbound receives the exact Podlaz-owned egress mark.
func GenerateProviderXrayTunConfig(p profile.Profile, opts XrayTunConfigOptions) ([]byte, error) {
	if strings.TrimSpace(opts.Name) == "" {
		return nil, fmt.Errorf("TUN-mode Xray config requires a TUN interface name")
	}
	if opts.MTU <= 0 {
		return nil, fmt.Errorf("TUN-mode Xray config requires a positive MTU")
	}
	if opts.EgressMark == 0 {
		return nil, fmt.Errorf("TUN-mode native Xray config requires a non-zero Podlaz egress mark")
	}
	doc, err := validateProviderXrayConfigProfile(p, "TUN-mode")
	if err != nil {
		return nil, err
	}
	if err := composeProviderXrayEgressMark(doc, opts.EgressMark); err != nil {
		return nil, err
	}
	return renderProviderXrayConfig(doc, []xrayInbound{{
		Tag:      "podlaz-tun",
		Protocol: "tun",
		Settings: xrayTunInboundSettings{Name: opts.Name, MTU: opts.MTU, UserLevel: 0},
	}})
}

func validateProviderXrayOutboundMarks(doc providerXrayConfigDocument) error {
	return walkProviderXrayOutboundMarks(doc, 0, false)
}

func composeProviderXrayEgressMark(doc providerXrayConfigDocument, mark uint32) error {
	if mark == 0 {
		return fmt.Errorf("Podlaz egress mark must be non-zero")
	}
	return walkProviderXrayOutboundMarks(doc, mark, true)
}

func walkProviderXrayOutboundMarks(doc providerXrayConfigDocument, mark uint32, compose bool) error {
	raw := doc["outbounds"]
	var outbounds []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &outbounds); err != nil {
		return fmt.Errorf("malformed TUN-mode grouped Xray outbounds: %w", err)
	}
	for i := range outbounds {
		stream, err := providerXrayObjectField(outbounds[i]["streamSettings"], "streamSettings", i)
		if err != nil {
			return err
		}
		sockopt, err := providerXrayObjectField(stream["sockopt"], "streamSettings.sockopt", i)
		if err != nil {
			return err
		}
		providerMark, err := providerXrayMark(sockopt["mark"], i)
		if err != nil {
			return err
		}
		if compose && providerMark != 0 && providerMark != mark {
			return fmt.Errorf("provider sockopt.mark %d conflicts with Podlaz egress mark %d on outbound %d", providerMark, mark, i+1)
		}
		if !compose {
			continue
		}
		markRaw, _ := json.Marshal(mark)
		sockopt["mark"] = markRaw
		sockoptRaw, err := json.Marshal(sockopt)
		if err != nil {
			return fmt.Errorf("encode TUN-mode grouped Xray outbound %d sockopt: %w", i+1, err)
		}
		stream["sockopt"] = sockoptRaw
		streamRaw, err := json.Marshal(stream)
		if err != nil {
			return fmt.Errorf("encode TUN-mode grouped Xray outbound %d streamSettings: %w", i+1, err)
		}
		outbounds[i]["streamSettings"] = streamRaw
	}
	if compose {
		outboundsRaw, err := json.Marshal(outbounds)
		if err != nil {
			return fmt.Errorf("encode TUN-mode grouped Xray outbounds: %w", err)
		}
		doc["outbounds"] = outboundsRaw
	}
	return nil
}

func providerXrayObjectField(raw json.RawMessage, field string, outboundIndex int) (map[string]json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return map[string]json.RawMessage{}, nil
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return nil, fmt.Errorf("TUN-mode grouped Xray outbound %d %s must be an object", outboundIndex+1, field)
	}
	return value, nil
}

func providerXrayMark(raw json.RawMessage, outboundIndex int) (uint32, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return 0, nil
	}
	value, err := strconv.ParseUint(trimmed, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("TUN-mode grouped Xray outbound %d provider sockopt.mark must be a non-negative 32-bit integer", outboundIndex+1)
	}
	return uint32(value), nil
}
