package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

const tunDiagnosticCommandOutputLimit = 16 * 1024

var tunDiagnosticCommandRunner = runTunDiagnosticCommand

type tunDiagnosticCommandResult struct {
	command  string
	stdout   string
	stderr   string
	exitCode int
}

func tunDiagnosticAdapters(input tunDiagnosticInput) tundiag.ProbeAdapters {
	client := tundiag.NetworkClient{}
	return tundiag.ProbeAdapters{
		Session:      func(context.Context) tundiag.ProbeResult { return probeTunDiagnosticSession(input) },
		ServerBypass: func(ctx context.Context) tundiag.ProbeResult { return probeTunServerBypass(ctx, input.plan) },
		IPv4Route:    func(ctx context.Context) tundiag.ProbeResult { return probeTunIPv4Route(ctx, input.plan) },
		DNSState:     func(context.Context) tundiag.ProbeResult { return probeTunDNSState(input.plan, input.snapshot) },
		DNSUDP:       func(ctx context.Context) tundiag.ProbeResult { return probeTunDNSWire(ctx, client, input.plan, false) },
		DNSTCP:       func(ctx context.Context) tundiag.ProbeResult { return probeTunDNSWire(ctx, client, input.plan, true) },
		SystemResolver: func(ctx context.Context) tundiag.ProbeResult {
			return probeTunSystemResolver(ctx, client, "example.com")
		},
		NXDomainIntegrity: func(ctx context.Context) tundiag.ProbeResult { return probeTunNXDomain(ctx, client) },
		TCP443:            func(ctx context.Context) tundiag.ProbeResult { return probeTunTCP(ctx, client) },
		TLS:               func(ctx context.Context) tundiag.ProbeResult { return probeTunTLS(ctx, client) },
		HTTPSCloudflare: func(ctx context.Context) tundiag.ProbeResult {
			return probeTunHTTPS(ctx, client, "https-cloudflare-small", tundiag.ClassHTTPSFailure)
		},
		HTTPSGoogle: func(ctx context.Context) tundiag.ProbeResult {
			return probeTunHTTPS(ctx, client, "https-google-small", tundiag.ClassHTTPSFailure)
		},
		DoHCloudflare: func(ctx context.Context) tundiag.ProbeResult { return probeTunDoH(ctx, "doh-cloudflare") },
		DoHGoogle:     func(ctx context.Context) tundiag.ProbeResult { return probeTunDoH(ctx, "doh-google") },
		IPv6:          func(ctx context.Context) tundiag.ProbeResult { return probeTunIPv6(ctx, input.plan, input.snapshot) },
		PMTUCloudflare: func(ctx context.Context) tundiag.ProbeResult {
			return probeTunHTTPS(ctx, client, "pmtu-cloudflare-16k", tundiag.ClassHTTPSFailure)
		},
		PMTUHetzner: func(ctx context.Context) tundiag.ProbeResult {
			return probeTunHTTPS(ctx, client, "pmtu-hetzner-16k", tundiag.ClassHTTPSFailure)
		},
	}
}

func probeTunDiagnosticSession(input tunDiagnosticInput) tundiag.ProbeResult {
	switch {
	case input.state.Mode != planner.ModeTun:
		return diagnosticFailure(tundiag.ClassSessionInactive, "active connection is not TUN mode")
	case input.state.Connection != "active" && input.state.Connection != "verifying":
		return diagnosticFailure(tundiag.ClassSessionInactive, "TUN session is not active")
	case input.state.TransactionID == "":
		return diagnosticFailure(tundiag.ClassSessionMetadataInconsistent, "TUN session has no transaction id")
	case !input.coreRunning:
		return diagnosticFailure(tundiag.ClassSessionMetadataInconsistent, "TUN session metadata is active but Xray is not running")
	default:
		return tundiag.ProbeResult{Status: tundiag.ProbePass}
	}
}

func probeTunServerBypass(ctx context.Context, plan planner.TunPlan) tundiag.ProbeResult {
	bypass := tunDiagnosticServerBypass(plan)
	target := strings.TrimSuffix(bypass.Destination, "/32")
	if net.ParseIP(target) == nil {
		return diagnosticFailure(tundiag.ClassServerBypassFailure, "transaction has no concrete VPN server bypass address")
	}
	evidence, command, err := lookupTunRoute(ctx, "-4", target)
	result := tundiag.ProbeResult{Evidence: tundiag.Evidence{Route: &evidence, Commands: []tundiag.CommandEvidence{command}}}
	if err != nil {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassServerBypassFailure
		result.Error = err.Error()
		return result
	}
	if evidence.Interface == netsnapshot.DefaultTunName || (bypass.Interface != "" && evidence.Interface != bypass.Interface) {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassServerBypassFailure
		result.Error = fmt.Sprintf("VPN server route uses interface %s; expected physical interface %s", evidence.Interface, emptyAs(bypass.Interface, "outside podlaz0"))
		return result
	}
	result.Status = tundiag.ProbePass
	return result
}

func probeTunIPv4Route(ctx context.Context, plan planner.TunPlan) tundiag.ProbeResult {
	evidence, routeCommand, err := lookupTunRoute(ctx, "-4", "1.1.1.1")
	ruleResult, ruleErr := tunDiagnosticCommandRunner(ctx, "ip", "-4", "rule", "show")
	commands := []tundiag.CommandEvidence{routeCommand, commandEvidence(ruleResult)}
	result := tundiag.ProbeResult{Evidence: tundiag.Evidence{Route: &evidence, Commands: commands}}
	if err != nil {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassRouteFailure
		result.Error = err.Error()
		return result
	}
	if ruleErr != nil {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassPolicyRuleFailure
		result.Error = ruleErr.Error()
		return result
	}
	if !tunDiagnosticHasPolicyRule(ruleResult.stdout, planner.TunRulePriority) {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassPolicyRuleFailure
		result.Error = fmt.Sprintf("missing priority %d lookup rule for the podlaz routing table", planner.TunRulePriority)
		return result
	}
	expectedInterface := emptyAs(plan.TunDevice.Name, netsnapshot.DefaultTunName)
	if evidence.Interface != expectedInterface {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassRouteFailure
		result.Error = fmt.Sprintf("IPv4 route uses interface %s; expected %s", evidence.Interface, expectedInterface)
		return result
	}
	result.Status = tundiag.ProbePass
	return result
}

func probeTunDNSState(plan planner.TunPlan, snapshot netsnapshot.Snapshot) tundiag.ProbeResult {
	targetLink := emptyAs(plan.DNS.TargetLink, netsnapshot.DefaultTunName)
	for _, link := range snapshot.DNS.ResolvedLinks {
		if link.Name != targetLink && tunDiagnosticContains(link.DNSDomains, "~.") {
			return diagnosticFailure(tundiag.ClassForeignDNSConflict, fmt.Sprintf("foreign resolved link %s owns route-only domain ~.", link.Name))
		}
	}
	for _, link := range snapshot.DNS.ResolvedLinks {
		if link.Name != targetLink {
			continue
		}
		missing := make([]string, 0, 3)
		for _, server := range plan.DNS.Servers {
			if !tunDiagnosticContains(link.DNSServers, server) {
				missing = append(missing, "DNS server "+server)
			}
		}
		if !tunDiagnosticContains(link.DNSDomains, "~.") {
			missing = append(missing, "route-only domain ~.")
		}
		if !tunDiagnosticContains(link.Protocols, "+DefaultRoute") {
			missing = append(missing, "+DefaultRoute")
		}
		if len(missing) > 0 {
			return diagnosticFailure(tundiag.ClassDNSApplyFailure, "resolved link "+targetLink+" is missing "+strings.Join(missing, ", "))
		}
		return tundiag.ProbeResult{Status: tundiag.ProbePass, Evidence: tundiag.Evidence{Notes: []string{"resolved link " + targetLink + " owns ~. and the planned DNS servers"}}}
	}
	return diagnosticFailure(tundiag.ClassDNSApplyFailure, "systemd-resolved has no link record for "+targetLink)
}

func probeTunDNSWire(ctx context.Context, client tundiag.NetworkClient, plan planner.TunPlan, tcp bool) tundiag.ProbeResult {
	if len(plan.DNS.Servers) == 0 {
		return diagnosticFailure(tundiag.ClassDNSApplyFailure, "transaction has no planned DNS server")
	}
	server := plan.DNS.Servers[0]
	var evidence tundiag.DNSEvidence
	var err error
	if tcp {
		evidence, err = client.DNSTCP(ctx, server, "example.com", tundiag.DNSRecordTypeA)
	} else {
		evidence, err = client.DNSUDP(ctx, server, "example.com", tundiag.DNSRecordTypeA)
	}
	result := tundiag.ProbeResult{Evidence: tundiag.Evidence{DNS: &evidence}}
	if err != nil {
		result.Status = tundiag.ProbeFail
		if tcp {
			result.Classification = tundiag.ClassDNSTCPFailure
		} else {
			result.Classification = tundiag.ClassDNSUDPFailure
		}
		result.Error = err.Error()
		return result
	}
	if evidence.ResponseCode != tundiag.DNSRCodeSuccess || len(evidence.Addresses) == 0 {
		result.Status = tundiag.ProbeFail
		if tcp {
			result.Classification = tundiag.ClassDNSTCPFailure
		} else {
			result.Classification = tundiag.ClassDNSUDPFailure
		}
		result.Error = fmt.Sprintf("DNS response code=%d addresses=%d", evidence.ResponseCode, len(evidence.Addresses))
		return result
	}
	result.Status = tundiag.ProbePass
	return result
}

func probeTunSystemResolver(ctx context.Context, client tundiag.NetworkClient, name string) tundiag.ProbeResult {
	addresses, err := client.Resolve(ctx, name)
	if err != nil {
		return tundiag.ProbeResult{Status: tundiag.ProbeFail, Classification: tundiag.ClassDNSResolutionFailure, Error: err.Error()}
	}
	return tundiag.ProbeResult{Status: tundiag.ProbePass, Evidence: tundiag.Evidence{ResolvedAddresses: addresses}}
}

func probeTunNXDomain(ctx context.Context, client tundiag.NetworkClient) tundiag.ProbeResult {
	addresses, err := client.Resolve(ctx, "podlaz-diagnostic.invalid")
	if client.IsNXDomain(err) {
		return tundiag.ProbeResult{Status: tundiag.ProbePass, Evidence: tundiag.Evidence{Notes: []string{"reserved .invalid name returned NXDOMAIN"}}}
	}
	if err != nil {
		return tundiag.ProbeResult{Status: tundiag.ProbeFail, Classification: tundiag.ClassDNSResolutionFailure, Error: err.Error()}
	}
	return tundiag.ProbeResult{Status: tundiag.ProbeFail, Classification: tundiag.ClassDNSHijackDetected, Error: "reserved .invalid name resolved unexpectedly", Evidence: tundiag.Evidence{ResolvedAddresses: addresses}}
}

func probeTunTCP(ctx context.Context, client tundiag.NetworkClient) tundiag.ProbeResult {
	duration, err := client.TCP(ctx, "www.cloudflare.com", 443)
	if err != nil {
		return diagnosticFailure(tundiag.ClassTCP443Failure, err.Error())
	}
	return tundiag.ProbeResult{Status: tundiag.ProbePass, DurationMS: duration.Milliseconds(), Evidence: tundiag.Evidence{Endpoint: "www.cloudflare.com:443"}}
}

func probeTunTLS(ctx context.Context, client tundiag.NetworkClient) tundiag.ProbeResult {
	evidence, err := client.TLS(ctx, "www.cloudflare.com", 443)
	if err != nil {
		return tundiag.ProbeResult{Status: tundiag.ProbeFail, Classification: tundiag.ClassTLSFailure, Error: err.Error(), Evidence: tundiag.Evidence{TLS: &evidence}}
	}
	return tundiag.ProbeResult{Status: tundiag.ProbePass, Evidence: tundiag.Evidence{TLS: &evidence}}
}

func probeTunHTTPS(ctx context.Context, client tundiag.NetworkClient, targetID string, classification tundiag.Classification) tundiag.ProbeResult {
	target, ok := tundiag.FindTarget(targetID)
	if !ok {
		return diagnosticFailure(tundiag.ClassInternalDiagnosticError, "missing endpoint catalog target "+targetID)
	}
	evidence, err := client.HTTPS(ctx, target)
	result := tundiag.ProbeResult{Evidence: tundiag.Evidence{Endpoint: target.URL, HTTP: &evidence}}
	if err != nil {
		result.Status = tundiag.ProbeFail
		result.Classification = classification
		result.Error = err.Error()
		return result
	}
	if target.Kind == tundiag.TargetPMTU && evidence.BytesRead < target.MaxResponseBytes {
		result.Status = tundiag.ProbeFail
		result.Classification = classification
		result.Error = fmt.Sprintf("bounded transfer returned %d of %d requested bytes", evidence.BytesRead, target.MaxResponseBytes)
		return result
	}
	result.Status = tundiag.ProbePass
	return result
}

func probeTunDoH(ctx context.Context, targetID string) tundiag.ProbeResult {
	target, ok := tundiag.FindTarget(targetID)
	if !ok {
		return diagnosticFailure(tundiag.ClassInternalDiagnosticError, "missing endpoint catalog target "+targetID)
	}
	client := tundiag.NetworkClient{DialContext: tunDiagnosticBootstrapDialer(target)}
	dnsEvidence, httpEvidence, err := client.DoH(ctx, target, "example.com", tundiag.DNSRecordTypeA)
	result := tundiag.ProbeResult{Evidence: tundiag.Evidence{Endpoint: target.URL, DNS: &dnsEvidence, HTTP: &httpEvidence}}
	if err != nil {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassDoHFailure
		result.Error = err.Error()
		return result
	}
	if dnsEvidence.ResponseCode != tundiag.DNSRCodeSuccess || len(dnsEvidence.Addresses) == 0 {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassDoHFailure
		result.Error = fmt.Sprintf("DoH response code=%d addresses=%d", dnsEvidence.ResponseCode, len(dnsEvidence.Addresses))
		return result
	}
	result.Status = tundiag.ProbePass
	return result
}

func probeTunIPv6(ctx context.Context, plan planner.TunPlan, snapshot netsnapshot.Snapshot) tundiag.ProbeResult {
	command, _ := tunDiagnosticCommandRunner(ctx, "ip", "-6", "-brief", "address", "show")
	evidence := tundiag.IPv6Evidence{
		State:            string(snapshot.IPv6.Status),
		DefaultInterface: snapshot.DefaultIPv6.Interface,
		RouteTable:       snapshot.DefaultIPv6.Raw,
	}
	evidence.UplinkAddresses, evidence.TunAddresses = parseTunDiagnosticIPv6Addresses(command.stdout, snapshot.DefaultIPv4.Interface, emptyAs(plan.TunDevice.Name, netsnapshot.DefaultTunName))
	result := tundiag.ProbeResult{Evidence: tundiag.Evidence{IPv6: &evidence, Commands: []tundiag.CommandEvidence{commandEvidence(command)}}}
	if snapshot.IPv6.Status != netsnapshot.StatusDetected {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassIPv6NotPresent
		result.Error = "host has no detected IPv6 path"
		return result
	}
	if snapshot.DefaultIPv6.Status != netsnapshot.StatusDetected {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassIPv6Unusable
		result.Error = "IPv6 is present but no usable default route was detected"
		return result
	}
	tunName := emptyAs(plan.TunDevice.Name, netsnapshot.DefaultTunName)
	if snapshot.DefaultIPv6.Interface != "" && snapshot.DefaultIPv6.Interface != tunName {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassIPv6Leak
		result.Error = "IPv6 default route bypasses " + tunName + " via " + snapshot.DefaultIPv6.Interface
		return result
	}
	result.Status = tundiag.ProbePass
	return result
}

func lookupTunRoute(ctx context.Context, family, target string) (tundiag.RouteEvidence, tundiag.CommandEvidence, error) {
	result, err := tunDiagnosticCommandRunner(ctx, "ip", family, "route", "get", target)
	evidence := parseTunDiagnosticRoute(result.stdout)
	evidence.Destination = target
	return evidence, commandEvidence(result), err
}

func parseTunDiagnosticRoute(output string) tundiag.RouteEvidence {
	line := strings.TrimSpace(strings.Split(output, "\n")[0])
	fields := strings.Fields(line)
	evidence := tundiag.RouteEvidence{Raw: line}
	for i := 0; i < len(fields)-1; i++ {
		switch fields[i] {
		case "dev":
			evidence.Interface = fields[i+1]
		case "via":
			evidence.Gateway = fields[i+1]
		case "table":
			evidence.Table = fields[i+1]
		}
	}
	return evidence
}

func tunDiagnosticHasPolicyRule(output string, priority int) bool {
	needle := strconv.Itoa(priority) + ":"
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, needle) && (strings.Contains(line, "lookup podlaz") || strings.Contains(line, "lookup "+netsnapshot.DefaultRouteTableID)) {
			return true
		}
	}
	return false
}

func tunDiagnosticBootstrapDialer(target tundiag.Target) tundiag.DialContextFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if len(target.BootstrapAddresses) == 0 {
			return (&net.Dialer{}).DialContext(ctx, network, address)
		}
		_, port, err := net.SplitHostPort(address)
		if err != nil {
			port = strconv.Itoa(int(target.Port))
		}
		var errs []error
		for _, ip := range target.BootstrapAddresses {
			conn, dialErr := (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(ip, port))
			if dialErr == nil {
				return conn, nil
			}
			errs = append(errs, dialErr)
		}
		return nil, errors.Join(errs...)
	}
}

func parseTunDiagnosticIPv6Addresses(output, uplink, tunName string) ([]string, []string) {
	var uplinkAddresses []string
	var tunAddresses []string
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		name := strings.TrimSuffix(fields[0], ":")
		for _, field := range fields[2:] {
			if !strings.Contains(field, ":") {
				continue
			}
			if name == uplink {
				uplinkAddresses = append(uplinkAddresses, field)
			}
			if name == tunName {
				tunAddresses = append(tunAddresses, field)
			}
		}
	}
	return uplinkAddresses, tunAddresses
}

func diagnosticFailure(classification tundiag.Classification, message string) tundiag.ProbeResult {
	return tundiag.ProbeResult{Status: tundiag.ProbeFail, Classification: classification, Error: message}
}

func tunDiagnosticContains(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), want) {
			return true
		}
	}
	return false
}

func readInterfaceMTU(name string) int {
	if strings.TrimSpace(name) == "" || strings.ContainsAny(name, `/\\`) {
		return 0
	}
	data, err := os.ReadFile(filepath.Join("/sys/class/net", name, "mtu"))
	if err != nil {
		return 0
	}
	mtu, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || mtu <= 0 {
		return 0
	}
	return mtu
}

func runTunDiagnosticCommand(ctx context.Context, name string, args ...string) (tunDiagnosticCommandResult, error) {
	command := exec.CommandContext(ctx, name, args...)
	stdout := newTunDiagnosticCappedBuffer(tunDiagnosticCommandOutputLimit)
	stderr := newTunDiagnosticCappedBuffer(tunDiagnosticCommandOutputLimit)
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	result := tunDiagnosticCommandResult{command: strings.TrimSpace(name + " " + strings.Join(args, " ")), stdout: stdout.String(), stderr: stderr.String()}
	if err == nil {
		return result, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		result.exitCode = exitError.ExitCode()
	} else {
		result.exitCode = -1
	}
	return result, fmt.Errorf("%s failed: %w", result.command, err)
}

func commandEvidence(result tunDiagnosticCommandResult) tundiag.CommandEvidence {
	return tundiag.CommandEvidence{Command: result.command, Stdout: result.stdout, Stderr: result.stderr, ExitCode: result.exitCode}
}

type tunDiagnosticCappedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func newTunDiagnosticCappedBuffer(limit int) *tunDiagnosticCappedBuffer {
	return &tunDiagnosticCappedBuffer{limit: limit}
}

func (b *tunDiagnosticCappedBuffer) Write(data []byte) (int, error) {
	originalLength := len(data)
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		_, _ = b.buffer.Write(data)
	}
	return originalLength, nil
}

func (b *tunDiagnosticCappedBuffer) String() string {
	return b.buffer.String()
}

var _ io.Writer = (*tunDiagnosticCappedBuffer)(nil)
