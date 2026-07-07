package engine

import (
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/profile"
)

func xrayTunOutboundConfig(p profile.Profile, address string, streamSettings map[string]any) map[string]any {
	return map[string]any{
		"tag":      "podlaz-tun-proxy",
		"protocol": "vless",
		"settings": map[string]any{
			"vnext": []map[string]any{{
				"address": address,
				"port":    p.Port,
				"users": []map[string]any{{
					"id":         p.UserIdentity,
					"encryption": vlessEncryption(p),
					"flow":       strings.TrimSpace(p.Flow),
					"level":      0,
				}},
			}},
		},
		"streamSettings": streamSettings,
	}
}
