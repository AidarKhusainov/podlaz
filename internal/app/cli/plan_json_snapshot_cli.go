package cli

import (
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/render"
)

func snapshotForJSON(s netsnapshot.Snapshot) map[string]any {
	return map[string]any{
		"os":                 render.Redact(s.OS),
		"default_ipv4_route": routeForJSON(s.DefaultIPv4),
		"default_ipv6_route": routeForJSON(s.DefaultIPv6),
		"server_route":       routeForJSON(s.ServerRoute),
		"dns": map[string]any{
			"mode":             render.Redact(s.DNS.Mode),
			"systemd_resolved": findingForJSON(s.DNS.Resolved),
		},
		"network_manager": map[string]any{
			"finding": findingForJSON(s.NetworkManager.Finding),
			"state":   render.Redact(s.NetworkManager.State),
		},
		"nftables": map[string]any{
			"availability": findingForJSON(s.Nftables.Availability),
			"podlaz_table": findingForJSON(s.Nftables.PodlazTable),
		},
		"tun_devices":     tunDevicesForJSON(s.TunDevices),
		"ipv4_addresses":  ipv4AddressInventoryForJSON(s.IPv4Addresses),
		"ipv4_routes":     ipv4RouteInventoryForJSON(s.IPv4Routes),
		"ipv4":            findingForJSON(s.IPv4),
		"ipv6":            findingForJSON(s.IPv6),
		"stale_resources": staleResourcesForJSON(s.StaleResources),
	}
}

func routeForJSON(r netsnapshot.Route) map[string]any {
	return map[string]any{
		"status":      string(r.Status),
		"family":      render.Redact(r.Family),
		"destination": render.Redact(r.Destination),
		"table":       render.Redact(r.Table),
		"interface":   render.Redact(r.Interface),
		"gateway":     render.Redact(r.Gateway),
		"raw":         render.Redact(r.Raw),
		"detail":      render.Redact(r.Detail),
	}
}

func ipv4AddressInventoryForJSON(v netsnapshot.IPAddressInventory) map[string]any {
	addresses := make([]map[string]any, len(v.Addresses))
	for i, address := range v.Addresses {
		addresses[i] = map[string]any{
			"family":    render.Redact(address.Family),
			"interface": render.Redact(address.Interface),
			"cidr":      render.Redact(address.CIDR),
			"scope":     render.Redact(address.Scope),
		}
	}
	return map[string]any{"inspection": findingForJSON(v.Inspection), "addresses": addresses}
}

func ipv4RouteInventoryForJSON(v netsnapshot.RouteInventory) map[string]any {
	routes := make([]map[string]any, len(v.Routes))
	for i, route := range v.Routes {
		routes[i] = routeForJSON(route)
	}
	return map[string]any{"inspection": findingForJSON(v.Inspection), "routes": routes}
}

func findingForJSON(f netsnapshot.Finding) map[string]any {
	return map[string]any{
		"status":  string(f.Status),
		"summary": render.Redact(f.Summary),
		"detail":  render.Redact(f.Detail),
	}
}

func tunDevicesForJSON(v []netsnapshot.TunDevice) []map[string]any {
	out := make([]map[string]any, len(v))
	for i, d := range v {
		out[i] = map[string]any{
			"name":   render.Redact(d.Name),
			"status": string(d.Status),
			"detail": render.Redact(d.Detail),
			"raw":    render.Redact(d.Raw),
		}
	}
	return out
}

func staleResourcesForJSON(v []netsnapshot.StaleResource) []map[string]any {
	out := make([]map[string]any, len(v))
	for i, r := range v {
		out[i] = map[string]any{
			"kind":   render.Redact(r.Kind),
			"name":   render.Redact(r.Name),
			"status": string(r.Status),
			"detail": render.Redact(r.Detail),
		}
	}
	return out
}
