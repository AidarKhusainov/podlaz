package daemon

import (
	"bytes"
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

const podlazTunRulePriority = "10000"
const podlazServerRulePriority = "9999"

var podlazRuntimeRoutingStaleResources = func(ctx context.Context) []netsnapshot.StaleResource {
	ipPath, err := exec.LookPath("ip")
	if err != nil {
		return []netsnapshot.StaleResource{{Kind: "runtime-inspection", Name: "ip", Status: netsnapshot.StatusUnknown, Detail: "ip command is unavailable: " + err.Error()}}
	}
	var resources []netsnapshot.StaleResource
	routeTable := inspectPodlazRouteTable(ctx, ipPath)
	switch {
	case routeTable.UnknownDetail != "":
		resources = append(resources, netsnapshot.StaleResource{Kind: "runtime-inspection", Name: "route-table-" + netsnapshot.DefaultRouteTableID, Status: netsnapshot.StatusUnknown, Detail: routeTable.UnknownDetail})
	case routeTable.Missing:
		// A supported iproute2 missing-table result is authoritative absence: a
		// clean host has no podlaz-owned route residue in table 51820.
	default:
		for _, rawLine := range strings.Split(routeTable.Output, "\n") {
			line := strings.TrimSpace(rawLine)
			if line == "" {
				continue
			}
			resources = append(resources, netsnapshot.StaleResource{Kind: "route", Name: staleRouteResourceName(line), Status: netsnapshot.StatusDetected, Detail: line})
		}
	}
	if out, ok, detail := runReadOnlyCommand(ctx, ipPath, "-4", "rule", "show"); ok {
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || !podlazPolicyRuleLine(line) {
				continue
			}
			resources = append(resources, netsnapshot.StaleResource{Kind: "policy-rule", Name: policyRuleName(line), Status: netsnapshot.StatusDetected, Detail: line})
		}
	} else {
		resources = append(resources, netsnapshot.StaleResource{Kind: "runtime-inspection", Name: "policy-rules", Status: netsnapshot.StatusUnknown, Detail: detail})
	}
	return resources
}

type routeTableInspection struct {
	Output        string
	Missing       bool
	UnknownDetail string
}

func inspectPodlazRouteTable(ctx context.Context, ipPath string) routeTableInspection {
	args := []string{"-4", "route", "show", "table", netsnapshot.DefaultRouteTableID}
	cmd := exec.CommandContext(ctx, ipPath, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return routeTableInspection{Output: strings.TrimSpace(stdout.String())}
	}
	if supportedMissingRouteTableResult(err, stdout.String(), stderr.String()) {
		return routeTableInspection{Missing: true}
	}
	raw := strings.TrimSpace(strings.Join(compactStrings([]string{stdout.String(), stderr.String()}), "\n"))
	return routeTableInspection{UnknownDetail: fmt.Sprintf("%s %s failed: %v: %s", ipPath, strings.Join(args, " "), err, raw)}
}

func supportedMissingRouteTableResult(err error, stdout, stderr string) bool {
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 2 || stdout != "" {
		return false
	}
	normalized := strings.ReplaceAll(stderr, "\r\n", "\n")
	switch normalized {
	case "Error: ipv4: FIB table does not exist.\nDump terminated\n",
		"Error: FIB table does not exist.\nDump terminated\n":
		return true
	default:
		return false
	}
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
		conflicts = []string{"TUN lifecycle preflight could not prove a safe operation"}
	}
	next := strings.TrimSpace(e.NextStep)
	if next == "" {
		next = "Resolve the reported Podlaz lifecycle condition and retry."
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
	Resources                []string
	RoutingRecoveryAvailable bool
}

func (e *tunStalePodlazStateBlocker) Error() string {
	if e == nil || len(e.Resources) == 0 {
		return "podlaz: stale podlaz-owned networking state blocks TUN connect"
	}
	if staleResourcesContainRouting(e.Resources) && !e.RoutingRecoveryAvailable {
		return fmt.Sprintf(`podlaz: ambiguous stale routing state blocks TUN connect before network mutation.

Detected:
  - %s

The remaining policy-rule/route shape matches Podlaz's historical routing layout, but exact durable rollback ownership evidence is unavailable for every observed routing object. Recovery cannot safely delete unmatched kernel objects from historical priorities/table numbers alone.

Next step: run plz doctor and inspect the reported rules/routes as administrator. Remove them manually only after independently proving ownership, then retry connect.`, strings.Join(e.Resources, "\n  - "))
	}
	return fmt.Sprintf(`podlaz: stale podlaz-owned networking state blocks TUN connect.

Detected:
  - %s

podlaz did not change network state.
Run daemon-owned recovery first, then retry connect.

Run:
  plz recover --execute --yes`, strings.Join(e.Resources, "\n  - "))
}

func staleResourcesContainRouting(resources []string) bool {
	for _, resource := range resources {
		resource = strings.TrimSpace(resource)
		if strings.HasPrefix(resource, "policy-rule ") || strings.HasPrefix(resource, "route ") {
			return true
		}
	}
	return false
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

func (m *XrayManager) withPodlazRuntimeStaleState(ctx context.Context, s netsnapshot.Snapshot) netsnapshot.Snapshot {
	routingResources := podlazRuntimeRoutingStaleResources(ctx)
	transactionResources, transactions := m.transactionFileStaleState()
	markRoutingRecoveryAuthority(routingResources, transactions)
	s.StaleResources = append(s.StaleResources, routingResources...)
	s.StaleResources = append(s.StaleResources, transactionResources...)
	return s
}

func (m *XrayManager) transactionFileStaleResources() []netsnapshot.StaleResource {
	resources, _ := m.transactionFileStaleState()
	return resources
}

func (m *XrayManager) transactionFileStaleState() ([]netsnapshot.StaleResource, []txstate.Transaction) {
	runtimeDir := strings.TrimSpace(m.runtimeDir())
	if runtimeDir == "" {
		return nil, nil
	}
	summaries, warnings := txstate.ScanTransactions(runtimeDir)
	resources := make([]netsnapshot.StaleResource, 0, len(summaries)+len(warnings))
	transactions := make([]txstate.Transaction, 0, len(summaries))
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
		tx, err := txstate.LoadTransactionFile(summary.Path)
		if err == nil && tx.RequiresRecovery() {
			transactions = append(transactions, tx)
		}
	}
	for range warnings {
		resources = append(resources, netsnapshot.StaleResource{
			Kind:   "transaction-file",
			Name:   "invalid-or-unreadable",
			Status: netsnapshot.StatusDetected,
			Detail: "validated transaction scan failed; run daemon-owned recovery or inspect the transaction directory as administrator",
		})
	}
	return resources, transactions
}

func markRoutingRecoveryAuthority(resources []netsnapshot.StaleResource, transactions []txstate.Transaction) {
	for i := range resources {
		resource := &resources[i]
		if resource.Status != netsnapshot.StatusDetected || !routingStaleResourceKind(resource.Kind) {
			continue
		}
		for _, tx := range transactions {
			if recovery.TransactionOwnsObservedRoutingResource(tx, resource.Kind, resource.Name, resource.Detail) {
				resource.RecoveryAuthorized = true
				break
			}
		}
	}
}

func routingStaleResourceKind(kind string) bool {
	kind = strings.TrimSpace(kind)
	return kind == "route" || kind == "policy-rule"
}

func isTunHandoffBlocker(err error) bool {
	var blocker *tunHandoffBlocker
	return errors.As(err, &blocker)
}

func isTunStalePodlazStateBlocker(err error) bool {
	var blocker *tunStalePodlazStateBlocker
	return errors.As(err, &blocker)
}

func stalePodlazStateBlocker(s netsnapshot.Snapshot) *tunStalePodlazStateBlocker {
	resources := stalePodlazResourceSummaries(s)
	if len(resources) == 0 {
		return nil
	}
	return &tunStalePodlazStateBlocker{
		Resources:                resources,
		RoutingRecoveryAvailable: staleStateHasRoutingRecoveryAuthority(s),
	}
}

func staleStateHasRoutingRecoveryAuthority(s netsnapshot.Snapshot) bool {
	authorized := map[string]bool{}
	sawRouting := false
	for _, resource := range s.StaleResources {
		if resource.Status != netsnapshot.StatusDetected && resource.Status != netsnapshot.StatusUnknown {
			continue
		}
		if !routingStaleResourceKind(resource.Kind) {
			continue
		}
		sawRouting = true
		if resource.Status != netsnapshot.StatusDetected || !resource.RecoveryAuthorized {
			return false
		}
		authorized[routingStaleResourceKey(resource.Kind, resource.Name)] = true
	}
	for _, signal := range s.PolicyRouting {
		if !podlazPolicyRoutingSignal(signal) {
			continue
		}
		kind, name := policyRoutingSignalResourceIdentity(signal)
		if kind == "" || name == "" {
			return false
		}
		sawRouting = true
		if !authorized[routingStaleResourceKey(kind, name)] {
			return false
		}
	}
	return sawRouting
}

func policyRoutingSignalResourceIdentity(signal netsnapshot.PolicyRoutingSignal) (string, string) {
	switch signal.Kind {
	case "route":
		return "route", firstNonEmpty(staleRouteResourceName(signal.Raw), signal.Table, netsnapshot.DefaultRouteTableID)
	case "rule":
		return "policy-rule", firstNonEmpty(signal.Priority, signal.Table, netsnapshot.DefaultRouteTableID)
	default:
		return "", ""
	}
}

func routingStaleResourceKey(kind, name string) string {
	return strings.TrimSpace(kind) + "\x00" + strings.TrimSpace(name)
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
		if resource.Status == netsnapshot.StatusDetected || resource.Status == netsnapshot.StatusUnknown {
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
		kind, name := policyRoutingSignalResourceIdentity(signal)
		add(kind, name)
	}
	return resources
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

func staleRouteResourceName(line string) string {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return netsnapshot.DefaultRouteTableID
	}
	table := netsnapshot.DefaultRouteTableID
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "table" {
			table = fields[i+1]
			break
		}
	}
	return strings.TrimSpace(table)
}

func runReadOnlyCommand(ctx context.Context, name string, args ...string) (string, bool, string) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return "", false, fmt.Sprintf("%s %s failed: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), true, ""
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

func fallbackUnknown(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "<unknown>"
	}
	return value
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
