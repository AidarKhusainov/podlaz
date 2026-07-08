package engine

import (
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/profile"
)

func xrayTunOutboundConfig(p profile.Profile, address string, streamSettings map[string]any) map[string]any {
	user := map[string]any{
		"id":         p.UserIdentity,
		"encryption": vlessEncryption(p),
		"level":      0,
	}
	if flow := strings.TrimSpace(p.Flow); flow != "" {
		user["flow"] = flow
	}
	return map[string]any{
		"tag":      "podlaz-tun-proxy",
		"protocol": "vless",
		"settings": map[string]any{
			"vnext": []map[string]any{{
				"address": address,
				"port":    p.Port,
				"users":   []map[string]any{user},
			}},
		},
		"streamSettings": streamSettings,
	}
}
