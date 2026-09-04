package snapshot

import "net/netip"

// TunAllocationEvidence is the minimal authoritative host state required to
// choose collision-free identities for a new TUN Network Session. It carries
// typed kernel identities only; diagnostic presentation data belongs to
// Snapshot.
type TunAllocationEvidence struct {
	IPv4Addresses         []netip.Prefix
	IPv4Routes            []TunAllocationRoute
	IPv4PolicyRules       []TunAllocationRule
	ReservedRoutingTables []uint32
}

// TunAllocationRoute carries only the route fields used by collision-sensitive
// allocation. Default routes have Default=true and an invalid Destination.
type TunAllocationRoute struct {
	Destination netip.Prefix
	Default     bool
	Table       uint32
}

// TunAllocationRule carries the numeric identities that can collide with a new
// Podlaz policy rule or routing table. Table may be zero for rules such as the
// kernel l3mdev rule whose lookup table is selected from the matched VRF.
type TunAllocationRule struct {
	Priority uint32
	Table    uint32
}
