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

// CommandResult contains a completed read-only command's observable output.
type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// CommandRunner abstracts read-only host inspection commands for deterministic tests.
type CommandRunner interface {
	LookPath(file string) (string, error)
	Run(ctx context.Context, name string, args ...string) (CommandResult, error)
}

// HostResolver abstracts hostname resolution for deterministic tests.
type HostResolver func(ctx context.Context, host string) ([]string, error)

// OSRunner executes read-only host inspection commands through os/exec.
type OSRunner struct{}

// LookPath resolves a command using the host PATH.
func (OSRunner) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

// Run executes a host command and captures stdout, stderr, and exit code.
func (OSRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := CommandResult{
		Stdout:   strings.TrimSpace(stdout.String()),
		Stderr:   strings.TrimSpace(stderr.String()),
		ExitCode: 0,
	}
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

// Options controls read-only snapshot collection.
type Options struct {
	Server             string
	TunNames           []string
	OS                 string
	ResolveHost        HostResolver
	ResolveHostTimeout time.Duration
}

// Collect reads the current host state without mutating networking state.
func Collect(ctx context.Context, opts Options) Snapshot {
	return CollectWithRunner(ctx, OSRunner{}, opts)
}

// CollectWithRunner reads host state using an injectable command runner.
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
		s.TunDevices = tunDevices(ctx, runner, ipPath, tunNames)
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
	s.TunDevices = make([]TunDevice, 0, len(tunNames))
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

func routeTarget(ctx context.Context, server string, opts Options) (target string, detail string, err error) {
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
		return DNS{Mode: "systemd-resolved", Resolved: findingWithDetail(StatusDetected, "systemd-resolved status available", firstNonEmptyLine(result.Stdout))}
	}
	return DNS{Mode: "unknown", Resolved: findingWithDetail(StatusUnknown, "systemd-resolved status unavailable", commandFailureMessage(result, err))}
}

func networkManager(ctx context.Context, runner CommandRunner) NetworkManager {
	path, ok := lookup(runner, "nmcli")
	if !ok {
		return NetworkManager{Finding: finding(StatusMissing, "nmcli not found")}
	}
	result, err := runCommand(ctx, runner, path, "-t", "-f", "RUNNING,STATE", "general")
	if commandSucceeded(result, err) {
		line := firstNonEmptyLine(result.Stdout)
		return NetworkManager{Finding: findingWithDetail(StatusDetected, "NetworkManager state available", line), State: parseNMState(line)}
	}
	return NetworkManager{Finding: findingWithDetail(StatusUnknown, "NetworkManager state unavailable", commandFailureMessage(result, err))}
}

func parseNMState(line string) string {
	parts := strings.Split(line, ":")
	if len(parts) == 0 {
		return strings.TrimSpace(line)
	}
	return strings.TrimSpace(parts[len(parts)-1])
}

func nftables(ctx context.Context, runner CommandRunner) Nftables {
	path, ok := lookup(runner, "nft")
	if !ok {
		return Nftables{
			Availability: finding(StatusMissing, "nft not found"),
			PodlazTable:  finding(StatusMissing, "podlaz nftables table not inspected because nft is unavailable"),
		}
	}
	result, err := runCommand(ctx, runner, path, "list", "tables")
	if !commandSucceeded(result, err) {
		detail := commandFailureMessage(result, err)
		return Nftables{
			Availability: findingWithDetail(StatusUnknown, "nftables table listing unavailable", detail),
			PodlazTable:  findingWithDetail(StatusUnknown, "podlaz nftables table state unknown", detail),
		}
	}
	availability := finding(StatusDetected, "nftables table listing available")
	tableLine := fmt.Sprintf("table %s %s", DefaultNFTFamily, DefaultNFTTable)
	if strings.Contains(result.Stdout, tableLine) {
		return Nftables{
			Availability: availability,
			PodlazTable:  finding(StatusDetected, "podlaz nftables table exists"),
		}
	}
	return Nftables{
		Availability: availability,
		PodlazTable:  finding(StatusMissing, "podlaz nftables table not found"),
	}
}

func tunDevices(ctx context.Context, runner CommandRunner, ipPath string, tunNames []string) []TunDevice {
	devices := make([]TunDevice, 0, len(tunNames))
	for _, name := range tunNames {
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
		if dev.Status == StatusDetected {
			stale = append(stale, StaleResource{Kind: "tun-device", Name: dev.Name, Status: StatusDetected, Detail: dev.Raw})
		}
	}
	if s.Nftables.PodlazTable.Status == StatusDetected {
		stale = append(stale, StaleResource{Kind: "nftables-table", Name: DefaultNFTFamily + " " + DefaultNFTTable, Status: StatusDetected})
	}
	return stale
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
	return strings.Contains(text, "does not exist") ||
		strings.Contains(text, "cannot find device") ||
		strings.Contains(text, "no such file or directory") ||
		strings.Contains(text, "no such table") ||
		strings.Contains(text, "no such process")
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
