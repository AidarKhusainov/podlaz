// Package snapshot contains read-only Linux networking snapshots used by planners.
package snapshot

const (
	DefaultTunName      = "podlaz0"
	DefaultNFTFamily    = "inet"
	DefaultNFTTable     = "podlaz"
	DefaultRouteTableID = "51820"
)

type Status string

const (
	StatusUnknown     Status = "unknown"
	StatusUnsupported Status = "unsupported"
	StatusMissing     Status = "missing"
	StatusDetected    Status = "detected"
)

type Finding struct {
	Status  Status `json:"status"`
	Summary string `json:"summary"`
	Detail  string `json:"detail,omitempty"`
}

type Route struct {
	Status      Status `json:"status"`
	Family      string `json:"family,omitempty"`
	Destination string `json:"destination,omitempty"`
	Table       string `json:"table,omitempty"`
	Interface   string `json:"interface,omitempty"`
	Gateway     string `json:"gateway,omitempty"`
	Raw         string `json:"raw,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

type IPAddress struct {
	Family    string `json:"family,omitempty"`
	Interface string `json:"interface"`
	CIDR      string `json:"cidr"`
	Scope     string `json:"scope,omitempty"`
	Raw       string `json:"raw,omitempty"`
}

type IPAddressInventory struct {
	Inspection Finding     `json:"inspection"`
	Addresses  []IPAddress `json:"addresses,omitempty"`
}

type RouteInventory struct {
	Inspection Finding `json:"inspection"`
	Routes     []Route `json:"routes,omitempty"`
}

type PolicyRuleInventory struct {
	Inspection Finding               `json:"inspection"`
	Rules      []PolicyRoutingSignal `json:"rules,omitempty"`
}

type ResolvedLink struct {
	Index            string   `json:"index,omitempty"`
	Name             string   `json:"name"`
	CurrentScopes    []string `json:"current_scopes,omitempty"`
	Protocols        []string `json:"protocols,omitempty"`
	CurrentDNSServer string   `json:"current_dns_server,omitempty"`
	DNSServers       []string `json:"dns_servers,omitempty"`
	DNSDomains       []string `json:"dns_domains,omitempty"`
}

type DNS struct {
	Mode          string         `json:"mode"`
	Resolved      Finding        `json:"systemd_resolved"`
	ResolvedLinks []ResolvedLink `json:"resolved_links,omitempty"`
}

type NetworkManagerConnection struct {
	Name   string `json:"name,omitempty"`
	UUID   string `json:"uuid,omitempty"`
	Type   string `json:"type,omitempty"`
	Device string `json:"device,omitempty"`
	State  string `json:"state,omitempty"`
}

type NetworkManager struct {
	Finding                     Finding                    `json:"finding"`
	State                       string                     `json:"state,omitempty"`
	ActiveConnectionsInspection Finding                    `json:"active_connections_inspection"`
	ActiveConnections           []NetworkManagerConnection `json:"active_connections,omitempty"`
}

type Nftables struct {
	Availability Finding `json:"availability"`
	PodlazTable  Finding `json:"podlaz_table"`
}

type TunDevice struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"`
	Raw    string `json:"raw,omitempty"`
}

type PolicyRoutingSignal struct {
	Kind      string `json:"kind"`
	Priority  string `json:"priority,omitempty"`
	Selector  string `json:"selector,omitempty"`
	Table     string `json:"table,omitempty"`
	Fwmark    string `json:"fwmark,omitempty"`
	Interface string `json:"interface,omitempty"`
	Raw       string `json:"raw,omitempty"`
}

type StaleResource struct {
	Kind               string `json:"kind"`
	Name               string `json:"name"`
	Status             Status `json:"status"`
	Detail             string `json:"detail,omitempty"`
	RecoveryAuthorized bool   `json:"-"`
}

type Snapshot struct {
	OS              string                `json:"os"`
	DefaultIPv4     Route                 `json:"default_ipv4_route"`
	DefaultIPv6     Route                 `json:"default_ipv6_route"`
	ServerRoute     Route                 `json:"server_route"`
	DNS             DNS                   `json:"dns"`
	NetworkManager  NetworkManager        `json:"network_manager"`
	Nftables        Nftables              `json:"nftables"`
	TunDevices      []TunDevice           `json:"tun_devices"`
	PolicyRouting   []PolicyRoutingSignal `json:"policy_routing,omitempty"`
	IPv4PolicyRules PolicyRuleInventory   `json:"ipv4_policy_rules"`
	IPv4Addresses   IPAddressInventory    `json:"ipv4_addresses"`
	IPv4Routes      RouteInventory        `json:"ipv4_routes"`
	IPv4            Finding               `json:"ipv4"`
	IPv6            Finding               `json:"ipv6"`
	StaleResources  []StaleResource       `json:"stale_resources"`
	Warnings        []string              `json:"warnings,omitempty"`
}

func finding(status Status, summary string) Finding {
	return Finding{Status: status, Summary: summary}
}

func findingWithDetail(status Status, summary, detail string) Finding {
	return Finding{Status: status, Summary: summary, Detail: detail}
}
