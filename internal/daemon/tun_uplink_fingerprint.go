package daemon

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

type tunUplinkInterfaceIndexLookup func(string) (int, error)

// tunUplinkFingerprint contains only underlying-uplink evidence. Podlaz-owned
// TUN, policy-routing, DNS and nftables resources are intentionally excluded so
// their own mutations cannot advance the network generation.
type tunUplinkFingerprint struct {
	Interface            string
	InterfaceIndex       int
	Gateway              string
	Addresses            string
	NetworkManagerID     string
	ServerRouteInterface string
	ServerRouteGateway   string
}

func deriveTunUplinkFingerprint(s netsnapshot.Snapshot, indexLookup tunUplinkInterfaceIndexLookup) (tunUplinkFingerprint, error) {
	if s.DefaultIPv4.Status != netsnapshot.StatusDetected {
		return tunUplinkFingerprint{}, fmt.Errorf("default IPv4 route is %s", s.DefaultIPv4.Status)
	}
	uplink := strings.TrimSpace(s.DefaultIPv4.Interface)
	if uplink == "" || uplink == netsnapshot.DefaultTunName {
		return tunUplinkFingerprint{}, fmt.Errorf("invalid underlying default-route interface %q", uplink)
	}
	if indexLookup == nil {
		return tunUplinkFingerprint{}, errors.New("missing uplink interface-index lookup")
	}
	ifindex, err := indexLookup(uplink)
	if err != nil {
		return tunUplinkFingerprint{}, fmt.Errorf("inspect uplink interface index for %s: %w", uplink, err)
	}
	if ifindex <= 0 {
		return tunUplinkFingerprint{}, fmt.Errorf("invalid uplink interface index %d for %s", ifindex, uplink)
	}

	if s.IPv4Addresses.Inspection.Status != netsnapshot.StatusDetected {
		return tunUplinkFingerprint{}, fmt.Errorf("IPv4 address inspection is %s", s.IPv4Addresses.Inspection.Status)
	}
	addresses := make([]string, 0)
	for _, address := range s.IPv4Addresses.Addresses {
		if strings.TrimSpace(address.Interface) != uplink || strings.TrimSpace(address.Family) != "ipv4" {
			continue
		}
		if strings.TrimSpace(address.Scope) != "" && strings.TrimSpace(address.Scope) != "global" {
			continue
		}
		cidr := strings.TrimSpace(address.CIDR)
		if cidr != "" {
			addresses = append(addresses, cidr)
		}
	}
	if len(addresses) == 0 {
		return tunUplinkFingerprint{}, fmt.Errorf("no global IPv4 address detected on uplink %s", uplink)
	}
	sort.Strings(addresses)
	addresses = compactSortedStrings(addresses)

	if s.ServerRoute.Status != netsnapshot.StatusDetected {
		return tunUplinkFingerprint{}, fmt.Errorf("server route is %s", s.ServerRoute.Status)
	}
	serverInterface := strings.TrimSpace(s.ServerRoute.Interface)
	if serverInterface == "" || serverInterface == netsnapshot.DefaultTunName {
		return tunUplinkFingerprint{}, fmt.Errorf("invalid server-route interface %q", serverInterface)
	}
	if serverInterface != uplink {
		return tunUplinkFingerprint{}, fmt.Errorf("server route uses %s while default uplink is %s", serverInterface, uplink)
	}

	nmID, err := activeNetworkManagerConnectionID(s.NetworkManager, uplink)
	if err != nil {
		return tunUplinkFingerprint{}, err
	}

	return tunUplinkFingerprint{
		Interface:            uplink,
		InterfaceIndex:       ifindex,
		Gateway:              strings.TrimSpace(s.DefaultIPv4.Gateway),
		Addresses:            strings.Join(addresses, ","),
		NetworkManagerID:     nmID,
		ServerRouteInterface: serverInterface,
		ServerRouteGateway:   strings.TrimSpace(s.ServerRoute.Gateway),
	}, nil
}

func activeNetworkManagerConnectionID(nm netsnapshot.NetworkManager, uplink string) (string, error) {
	if nm.Finding.Status != netsnapshot.StatusDetected {
		return "", nil
	}
	var matches []netsnapshot.NetworkManagerConnection
	for _, connection := range nm.ActiveConnections {
		if strings.TrimSpace(connection.Device) == uplink && strings.EqualFold(strings.TrimSpace(connection.State), "activated") {
			matches = append(matches, connection)
		}
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("multiple active NetworkManager connections claim uplink %s", uplink)
	}
	if len(matches) == 0 {
		return "", nil
	}
	connection := matches[0]
	identity := strings.TrimSpace(connection.UUID)
	if identity == "" {
		identity = strings.TrimSpace(connection.Name)
	}
	if identity == "" {
		return "", fmt.Errorf("active NetworkManager connection on %s has no identity", uplink)
	}
	return identity, nil
}

func compactSortedStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}
