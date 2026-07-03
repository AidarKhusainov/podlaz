package sub

import (
	"encoding/json"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/profile"
)

func looksLikeGroupedProviderXrayObject(content []byte) bool {
	object, err := decodeSubscriptionJSONObject(content)
	if err != nil {
		return false
	}
	if _, hasRouting := object["routing"]; !hasRouting {
		return false
	}
	outbounds, ok := providerXrayOutbounds(object)
	if !ok {
		return false
	}
	vlessOutbounds := 0
	for _, outbound := range outbounds {
		if strings.EqualFold(strings.TrimSpace(outbound.Protocol), "vless") {
			vlessOutbounds++
		}
	}
	return vlessOutbounds >= 2
}

func looksLikeProviderXrayObject(content []byte) bool {
	object, err := decodeSubscriptionJSONObject(content)
	if err != nil {
		return false
	}
	outbounds, ok := providerXrayOutbounds(object)
	return ok && len(outbounds) > 0
}

type providerXrayOutboundSummary struct {
	Protocol string `json:"protocol"`
}

func providerXrayOutbounds(object map[string]json.RawMessage) ([]providerXrayOutboundSummary, bool) {
	rawOutbounds, ok := object["outbounds"]
	if !ok {
		return nil, false
	}
	var outbounds []providerXrayOutboundSummary
	if err := json.Unmarshal(rawOutbounds, &outbounds); err != nil {
		return nil, false
	}
	return outbounds, len(outbounds) > 0
}

func parseGroupedProviderXrayProfile(content []byte) (Parsed, error) {
	name, ok := xrayJSONWrapperProfileDisplayName(content)
	if !ok {
		name = "Xray JSON provider profile"
	}
	p, acceptedName, err := profile.NewSubscriptionProviderXrayConfig(name, content)
	if err != nil {
		return Parsed{}, err
	}
	parsed := Parsed{Profiles: []profile.Profile{p}}
	if !acceptedName {
		parsed.Warnings = append(parsed.Warnings, Issue{Line: 1, Message: profile.DisplayNameRejectedWarning})
	}
	return parsed, nil
}
