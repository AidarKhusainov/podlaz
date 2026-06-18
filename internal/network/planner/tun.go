package planner

import (
	"fmt"
	"net"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/profile"
)

const (
	ModeTun = "tun"

	TunTunnelMode       = "full-tunnel"
	DefaultTunMTU       = 1500
	TunRoutingTable     = "podlaz"
	TunRoutingTableID   = 51820
	TunRulePriority     = 10000
	ServerRulePriority  = 9999
	MainRoutingTable    = "main"
	IPv4DefaultRoute    = "default"
	IPv4DefaultSelector = "from all"

	DNSBackendSystemdResolved = "systemd-resolved per-link DNS"
	DNSActionConfigure        = "configure"
	DNSActionBlocked          = "blocked"
	DNSRollbackRestore        = "restore previous per-link DNS state where possible"
	DefaultTunDNSServer       = "1.1.1.1"

	FirewallBackendNftables    = "nftables"
	FirewallTableAction        = "create"
	FirewallActionAdd          = "add"
	FirewallActionBlocked      = "blocked"
	FirewallActionSkip         = "skip"
	FirewallActionValidate     = "validate-or-replace"
	FirewallRollbackRemove     = "remove inet podlaz"
	FirewallOutputChain        = "output"
	FirewallChainTypeFilter    = "filter"
	FirewallOutputHook         = "output"
	FirewallOutputPriority     = 0
	FirewallDefaultChainPolicy = "accept"
	FirewallVerdictAccept      = "accept"
	FirewallVerdictReject      = "reject"
	FirewallVerdictDrop        = "drop"
	FirewallServerBypassOwner  = "podlaz:firewall:server-bypass"
	FirewallLoopbackOwner      = "podlaz:firewall:loopback"
	FirewallTunEgressOwner     = "podlaz:firewall:tun-egress"
	FirewallKillSwitchOwner    = "podlaz:firewall:kill-switch"
	FirewallServerBypassKey    = "inet/podlaz/output/server-bypass"
	FirewallLoopbackKey        = "inet/podlaz/output/loopback"
	FirewallTunEgressKey       = "inet/podlaz/output/tun-egress"
	FirewallKillSwitchKey      = "inet/podlaz/output/kill-switch"

	KillSwitchPolicyOff    = "off"
	KillSwitchPolicySoft   = "soft"
	KillSwitchPolicyStrict = "strict"
)

type TunOptions struct {
	KillSwitchPolicy string
	DNSServers       []string
}

type TunDevicePlan struct {
	Name   string
	MTU    int
	Action string
	Reason string
}

type TunRoutePlan struct {
	Family      string
	Destination string
	Table       string
	Interface   string
	Gateway     string
	Action      string
	Reason      string
}

type TunPolicyRulePlan struct {
	Family   string
	Priority int
	Selector string
	Table    string
	Action   string
	Reason   string
}

type TunDNSPlan struct {
	Backend       string
	TargetLink    string
	Servers       []string
	Action        string
	Reason        string
	Rollback      string
	RollbackSteps []string
}

type TunFirewallPlan struct {
	Backend       string
	Family        string
	Table         string
	TableAction   string
	Chains        []TunFirewallChainPlan
	Rules         []TunFirewallRulePlan
	KillSwitch    TunKillSwitchPlan
	Reason        string
	Rollback      string
	RollbackSteps []string
}

type TunFirewallChainPlan struct {
	Name     string
	Type     string
	Hook     string
	Priority int
	Policy   string
	Action   string
	Reason   string
}

type TunFirewallRulePlan struct {
	Chain       string
	Expr        string
	Verdict     string
	Action      string
	Reason      string
	Ownership   string
	RollbackKey string
}

type TunKillSwitchPlan struct {
	Policy      string
	Action      string
	Scope       string
	Recovery    string
	Limitations []string
}

type TunPlan struct {
	Mode          string
	TunnelMode    string
	ProfileID     string
	ProfileName   string
	Snapshot      snapshot.Snapshot
	TunDevice     TunDevicePlan
	Routes        []TunRoutePlan
	PolicyRules   []TunPolicyRulePlan
	ServerBypass  TunRoutePlan
	DNS           TunDNSPlan
	Firewall      TunFirewallPlan
	LoopRisks     []string
	Warnings      []string
	Steps         []string
	RollbackSteps []string
}

func PlanTun(p profile.Profile, s snapshot.Snapshot) (TunPlan, error) {
	return PlanTunWithOptions(p, s, TunOptions{})
}

func PlanTunWithOptions(p profile.Profile, s snapshot.Snapshot, opts TunOptions) (TunPlan, error) {
	if err := profile.Validate(p); err != nil {
		return TunPlan{}, err
	}

	device := TunDevicePlan{Name: snapshot.DefaultTunName, MTU: DefaultTunMTU, Action: "create", Reason: "stable podlaz full-tunnel interface"}
	serverIP := concreteServerBypassIP(s)
	serverBypass := serverBypassRoute(s, serverIP)
	routes := []TunRoutePlan{{
		Family:      "ipv4",
		Destination: IPv4DefaultRoute,
		Table:       TunRoutingTable,
		Interface:   snapshot.DefaultTunName,
		Action:      "add",
		Reason:      "route default IPv4 traffic through the podlaz TUN interface",
	}}
	policyRules := []TunPolicyRulePlan{{
		Family:   "ipv4",
		Priority: TunRulePriority,
		Selector: IPv4DefaultSelector,
		Table:    TunRoutingTable,
		Action:   "add",
		Reason:   "send default IPv4 traffic through the podlaz routing table before the kernel main table rule",
	}}
	if serverIP != "" {
		routes = append(routes, serverBypass)
		policyRules = append([]TunPolicyRulePlan{{
			Family:   "ipv4",
			Priority: ServerRulePriority,
			Selector: "to " + serverIP + "/32",
			Table:    MainRoutingTable,
			Action:   "add",
			Reason:   "keep VPN server traffic on the current uplink before the full-tunnel policy rule",
		}}, policyRules...)
	}

	dnsPlan := dnsPlan(s, device, normalizeDNSServers(opts.DNSServers))
	firewallPlan := firewallPlan(s, normalizeKillSwitchPolicy(opts.KillSwitchPolicy), device, serverIP)

	loopRisks := tunRouteLoopRisks(s)
	warnings := append([]string{}, s.Warnings...)
	warnings = append(warnings, tunSnapshotWarnings(s)...)
	warnings = append(warnings, tunDesiredStateWarnings(s, serverIP)...)
	warnings = append(warnings, dnsPlanWarnings(s, dnsPlan)...)
	warnings = append(warnings, firewallPlanWarnings(s, firewallPlan)...)
	warnings = append(warnings, loopRisks...)

	steps := []string{
		"Collect current host networking snapshot without requiring root",
		fmt.Sprintf("Plan TUN interface %s with MTU %d", device.Name, device.MTU),
		fmt.Sprintf("Plan routing table %s (%d) with IPv4 default route through %s", TunRoutingTable, TunRoutingTableID, device.Name),
	}
	if serverIP != "" {
		steps = append(steps, fmt.Sprintf("Plan policy rule priority %d for VPN server bypass via %s", ServerRulePriority, MainRoutingTable))
	}
	steps = append(steps,
		fmt.Sprintf("Plan policy rule priority %d for default IPv4 traffic via %s", TunRulePriority, TunRoutingTable),
		fmt.Sprintf("Plan DNS backend %s on link %s with server(s) %s", dnsPlan.Backend, dnsPlan.TargetLink, strings.Join(dnsPlan.Servers, ", ")),
		fmt.Sprintf("Plan nftables table %s %s with %d chain(s), %d rule(s), and %s kill-switch policy", firewallPlan.Family, firewallPlan.Table, len(firewallPlan.Chains), len(firewallPlan.Rules), firewallPlan.KillSwitch.Policy),
		"Leave TUN devices, routes, policy rules, DNS, nftables, firewall, and Xray process state unchanged in this dry-run",
	)

	return TunPlan{
		Mode:          ModeTun,
		TunnelMode:    TunTunnelMode,
		ProfileID:     p.ID,
		ProfileName:   p.Name,
		Snapshot:      s,
		TunDevice:     device,
		Routes:        routes,
		PolicyRules:   policyRules,
		ServerBypass:  serverBypass,
		DNS:           dnsPlan,
		Firewall:      firewallPlan,
		LoopRisks:     loopRisks,
		Warnings:      compactWarnings(warnings),
		Steps:         steps,
		RollbackSteps: rollbackSteps(device, routes, policyRules, dnsPlan, firewallPlan),
	}, nil
}

func serverBypassRoute(s snapshot.Snapshot, serverIP string) TunRoutePlan {
	if serverIP == "" {
		return TunRoutePlan{Family: "ipv4", Destination: "<server-ip>", Table: MainRoutingTable, Action: "blocked", Reason: "server route did not resolve to a concrete IP address"}
	}
	return TunRoutePlan{Family: "ipv4", Destination: serverIP + "/32", Table: MainRoutingTable, Interface: s.DefaultIPv4.Interface, Gateway: s.DefaultIPv4.Gateway, Action: "add", Reason: "pin VPN server traffic to the current default uplink outside the TUN path"}
}

func dnsPlan(s snapshot.Snapshot, device TunDevicePlan, servers []string) TunDNSPlan {
	plan := TunDNSPlan{
		Backend:    DNSBackendSystemdResolved,
		TargetLink: device.Name,
		Servers:    append([]string{}, servers...),
		Action:     DNSActionConfigure,
		Reason:     "use systemd-resolved per-link DNS for full-tunnel DNS routing without writing /etc/resolv.conf",
		Rollback:   DNSRollbackRestore,
		RollbackSteps: []string{
			fmt.Sprintf("Restore previous systemd-resolved per-link DNS state for %s where possible", device.Name),
			fmt.Sprintf("Flush podlaz-owned DNS server/default-route/domain settings from %s if created by this transaction", device.Name),
		},
	}
	if s.DNS.Resolved.Status != snapshot.StatusDetected {
		plan.Action = DNSActionBlocked
		plan.Reason = fmt.Sprintf("systemd-resolved state is %s; DNS mutation is unsafe until the backend is detected", s.DNS.Resolved.Status)
	}
	return plan
}

func firewallPlan(s snapshot.Snapshot, policy string, device TunDevicePlan, serverIP string) TunFirewallPlan {
	tableAction := FirewallTableAction
	reason := "create a podlaz-owned nftables table for full-tunnel leak prevention dry-run state"
	if s.Nftables.Availability.Status != snapshot.StatusDetected {
		tableAction = FirewallActionBlocked
		reason = fmt.Sprintf("nftables availability is %s; firewall mutation is unsafe until nft can be inspected", s.Nftables.Availability.Status)
	}
	if s.Nftables.PodlazTable.Status == snapshot.StatusDetected {
		tableAction = FirewallActionValidate
		reason = "podlaz nftables table already exists; future apply must validate ownership or recover it before replacing rules"
	}

	ruleAction := firewallRuleAction(tableAction)
	return TunFirewallPlan{
		Backend:     FirewallBackendNftables,
		Family:      snapshot.DefaultNFTFamily,
		Table:       snapshot.DefaultNFTTable,
		TableAction: tableAction,
		Chains: []TunFirewallChainPlan{{
			Name:     FirewallOutputChain,
			Type:     FirewallChainTypeFilter,
			Hook:     FirewallOutputHook,
			Priority: FirewallOutputPriority,
			Policy:   FirewallDefaultChainPolicy,
			Action:   tableAction,
			Reason:   "own outbound full-tunnel filtering inside the podlaz nftables table",
		}},
		Rules:      firewallRules(policy, device, serverIP, ruleAction),
		KillSwitch: killSwitchPlan(policy, device),
		Reason:     reason,
		Rollback:   FirewallRollbackRemove,
		RollbackSteps: []string{
			fmt.Sprintf("Remove nftables table %s %s if created by this transaction", snapshot.DefaultNFTFamily, snapshot.DefaultNFTTable),
		},
	}
}

func firewallRuleAction(tableAction string) string {
	switch tableAction {
	case FirewallTableAction:
		return FirewallActionAdd
	case FirewallActionBlocked:
		return FirewallActionBlocked
	default:
		return tableAction
	}
}

func firewallRules(policy string, device TunDevicePlan, serverIP, action string) []TunFirewallRulePlan {
	rules := []TunFirewallRulePlan{serverBypassFirewallRule(serverIP, action), loopbackFirewallRule(action), tunEgressFirewallRule(device, action)}
	if rule := killSwitchFirewallRule(policy, device, action); rule.Action != "" {
		rules = append(rules, rule)
	}
	return rules
}

func serverBypassFirewallRule(serverIP, action string) TunFirewallRulePlan {
	rule := TunFirewallRulePlan{
		Chain:       FirewallOutputChain,
		Expr:        "ip daddr " + serverIP,
		Verdict:     FirewallVerdictAccept,
		Action:      action,
		Reason:      "allow VPN server control traffic outside TUN before non-TUN blocking",
		Ownership:   FirewallServerBypassOwner,
		RollbackKey: FirewallServerBypassKey,
	}
	if serverIP == "" {
		rule.Expr = "ip daddr <server-ip>"
		rule.Action = FirewallActionBlocked
		rule.Reason = "VPN server bypass target is unknown; firewall bypass rule cannot be applied safely"
	}
	return rule
}

func loopbackFirewallRule(action string) TunFirewallRulePlan {
	return TunFirewallRulePlan{
		Chain:       FirewallOutputChain,
		Expr:        `oifname "lo"`,
		Verdict:     FirewallVerdictAccept,
		Action:      action,
		Reason:      "allow local loopback traffic so systemd-resolved stub DNS and local proxy IPC are not blocked by the TUN kill-switch",
		Ownership:   FirewallLoopbackOwner,
		RollbackKey: FirewallLoopbackKey,
	}
}

func tunEgressFirewallRule(device TunDevicePlan, action string) TunFirewallRulePlan {
	return TunFirewallRulePlan{
		Chain:       FirewallOutputChain,
		Expr:        fmt.Sprintf("oifname %q", device.Name),
		Verdict:     FirewallVerdictAccept,
		Action:      action,
		Reason:      "allow traffic that egresses through the podlaz TUN interface",
		Ownership:   FirewallTunEgressOwner,
		RollbackKey: FirewallTunEgressKey,
	}
}

func killSwitchFirewallRule(policy string, device TunDevicePlan, action string) TunFirewallRulePlan {
	rule := TunFirewallRulePlan{
		Chain:       FirewallOutputChain,
		Expr:        fmt.Sprintf("oifname != %q", device.Name),
		Action:      action,
		Reason:      "block non-TUN traffic according to selected kill-switch policy",
		Ownership:   FirewallKillSwitchOwner,
		RollbackKey: FirewallKillSwitchKey,
	}
	switch policy {
	case KillSwitchPolicyOff:
		rule.Action = FirewallActionSkip
		rule.Verdict = ""
		rule.Reason = "kill-switch policy is off; do not install a non-TUN blocking rule"
	case KillSwitchPolicyStrict:
		rule.Verdict = FirewallVerdictDrop
		rule.Reason = "strict kill-switch drops non-TUN traffic until recovery removes podlaz-owned rules"
	default:
		rule.Verdict = FirewallVerdictReject
		rule.Reason = "soft kill-switch rejects non-TUN traffic during transition and must restore direct connectivity on failure"
	}
	if action == FirewallActionBlocked && policy != KillSwitchPolicyOff {
		rule.Action = FirewallActionBlocked
	}
	return rule
}

func killSwitchPlan(policy string, device TunDevicePlan) TunKillSwitchPlan {
	plan := TunKillSwitchPlan{
		Policy:   policy,
		Action:   "block non-TUN traffic according to selected kill-switch policy",
		Scope:    fmt.Sprintf("allow traffic through %s, local loopback, and the explicit VPN server bypass; block other non-TUN traffic according to policy", device.Name),
		Recovery: "recover --execute --yes must be able to remove podlaz-owned nftables state",
	}
	switch policy {
	case KillSwitchPolicyOff:
		plan.Action = "do not install leak-blocking nftables rules"
		plan.Scope = "firewall table ownership is still planned for future cleanup visibility, but no traffic blocking is selected"
		plan.Limitations = []string{"off policy does not claim leak protection"}
	case KillSwitchPolicyStrict:
		plan.Limitations = []string{
			"strict kill-switch may intentionally keep direct connectivity blocked after VPN failure until recovery removes podlaz-owned rules",
			"strict kill-switch cannot be claimed as leak protection until apply, verify, rollback, and recover execution exist",
		}
	default:
		plan.Limitations = []string{"soft kill-switch is a transition guard only and must restore direct connectivity on failed apply"}
	}
	return plan
}

func normalizeKillSwitchPolicy(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "", KillSwitchPolicySoft:
		return KillSwitchPolicySoft
	case KillSwitchPolicyStrict:
		return KillSwitchPolicyStrict
	case KillSwitchPolicyOff:
		return KillSwitchPolicyOff
	default:
		return KillSwitchPolicySoft
	}
}

func normalizeDNSServers(servers []string) []string {
	if len(servers) == 0 {
		return []string{DefaultTunDNSServer}
	}
	return append([]string{}, servers...)
}
