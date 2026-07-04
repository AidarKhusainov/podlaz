package snapshot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const defaultCommandTimeout = 3 * time.Second
const defaultResolveHostTimeout = 3 * time.Second

type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

type CommandRunner interface {
	LookPath(file string) (string, error)
	Run(ctx context.Context, name string, args ...string) (CommandResult, error)
}

type HostResolver func(ctx context.Context, host string) ([]string, error)

type OSRunner struct{}

func (OSRunner) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

func (OSRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := CommandResult{Stdout: strings.TrimSpace(stdout.String()), Stderr: strings.TrimSpace(stderr.String()), ExitCode: 0}
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
	} else {
		result.ExitCode = -1
	}
	return result, err
}

type Options struct {
	Server             string
	TunNames           []string
	OS                 string
	ResolveHost        HostResolver
	ResolveHostTimeout time.Duration
}

func Collect(ctx context.Context, opts Options) Snapshot {
	return CollectWithRunner(ctx, OSRunner{}, opts)
}

func CollectWithRunner(ctx context.Context, runner CommandRunner, opts Options) Snapshot {
	tunNames := opts.TunNames
	if len(tunNames) == 0 {
		tunNames = []string{DefaultTunName}
	}
	platform := opts.OS
	if platform == "" {
		platform = runtime.GOOS
	}
	s := Snapshot{OS: platform}
	if platform != "linux" {
		markUnsupported(&s, tunNames)
		return s
	}
	ipPath, ipOK := lookup(runner, "ip")
	if ipOK {
		s.DefaultIPv4 = route(ctx, runner, ipPath, "ipv4", "default", "-4", "route", "show", "default")
		s.DefaultIPv6 = route(ctx, runner, ipPath, "ipv6", "default", "-6", "route", "show", "default")
		s.ServerRoute = serverRoute(ctx, runner, ipPath, strings.TrimSpace(opts.Server), opts)
		s.TunDevices = tunDevices(ctx, runner, ipPath, appendUniqueStrings(tunNames, tunLikeInterfaceNames(ctx, runner, ipPath)...))
		s.PolicyRouting = policyRouting(ctx, runner, ipPath)
	} else {
		s.DefaultIPv4 = missingRoute("ipv4", "default", "ip command not found")
		s.DefaultIPv6 = missingRoute("ipv6", "default", "ip command not found")
		s.ServerRoute = missingRoute("", "server", "ip command not found")
		s.TunDevices = missingTunDevices(tunNames, "ip command not found")
	}
	s.IPv4 = availabilityFromDefaultRoute("IPv4", s.DefaultIPv4)
	s.IPv6 = availabilityFromDefaultRoute("IPv6", s.DefaultIPv6)
	s.DNS = dns(ctx, runner)
	s.NetworkManager = networkManager(ctx, runner)
	s.Nftables = nftables(ctx, runner)
	s.StaleResources = staleResources(s)
	return s
}

func markUnsupported(s *Snapshot, tunNames []string) {
	detail := "system snapshot collection is currently implemented for linux hosts only"
	s.DefaultIPv4 = Route{Status: StatusUnsupported, Family: "ipv4", Destination: "default", Detail: detail}
	s.DefaultIPv6 = Route{Status: StatusUnsupported, Family: "ipv6", Destination: "default", Detail: detail}
	s.ServerRoute = Route{Status: StatusUnsupported, Destination: "server", Detail: detail}
	s.DNS = DNS{Mode: "unsupported", Resolved: findingWithDetail(StatusUnsupported, "systemd-resolved is not inspected on this platform", detail)}
	s.NetworkManager = NetworkManager{Finding: findingWithDetail(StatusUnsupported, "NetworkManager is not inspected on this platform", detail)}
	s.Nftables = Nftables{
		Availability: findingWithDetail(StatusUnsupported, "nftables is not inspected on this platform", detail),
		PodlazTable:  findingWithDetail(StatusUnsupported, "podlaz nftables table is not inspected on this platform", detail),
	}
	for _, name := range tunNames {
		s.TunDevices = append(s.TunDevices, TunDevice{Name: name, Status: StatusUnsupported, Detail: detail})
	}
	s.IPv4 = findingWithDetail(StatusUnsupported, "IPv4 route assumptions are unsupported on this platform", detail)
	s.IPv6 = findingWithDetail(StatusUnsupported, "IPv6 route assumptions are unsupported on this platform", detail)
}

func lookup(runner CommandRunner, command string) (string, bool) {
	path, err := runner.LookPath(command)
	return path, err == nil
}

func route(ctx context.Context, runner CommandRunner, ipPath, family, destination string, args ...string) Route {
	result, err := runCommand(ctx, runner, ipPath, args...)
	if commandSucceeded(result, err) {
		line := firstNonEmptyLine(result.Stdout)
		if line == "" {
			return missingRoute(family, destination, "route not found")
		}
		return parseRouteLine(line, family, destination)
	}
	if resourceMissing(result) {
		return missingRoute(family, destination, commandFailureMessage(result, err))
	}
	return Route{Status: StatusUnknown, Family: family, Destination: destination, Detail: commandFailureMessage(result, err)}
}

func serverRoute(ctx context.Context, runner CommandRunner, ipPath, server string, opts Options) Route {
	if server == "" {
		return Route{Status: StatusUnknown, Destination: "server", Detail: "profile server is empty"}
	}
	target, detail, err := routeTarget(ctx, server, opts)
	if err != nil {
		return Route{Status: StatusUnknown, Destination: server, Detail: err.Error()}
	}
	r := route(ctx, runner, ipPath, "", server, "route", "get", target)
	if detail != "" {
		if r.Detail == "" {
			r.Detail = detail
		} else {
			r.Detail = detail + "; " + r.Detail
		}
	}
	return r
}

func routeTarget(ctx context.Context, server string, opts Options) (string, string, error) {
	if ip := net.ParseIP(server); ip != nil {
		return server, "", nil
	}
	resolver := opts.ResolveHost
	if resolver == nil {
		resolver = net.DefaultResolver.LookupHost
	}
	timeout := opts.ResolveHostTimeout
	if timeout <= 0 {
		timeout = defaultResolveHostTimeout
	}
	resolveCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	hosts, err := resolver(resolveCtx, server)
	if err != nil {
		return "", "", fmt.Errorf("resolve server hostname %q: %w", server, err)
	}
	ip := chooseResolvedIP(hosts)
	if ip == "" {
		return "", "", fmt.Errorf("resolve server hostname %q: no IP addresses returned", server)
	}
	return ip, fmt.Sprintf("server hostname %s resolved to %s", server, ip), nil
}

func chooseResolvedIP(hosts []string) string {
	var first string
	for _, host := range hosts {
		ip := net.ParseIP(strings.TrimSpace(host))
		if ip == nil {
			continue
		}
		if first == "" {
			first = ip.String()
		}
		if ip.To4() != nil {
			return ip.String()
		}
	}
	return first
}

func missingRoute(family, destination, detail string) Route {
	return Route{Status: StatusMissing, Family: family, Destination: destination, Detail: detail}
}

func parseRouteLine(line, family, destination string) Route {
	r := Route{Status: StatusDetected, Family: family, Destination: destination, Raw: line}
	fields := strings.Fields(line)
	for i := 0; i < len(fields)-1; i++ {
		switch fields[i] {
		case "dev":
			r.Interface = fields[i+1]
		case "via":
			r.Gateway = fields[i+1]
		}
	}
	return r
}

func availabilityFromDefaultRoute(label string, route Route) Finding {
	switch route.Status {
	case StatusDetected:
		return finding(StatusDetected, label+" default route detected")
	case StatusMissing:
		return findingWithDetail(StatusMissing, label+" default route missing", route.Detail)
	case StatusUnsupported:
		return findingWithDetail(StatusUnsupported, label+" route inspection unsupported", route.Detail)
	default:
		return findingWithDetail(StatusUnknown, label+" route state unknown", route.Detail)
	}
}

func dns(ctx context.Context, runner CommandRunner) DNS {
	path, ok := lookup(runner, "resolvectl")
	if !ok {
		return DNS{Mode: "unknown", Resolved: finding(StatusMissing, "resolvectl not found")}
	}
	result, err := runCommand(ctx, runner, path, "status", "--no-pager")
	if commandSucceeded(result, err) {
		return DNS{Mode: "systemd-resolved", Resolved: findingWithDetail(StatusDetected, "systemd-resolved status available", firstNonEmptyLine(result.Stdout)), ResolvedLinks: ParseResolvedLinks(result.Stdout)}
	}
	return DNS{Mode: "unknown", Resolved: findingWithDetail(StatusUnknown, "systemd-resolved status unavailable", commandFailureMessage(result, err))}
}

func ParseResolvedLinks(output string) []ResolvedLink {
	var links []ResolvedLink
	var current *ResolvedLink
	lastKey := ""
	flush := func() {
		if current != nil && strings.TrimSpace(current.Name) != "" {
			links = append(links, *current)
		}
	}
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if index, name, ok := parseResolvedLinkHeader(line); ok {
			flush()
			current = &ResolvedLink{Index: index, Name: name}
			lastKey = ""
			continue
		}
		if current == nil {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if ok {
			lastKey = strings.TrimSpace(key)
			applyResolvedLinkField(current, lastKey, strings.TrimSpace(value))
			continue
		}
		if lastKey != "" {
			applyResolvedLinkField(current, lastKey, line)
		}
	}
	flush()
	return links
}

func parseResolvedLinkHeader(line string) (string, string, bool) {
	if !strings.HasPrefix(line, "Link ") {
		return "", "", false
	}
	open := strings.LastIndex(line, "(")
	close := strings.LastIndex(line, ")")
	if open < 0 || close <= open {
		return "", "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(line[:open], "Link ")), strings.TrimSpace(line[open+1 : close]), true
}

func applyResolvedLinkField(link *ResolvedLink, key, value string) {
	if link == nil || strings.TrimSpace(value) == "" {
		return
	}
	switch strings.TrimSpace(key) {
	case "Current Scopes":
		link.CurrentScopes = appendUniqueTokens(link.CurrentScopes, value)
	case "Protocols":
		link.Protocols = appendUniqueTokens(link.Protocols, value)
	case "Current DNS Server":
		link.CurrentDNSServer = firstField(value)
	case "DNS Servers":
		link.DNSServers = appendUniqueTokens(link.DNSServers, value)
	case "DNS Domain":
		link.DNSDomains = appendUniqueTokens(link.DNSDomains, value)
	}
}

func appendUniqueTokens(values []string, text string) []string {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		seen[value] = true
	}
	for _, token := range strings.Fields(text) {
		token = strings.TrimSpace(token)
		if token == "" || seen[token] {
			continue
		}
		seen[token] = true
		values = append(values, token)
	}
	return values
}

func firstField(value string) string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func networkManager(ctx context.Context, runner CommandRunner) NetworkManager {
	path, ok := lookup(runner, "nmcli")
	if !ok {
		return NetworkManager{Finding: finding(StatusMissing, "nmcli not found")}
	}
	result, err := runCommand(ctx, runner, path, "-t", "-f", "RUNNING,STATE", "general")
	if !commandSucceeded(result, err) {
		return NetworkManager{Finding: findingWithDetail(StatusUnknown, "NetworkManager state unavailable", commandFailureMessage(result, err))}
	}
	nm := NetworkManager{Finding: findingWithDetail(StatusDetected, "NetworkManager state available", firstNonEmptyLine(result.Stdout)), State: parseNMState(firstNonEmptyLine(result.Stdout))}
	active, activeErr := runCommand(ctx, runner, path, "-t", "-f", "NAME,UUID,TYPE,DEVICE,STATE", "connection", "show", "--active")
	if commandSucceeded(active, activeErr) {
		nm.ActiveConnections = parseNMActiveConnections(active.Stdout)
	}
	return nm
}

func parseNMState(line string) string {
	parts := strings.Split(line, ":")
	if len(parts) == 0 {
		return strings.TrimSpace(line)
	}
	return strings.TrimSpace(parts[len(parts)-1])
}

func parseNMActiveConnections(output string) []NetworkManagerConnection {
	var out []NetworkManagerConnection
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, ":")
		for len(parts) < 5 {
			parts = append(parts, "")
		}
		out = append(out, NetworkManagerConnection{Name: parts[0], UUID: parts[1], Type: parts[2], Device: parts[3], State: parts[4]})
	}
	return out
}

func nftables(ctx context.Context, runner CommandRunner) Nftables {
	path, ok := lookup(runner, "nft")
	if !ok {
		return Nftables{Availability: finding(StatusMissing, "nft not found"), PodlazTable: finding(StatusMissing, "podlaz nftables table not inspected because nft is unavailable")}
	}
	result, err := runCommand(ctx, runner, path, "list", "tables")
	if !commandSucceeded(result, err) {
		detail := commandFailureMessage(result, err)
		return Nftables{Availability: findingWithDetail(StatusUnknown, "nftables table listing unavailable", detail), PodlazTable: findingWithDetail(StatusUnknown, "podlaz nftables table state unknown", detail)}
	}
	availability := finding(StatusDetected, "nftables table listing available")
	if strings.Contains(result.Stdout, fmt.Sprintf("table %s %s", DefaultNFTFamily, DefaultNFTTable)) {
		return Nftables{Availability: availability, PodlazTable: finding(StatusDetected, "podlaz nftables table exists")}
	}
	return Nftables{Availability: availability, PodlazTable: finding(StatusMissing, "podlaz nftables table not found")}
}

func tunLikeInterfaceNames(ctx context.Context, runner CommandRunner, ipPath string) []string {
	result, err := runCommand(ctx, runner, ipPath, "-o", "link", "show")
	if !commandSucceeded(result, err) {
		return nil
	}
	var names []string
	for _, line := range strings.Split(result.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimSuffix(fields[1], ":")
		name = strings.Split(name, "@")[0]
		if isTunLikeInterfaceName(name) {
			names = append(names, name)
		}
	}
	return names
}

func tunDevices(ctx context.Context, runner CommandRunner, ipPath string, tunNames []string) []TunDevice {
	devices := make([]TunDevice, 0, len(tunNames))
	for _, name := range appendUniqueStrings(nil, tunNames...) {
		result, err := runCommand(ctx, runner, ipPath, "link", "show", "dev", name)
		switch {
		case commandSucceeded(result, err):
			devices = append(devices, TunDevice{Name: name, Status: StatusDetected, Raw: firstNonEmptyLine(result.Stdout)})
		case resourceMissing(result):
			devices = append(devices, TunDevice{Name: name, Status: StatusMissing, Detail: commandFailureMessage(result, err)})
		default:
			devices = append(devices, TunDevice{Name: name, Status: StatusUnknown, Detail: commandFailureMessage(result, err)})
		}
	}
	return devices
}

func policyRouting(ctx context.Context, runner CommandRunner, ipPath string) []PolicyRoutingSignal {
	var out []PolicyRoutingSignal
	if result, err := runCommand(ctx, runner, ipPath, "-4", "rule", "show"); commandSucceeded(result, err) {
		out = append(out, parsePolicyRules(result.Stdout)...)
	}
	if result, err := runCommand(ctx, runner, ipPath, "-4", "route", "show", "table", "all"); commandSucceeded(result, err) {
		out = append(out, parsePolicyRoutes(result.Stdout)...)
	}
	return out
}

func parsePolicyRules(output string) []PolicyRoutingSignal {
	var out []PolicyRoutingSignal
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || systemPolicyRule(line) {
			continue
		}
		fields := strings.Fields(strings.TrimSuffix(line, ":"))
		sig := PolicyRoutingSignal{Kind: "rule", Raw: line}
		if len(fields) > 0 {
			sig.Priority = strings.TrimSuffix(fields[0], ":")
		}
		for i := 0; i < len(fields)-1; i++ {
			switch fields[i] {
			case "lookup", "table":
				sig.Table = fields[i+1]
			case "fwmark":
				sig.Fwmark = fields[i+1]
			case "from", "to":
				if sig.Selector == "" {
					sig.Selector = fields[i] + " " + fields[i+1]
				} else {
					sig.Selector += " " + fields[i] + " " + fields[i+1]
				}
			}
		}
		if sig.Fwmark != "" || foreignPolicyTable(sig.Table) {
			out = append(out, sig)
		}
	}
	return out
}

func parsePolicyRoutes(output string) []PolicyRoutingSignal {
	var out []PolicyRoutingSignal
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		sig := PolicyRoutingSignal{Kind: "route", Raw: line}
		for i := 0; i < len(fields)-1; i++ {
			switch fields[i] {
			case "dev":
				sig.Interface = fields[i+1]
			case "table":
				sig.Table = fields[i+1]
			}
		}
		if isForeignTunLikeName(sig.Interface) || foreignPolicyTable(sig.Table) {
			out = append(out, sig)
		}
	}
	return out
}

func systemPolicyRule(line string) bool {
	return strings.Contains(line, "lookup local") || strings.Contains(line, "lookup main") || strings.Contains(line, "lookup default")
}

func foreignPolicyTable(table string) bool {
	table = strings.TrimSpace(table)
	return table != "" && table != "main" && table != "default" && table != "local" && table != "podlaz" && table != DefaultRouteTableID
}

func isTunLikeInterfaceName(name string) bool {
	return name == DefaultTunName || isForeignTunLikeName(name)
}

func isForeignTunLikeName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return strings.HasPrefix(name, "tun") || strings.HasPrefix(name, "tap") || strings.HasPrefix(name, "wg") || strings.HasPrefix(name, "tailscale") || strings.HasPrefix(name, "zt") || strings.HasPrefix(name, "ppp") || strings.HasPrefix(name, "ipsec") || strings.HasPrefix(name, "proton") || strings.HasPrefix(name, "nord")
}

func missingTunDevices(tunNames []string, detail string) []TunDevice {
	devices := make([]TunDevice, 0, len(tunNames))
	for _, name := range tunNames {
		devices = append(devices, TunDevice{Name: name, Status: StatusMissing, Detail: detail})
	}
	return devices
}

func staleResources(s Snapshot) []StaleResource {
	var stale []StaleResource
	for _, dev := range s.TunDevices {
		if dev.Name == DefaultTunName && dev.Status == StatusDetected {
			stale = append(stale, StaleResource{Kind: "tun-device", Name: dev.Name, Status: StatusDetected, Detail: dev.Raw})
		}
	}
	if s.Nftables.PodlazTable.Status == StatusDetected {
		stale = append(stale, StaleResource{Kind: "nftables-table", Name: DefaultNFTFamily + " " + DefaultNFTTable, Status: StatusDetected})
	}
	return stale
}

func appendUniqueStrings(values []string, more ...string) []string {
	seen := map[string]bool{}
	out := append([]string(nil), values...)
	for _, value := range out {
		seen[value] = true
	}
	for _, value := range more {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func runCommand(ctx context.Context, runner CommandRunner, name string, args ...string) (CommandResult, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, defaultCommandTimeout)
	defer cancel()
	return runner.Run(cmdCtx, name, args...)
}

func commandSucceeded(result CommandResult, err error) bool {
	return err == nil && result.ExitCode == 0
}

func resourceMissing(result CommandResult) bool {
	if result.ExitCode == 0 {
		return false
	}
	text := strings.ToLower(result.Stdout + " " + result.Stderr)
	return strings.Contains(text, "does not exist") || strings.Contains(text, "cannot find device") || strings.Contains(text, "no such file or directory") || strings.Contains(text, "no such table") || strings.Contains(text, "no such process")
}

func commandFailureMessage(result CommandResult, err error) string {
	parts := make([]string, 0, 3)
	if result.ExitCode >= 0 {
		parts = append(parts, fmt.Sprintf("exit code %d", result.ExitCode))
	}
	if result.Stderr != "" {
		parts = append(parts, "stderr: "+singleLine(result.Stderr))
	}
	if err != nil && result.Stderr == "" {
		parts = append(parts, err.Error())
	}
	if len(parts) == 0 {
		return "command failed"
	}
	return strings.Join(parts, ", ")
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func singleLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
