package daemon

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

const defaultDNSRouteDomain = "~."
const podlazTunRulePriority = "10000"
const podlazServerRulePriority = "9999"

var nmcliConnectionDown = func(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("missing NetworkManager connection id")
	}
	out, err := exec.CommandContext(ctx, "nmcli", "connection", "down", id).CombinedOutput()
	if err != nil {
		return fmt.Errorf("nmcli connection down %s: %w: %s", id, err, strings.TrimSpace(string(out)))
	}
	return nil
}

var controlledPodlazRecover = func(ctx context.Context, runtimeDir string) error {
	result := recovery.ExecuteWithOptions(ctx, recovery.Options{RuntimeDir: runtimeDir, Executor: recovery.DaemonCleanupExecutor{RuntimeDir: runtimeDir}})
	if result.HasFailures() || result.HasIncompleteCleanup() {
		return fmt.Errorf("controlled podlaz recovery did not fully complete before replace-podlaz handoff: %s", strings.TrimSpace(result.String()))
	}
	return nil
}

var podlazRuntimeRoutingStaleResources = func(ctx context.Context) []netsnapshot.StaleResource {
	ipPath, err := exec.LookPath("ip")
	if err != nil {
		return nil
	}
	var resources []netsnapshot.StaleResource
	if out, ok := runReadOnlyCommand(ctx, ipPath, "-4", "route", "show", "table", netsnapshot.DefaultRouteTableID); ok && strings.TrimSpace(out) != "" {
		resources = append(resources, netsnapshot.StaleResource{Kind: "route-table", Name: netsnapshot.DefaultRouteTableID, Status: netsnapshot.StatusDetected, Detail: firstNonEmptyLine(out)})
	}
	if out, ok := runReadOnlyCommand(ctx, ipPath, "-4", "rule", "show"); ok {
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || !podlazPolicyRuleLine(line) {
				continue
			}
			resources = append(resources, netsnapshot.StaleResource{Kind: "policy-rule", Name: policyRuleName(line), Status: netsnapshot.StatusDetected, Detail: line})
		}
	}
	return resources
}

type tunHandoffBlocker struct {
	Policy    string
	Conflicts []string
	NextStep  string
}

func (e *tunHandoffBlocker) Error() string {
	if e == nil {
		return "podlaz: TUN handoff blocked"
	}
	conflicts := e.Conflicts
	if len(conflicts) == 0 {
		conflicts = []string{"unknown foreign VPN ownership signal"}
	}
	next := strings.TrimSpace(e.NextStep)
	if next == "" {
		next = "Stop the other VPN or use an explicit safe handoff policy, then retry."
	}
	return fmt.Sprintf(`podlaz: TUN handoff blocked before network mutation.

Detected:
  - %s

Policy: %s
podlaz did not change network state.
Next step: %s

Run:
  plz doctor`, strings.Join(conflicts, "\n  - "), fallbackUnknown(e.Policy), next)
}

type tunStalePodlazStateBlocker struct {
	Resources []string
}

func (e *tunStalePodlazStateBlocker) Error() string {
	if e == nil || len(e.Resources) == 0 {
		return "podlaz: stale podlaz-owned networking state blocks TUN connect"
	}
	return fmt.Sprintf(`podlaz: stale podlaz-owned networking state blocks TUN connect.

Detected:
  - %s

podlaz did not change network state.
Run daemon-owned recovery first, then retry connect.

Run:
  plz recover --execute --yes`, strings.Join(e.Resources, "\n  - "))
}

func (m *XrayManager) prepareActivePodlazReplace(ctx context.Context, handoff string) error {
	policy := api.NormalizeHandoffPolicy(handoff)
	m.mu.Lock()
	active := m.cmd != nil || m.state.Connection == "active"
	mode := m.state.Mode
	m.mu.Unlock()
	if !active {
		return nil
	}
	if policy != api.HandoffReplacePodlaz || mode != "tun" {
		return errConnectionAlreadyActive
	}
	if _, err := m.Disconnect(ctx); err != nil {
		return fmt.Errorf("replace active podlaz TUN connection: %w", err)
	}
	return nil
}

func (m *XrayManager) prepareTunHandoff(ctx context.Context, s netsnapshot.Snapshot, handoff string, opts netsnapshot.Options) (netsnapshot.Snapshot, error) {
	policy := api.NormalizeHandoffPolicy(handoff)
	s = m.withPodlazRuntimeStaleState(ctx, s)
	switch policy {
	case api.HandoffAsk:
		return s, &tunHandoffBlocker{Policy: policy, Conflicts: []string{"--handoff=ask is interactive and is not supported by daemon/non-interactive connect"}, NextStep: "Use --handoff=block, --handoff=stop-known, or --handoff=replace-podlaz explicitly."}
	case api.HandoffReplacePodlaz:
		if stalePodlazStateBlocker(s) != nil {
			if err := m.runControlledPodlazRecover(ctx); err != nil {
				return s, err
			}
			s = m.withPodlazRuntimeStaleState(ctx, m.collectTunSnapshot(ctx, opts))
		}
	case api.HandoffStopKnown:
		connections := activeNetworkManagerVPNConnections(s)
		for _, connection := range connections {
			id := firstNonEmpty(connection.UUID, connection.Name)
			if err := nmcliConnectionDown(ctx, id); err != nil {
				return s, &tunHandoffBlocker{Policy: policy, Conflicts: []string{"failed to stop NetworkManager VPN " + fallbackUnknown(connection.Name)}, NextStep: err.Error()}
			}
		}
		if len(connections) > 0 {
			s = m.withPodlazRuntimeStaleState(ctx, m.collectTunSnapshot(ctx, opts))
		}
	}
	if err := preflightTunOwnership(s, policy); err != nil {
		return s, err
	}
	return s, nil
}

func (m *XrayManager) withPodlazRuntimeStaleState(ctx context.Context, s netsnapshot.Snapshot) netsnapshot.Snapshot {
	s.StaleResources = append(s.StaleResources, podlazRuntimeRoutingStaleResources(ctx)...)
	s.StaleResources = append(s.StaleResources, m.transactionFileStaleResources()...)
	return s
}

func (m *XrayManager) transactionFileStaleResources() []netsnapshot.StaleResource {
	runtimeDir := strings.TrimSpace(m.runtimeDir())
	if runtimeDir == "" {
		return nil
	}
	summaries, warnings := txstate.ScanTransactions(runtimeDir)
	resources := make([]netsnapshot.StaleResource, 0, len(summaries)+len(warnings))
	for _, summary := range summaries {
		if !summary.RequiresRecovery {
			continue
		}
		name := strings.TrimSpace(filepath.Base(summary.Path))
		if name == "" || name == "." {
			name = firstNonEmpty(summary.ID+txstate.TransactionFileSuffix, "unknown")
		}
		resources = append(resources, netsnapshot.StaleResource{
			Kind:   "transaction-file",
			Name:   name,
			Status: netsnapshot.StatusDetected,
			Detail: fmt.Sprintf("state=%s requires daemon-owned recovery", summary.State),
		})
	}
	for range warnings {
		resources = append(resources, netsnapshot.StaleResource{
			Kind:   "transaction-file",
			Name:   "invalid-or-unreadable",
			Status: netsnapshot.StatusDetected,
			Detail: "validated transaction scan failed; run daemon-owned recovery or inspect the transaction directory as administrator",
		})
	}
	return resources
}

func (m *XrayManager) runControlledPodlazRecover(ctx context.Context) error {
	return controlledPodlazRecover(ctx, m.runtimeDir())
}

func preflightTunOwnership(s netsnapshot.Snapshot, handoff string) error {
	policy := api.NormalizeHandoffPolicy(handoff)
	if blocker := stalePodlazStateBlocker(s); blocker != nil {
		return blocker
	}
	conflicts := foreignOwnershipConflicts(s)
	if len(conflicts) == 0 {
		return nil
	}
	next := "Use --handoff=block to keep state unchanged, --handoff=stop-known for manageable NetworkManager VPNs, or stop the foreign VPN manually."
	if policy == api.HandoffStopKnown {
		next = "A known NetworkManager VPN was stopped if possible, but foreign VPN ownership signals remain; inspect plz doctor."
	}
	return &tunHandoffBlocker{Policy: policy, Conflicts: conflicts, NextStep: next}
}

func isTunHandoffBlocker(err error) bool {
	var blocker *tunHandoffBlocker
	return errors.As(err, &blocker)
}

func isTunStalePodlazStateBlocker(err error) bool {
	var blocker *tunStalePodlazStateBlocker
	return errors.As(err, &blocker)
}

func foreignOwnershipConflicts(s netsnapshot.Snapshot) []string {
	var conflicts []string
	if foreign, ok := foreignDefaultDNSOwner(s); ok {
		conflicts = append(conflicts, fmt.Sprintf("foreign route-only DNS owner interface=%s domain=%s server=%s", fallbackUnknown(foreign.Name), firstOrDefault(foreign.DNSDomains, defaultDNSRouteDomain), firstNonEmpty(foreign.CurrentDNSServer, firstOrDefault(foreign.DNSServers, ""))))
	}
	for _, device := range s.TunDevices {
		if device.Status == netsnapshot.StatusDetected && isForeignTunLikeName(device.Name) {
			conflicts = append(conflicts, "foreign TUN-like interface "+device.Name)
		}
	}
	if s.ServerRoute.Status == netsnapshot.StatusDetected && isForeignTunLikeName(s.ServerRoute.Interface) {
		conflicts = append(conflicts, "VPN server route uses foreign VPN interface "+s.ServerRoute.Interface)
	}
	for _, signal := range s.PolicyRouting {
		if podlazPolicyRoutingSignal(signal) {
			continue
		}
		conflicts = append(conflicts, fmt.Sprintf("foreign policy routing %s", fallbackUnknown(signal.Raw)))
	}
	for _, connection := range activeNetworkManagerVPNConnections(s) {
		conflicts = append(conflicts, fmt.Sprintf("active NetworkManager VPN connection %s on %s", fallbackUnknown(connection.Name), fallbackUnknown(connection.Device)))
	}
	return compactStrings(conflicts)
}

func activeNetworkManagerVPNConnections(s netsnapshot.Snapshot) []netsnapshot.NetworkManagerConnection {
	var out []netsnapshot.NetworkManagerConnection
	for _, connection := range s.NetworkManager.ActiveConnections {
		if strings.EqualFold(strings.TrimSpace(connection.Type), "vpn") || isForeignTunLikeName(connection.Device) {
			out = append(out, connection)
		}
	}
	return out
}

func stalePodlazStateBlocker(s netsnapshot.Snapshot) *tunStalePodlazStateBlocker {
	resources := stalePodlazResourceSummaries(s)
	if len(resources) == 0 {
		return nil
	}
	return &tunStalePodlazStateBlocker{Resources: resources}
}

func stalePodlazResourceSummaries(s netsnapshot.Snapshot) []string {
	seen := map[string]bool{}
	var resources []string
	add := func(kind, name string) {
		kind = strings.TrimSpace(kind)
		name = strings.TrimSpace(name)
		if kind == "" || name == "" {
			return
		}
		value := kind + " " + name
		if seen[value] {
			return
		}
		seen[value] = true
		resources = append(resources, value)
	}
	for _, resource := range s.StaleResources {
		if resource.Status == netsnapshot.StatusDetected {
			add(resource.Kind, resource.Name)
		}
	}
	for _, device := range s.TunDevices {
		if device.Name == netsnapshot.DefaultTunName && device.Status == netsnapshot.StatusDetected {
			add("tun-device", device.Name)
		}
	}
	if s.Nftables.PodlazTable.Status == netsnapshot.StatusDetected {
		add("nftables-table", netsnapshot.DefaultNFTFamily+" "+netsnapshot.DefaultNFTTable)
	}
	for _, signal := range s.PolicyRouting {
		if !podlazPolicyRoutingSignal(signal) {
			continue
		}
		switch signal.Kind {
		case "route":
			add("route-table", firstNonEmpty(signal.Table, netsnapshot.DefaultRouteTableID))
		case "rule":
			add("policy-rule", firstNonEmpty(signal.Priority, signal.Table, netsnapshot.DefaultRouteTableID))
		}
	}
	return resources
}

func foreignDefaultDNSOwner(s netsnapshot.Snapshot) (netsnapshot.ResolvedLink, bool) {
	for _, link := range s.DNS.ResolvedLinks {
		if strings.TrimSpace(link.Name) == "" || link.Name == netsnapshot.DefaultTunName {
			continue
		}
		if !containsToken(link.DNSDomains, defaultDNSRouteDomain) {
			continue
		}
		if containsToken(link.Protocols, "+DefaultRoute") || strings.TrimSpace(link.CurrentDNSServer) != "" || len(link.DNSServers) > 0 {
			return link, true
		}
	}
	return netsnapshot.ResolvedLink{}, false
}

func podlazPolicyRoutingSignal(signal netsnapshot.PolicyRoutingSignal) bool {
	table := strings.TrimSpace(signal.Table)
	priority := strings.TrimSpace(signal.Priority)
	return table == netsnapshot.DefaultRouteTableID || table == "podlaz" || priority == podlazTunRulePriority || priority == podlazServerRulePriority
}

func podlazPolicyRuleLine(line string) bool {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return false
	}
	priority := strings.TrimSuffix(fields[0], ":")
	return priority == podlazTunRulePriority || priority == podlazServerRulePriority || strings.Contains(line, "lookup "+netsnapshot.DefaultRouteTableID) || strings.Contains(line, "lookup podlaz")
}

func policyRuleName(line string) string {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return netsnapshot.DefaultRouteTableID
	}
	priority := strings.TrimSuffix(fields[0], ":")
	return firstNonEmpty(priority, netsnapshot.DefaultRouteTableID)
}

func runReadOnlyCommand(ctx context.Context, name string, args ...string) (string, bool) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

func firstNonEmptyLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func isForeignTunLikeName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return name != "" && name != netsnapshot.DefaultTunName && (strings.HasPrefix(name, "tun") || strings.HasPrefix(name, "tap") || strings.HasPrefix(name, "wg") || strings.HasPrefix(name, "tailscale") || strings.HasPrefix(name, "zt") || strings.HasPrefix(name, "ppp") || strings.HasPrefix(name, "ipsec") || strings.HasPrefix(name, "proton") || strings.HasPrefix(name, "nord"))
}

func containsToken(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func fallbackUnknown(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "<unknown>"
	}
	return value
}

func firstOrDefault(values []string, fallback string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func compactStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
