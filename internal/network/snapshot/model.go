// Package snapshot contains read-only Linux networking snapshots used by planners.
package snapshot

const (
	DefaultTunName   = "tunwarden0"
	DefaultNFTFamily = "inet"
	DefaultNFTTable  = "tunwarden"
)

// Status describes whether a read-only observation was available.
type Status string

const (
	StatusUnknown     Status = "unknown"
	StatusUnsupported Status = "unsupported"
	StatusMissing     Status = "missing"
	StatusDetected    Status = "detected"
)

// Finding is a generic read-only observation with a stable status vocabulary.
type Finding struct {
	Status  Status `json:"status"`
	Summary string `json:"summary"`
	Detail  string `json:"detail,omitempty"`
}

// Route describes a route observation from the host routing table.
type Route struct {
	Status      Status `json:"status"`
	Family      string `json:"family,omitempty"`
	Destination string `json:"destination,omitempty"`
	Interface   string `json:"interface,omitempty"`
	Gateway     string `json:"gateway,omitempty"`
	Raw         string `json:"raw,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

// DNS describes DNS backend state relevant to future full-tunnel DNS planning.
type DNS struct {
	Mode     string  `json:"mode"`
	Resolved Finding `json:"systemd_resolved"`
}

// NetworkManager describes NetworkManager availability and advisory state.
type NetworkManager struct {
	Finding Finding `json:"finding"`
	State   string  `json:"state,omitempty"`
}

// Nftables describes nftables availability and TunWarden-owned table presence.
type Nftables struct {
	Availability   Finding `json:"availability"`
	TunWardenTable Finding `json:"tunwarden_table"`
}

// TunDevice describes a known TunWarden TUN interface name.
type TunDevice struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"`
	Raw    string `json:"raw,omitempty"`
}

// StaleResource describes detected TunWarden-owned system state from a read-only snapshot.
type StaleResource struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Snapshot is the host state input for TUN planners.
type Snapshot struct {
	OS             string          `json:"os"`
	DefaultIPv4    Route           `json:"default_ipv4_route"`
	DefaultIPv6    Route           `json:"default_ipv6_route"`
	ServerRoute    Route           `json:"server_route"`
	DNS            DNS             `json:"dns"`
	NetworkManager NetworkManager  `json:"network_manager"`
	Nftables       Nftables        `json:"nftables"`
	TunDevices     []TunDevice     `json:"tun_devices"`
	IPv4           Finding         `json:"ipv4"`
	IPv6           Finding         `json:"ipv6"`
	StaleResources []StaleResource `json:"stale_resources"`
	Warnings       []string        `json:"warnings,omitempty"`
}

func finding(status Status, summary string) Finding {
	return Finding{Status: status, Summary: summary}
}

func findingWithDetail(status Status, summary, detail string) Finding {
	return Finding{Status: status, Summary: summary, Detail: detail}
}
