package daemon

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

// tunUplinkFingerprint is the material, non-Podlaz host-network identity that
// makes connect-time TUN connectivity evidence reusable. It deliberately does
// not contain podlaz0, Podlaz policy rules/routes, Podlaz nftables state, or
// Podlaz-scoped DNS state so our own mutations cannot advance the generation.
type tunUplinkFingerprint struct {
	Interface                string
	InterfaceIndex           int
	Gateway                  string
	Addresses                string
	NetworkManagerConnection string
	ServerRouteInterface     string
	ServerRouteGateway       string
}

func deriveTunUplinkFingerprint(snapshot netsnapshot.Snapshot) (tunUplinkFingerprint, error) {
	if snapshot.OS != "" && snapshot.OS != "linux" {
		return tunUplinkFingerprint{}, fmt.Errorf("uplink fingerprint requires linux snapshot, got %q", snapshot.OS)
	}
	if snapshot.DefaultIPv4.Status != netsnapshot.StatusDetected {
		return tunUplinkFingerprint{}, fmt.Errorf("default IPv4 route is %s", snapshot.DefaultIPv4.Status)
	}
	uplink := strings.TrimSpace(snapshot.DefaultIPv4.Interface)
	if uplink == "" || uplink == netsnapshot.DefaultTunName {
		return tunUplinkFingerprint{}, errors.New("default IPv4 route has no trustworthy underlying interface")
	}
	if snapshot.ServerRoute.Status != netsnapshot.StatusDetected {
		return tunUplinkFingerprint{}, fmt.Errorf("server bypass route is %s", snapshot.ServerRoute.Status)
	}
	serverInterface := strings.TrimSpace(snapshot.ServerRoute.Interface)
	if serverInterface == "" || serverInterface == netsnapshot.DefaultTunName {
		return tunUplinkFingerprint{}, errors.New("server bypass route is not proven outside podlaz0")
	}
	if snapshot.IPv4Addresses.Inspection.Status != netsnapshot.StatusDetected {
		return tunUplinkFingerprint{}, fmt.Errorf("IPv4 address inventory is %s", snapshot.IPv4Addresses.Inspection.Status)
	}

	addresses := make([]string, 0, 2)
	for _, address := range snapshot.IPv4Addresses.Addresses {
		if strings.TrimSpace(address.Interface) != uplink {
			continue
		}
		if scope := strings.TrimSpace(address.Scope); scope != "" && scope != "global" {
			continue
		}
		cidr := strings.TrimSpace(address.CIDR)
		if cidr != "" {
			addresses = append(addresses, cidr)
		}
	}
	if len(addresses) == 0 {
		return tunUplinkFingerprint{}, fmt.Errorf("uplink %s has no authoritative global IPv4 address", uplink)
	}
	sort.Strings(addresses)

	interfaceIndex, err := resolvedInterfaceIndex(snapshot.DNS.ResolvedLinks, uplink)
	if err != nil {
		return tunUplinkFingerprint{}, err
	}
	nmIdentity, err := activeNetworkManagerIdentity(snapshot.NetworkManager, uplink)
	if err != nil {
		return tunUplinkFingerprint{}, err
	}

	return tunUplinkFingerprint{
		Interface:                uplink,
		InterfaceIndex:           interfaceIndex,
		Gateway:                  strings.TrimSpace(snapshot.DefaultIPv4.Gateway),
		Addresses:                strings.Join(addresses, ","),
		NetworkManagerConnection: nmIdentity,
		ServerRouteInterface:     serverInterface,
		ServerRouteGateway:       strings.TrimSpace(snapshot.ServerRoute.Gateway),
	}, nil
}

func resolvedInterfaceIndex(links []netsnapshot.ResolvedLink, interfaceName string) (int, error) {
	index := 0
	matches := 0
	for _, link := range links {
		if strings.TrimSpace(link.Name) != interfaceName {
			continue
		}
		value, err := strconv.Atoi(strings.TrimSpace(link.Index))
		if err != nil || value <= 0 {
			return 0, fmt.Errorf("uplink %s has invalid link index", interfaceName)
		}
		matches++
		index = value
	}
	if matches != 1 {
		return 0, fmt.Errorf("uplink %s link identity is ambiguous: matches=%d", interfaceName, matches)
	}
	return index, nil
}

func activeNetworkManagerIdentity(networkManager netsnapshot.NetworkManager, interfaceName string) (string, error) {
	if networkManager.Finding.Status != netsnapshot.StatusDetected {
		return "", nil
	}
	var identity string
	matches := 0
	for _, connection := range networkManager.ActiveConnections {
		if strings.TrimSpace(connection.Device) != interfaceName || !strings.EqualFold(strings.TrimSpace(connection.State), "activated") {
			continue
		}
		matches++
		candidate := strings.TrimSpace(connection.UUID)
		if candidate == "" {
			candidate = strings.TrimSpace(connection.Name)
		}
		if candidate == "" {
			return "", fmt.Errorf("NetworkManager uplink %s has no stable connection identity", interfaceName)
		}
		identity = candidate
	}
	if matches > 1 {
		return "", fmt.Errorf("NetworkManager uplink %s identity is ambiguous: matches=%d", interfaceName, matches)
	}
	return identity, nil
}
