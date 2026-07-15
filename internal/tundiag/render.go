package tundiag

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func WriteJSON(w io.Writer, report Report) error {
	report = Finalize(report)
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(report)
}

func RenderHuman(report Report, verbose bool) string {
	report = Finalize(report)
	var b strings.Builder
	b.WriteString("TUN diagnostics\n")
	fmt.Fprintf(&b, "  Status        %s\n", report.Status)
	fmt.Fprintf(&b, "  Session       %s\n", emptyAs(report.Session.State, "unknown"))
	if report.Session.Mode != "" {
		fmt.Fprintf(&b, "  Mode          %s\n", report.Session.Mode)
	}
	if report.Session.ProfileName != "" {
		fmt.Fprintf(&b, "  Profile       %s\n", report.Session.ProfileName)
	}
	if report.Session.Interface != "" || report.Network.TunInterface != "" {
		fmt.Fprintf(&b, "  Interface     %s\n", emptyAs(report.Network.TunInterface, report.Session.Interface))
	}
	if report.Network.TunMTU > 0 || report.Network.UplinkMTU > 0 {
		fmt.Fprintf(&b, "  MTU           tun=%s uplink=%s\n", integerOrDash(report.Network.TunMTU), integerOrDash(report.Network.UplinkMTU))
	}
	if len(report.Network.DNSServers) > 0 {
		fmt.Fprintf(&b, "  DNS           %s\n", strings.Join(report.Network.DNSServers, ", "))
	}
	if report.Network.IPv4Status != "" || report.Network.IPv6Status != "" {
		fmt.Fprintf(&b, "  IP state      IPv4=%s IPv6=%s\n", emptyAs(report.Network.IPv4Status, "unknown"), emptyAs(report.Network.IPv6Status, "unknown"))
	}
	if verbose {
		renderVerboseNetwork(&b, report.Network)
	}
	if report.Historical {
		b.WriteString("  Report        saved result; live re-probing unavailable\n")
	}
	b.WriteString("\nChecks\n")
	for _, probe := range report.Probes {
		fmt.Fprintf(&b, "  %-7s %-24s", strings.ToUpper(string(probe.Status)), probe.ID)
		if probe.DurationMS > 0 {
			fmt.Fprintf(&b, " %d ms", probe.DurationMS)
		}
		if probe.Classification != "" && probe.Status != ProbePass {
			fmt.Fprintf(&b, "  %s", probe.Classification)
		}
		b.WriteByte('\n')
		if probe.Status == ProbeSkipped && probe.DependencyReason != "" {
			fmt.Fprintf(&b, "           skipped: %s\n", probe.DependencyReason)
		}
		if verbose {
			renderVerboseEvidence(&b, probe)
		}
	}
	b.WriteString("\nPrimary classification\n")
	if report.PrimaryClassification == "" {
		b.WriteString("  none\n")
	} else {
		fmt.Fprintf(&b, "  %s\n", report.PrimaryClassification)
	}
	if len(report.Warnings) > 0 {
		b.WriteString("\nWarnings\n")
		for _, warning := range report.Warnings {
			fmt.Fprintf(&b, "  %s\n", warning)
		}
	}
	if len(report.Errors) > 0 {
		b.WriteString("\nErrors\n")
		for _, diagnosticError := range report.Errors {
			fmt.Fprintf(&b, "  %s\n", diagnosticError)
		}
	}
	if report.ReportPath != "" {
		fmt.Fprintf(&b, "\nLast report\n  %s\n", report.ReportPath)
	}
	if report.PrimaryClassification != "" {
		fmt.Fprintf(&b, "\nNext step\n  %s\n", Guidance(report.PrimaryClassification))
	}
	return b.String()
}

func renderVerboseNetwork(b *strings.Builder, network Network) {
	if network.PhysicalInterface != "" {
		fmt.Fprintf(b, "  Uplink        %s", network.PhysicalInterface)
		if network.SSID != "" {
			fmt.Fprintf(b, " (%s)", network.SSID)
		}
		b.WriteByte('\n')
	}
	if network.Gateway != "" {
		fmt.Fprintf(b, "  Gateway       %s\n", network.Gateway)
	}
	if len(network.LocalAddresses) > 0 {
		fmt.Fprintf(b, "  Local IPs     %s\n", strings.Join(network.LocalAddresses, ", "))
	}
	if network.ServerEndpoint != "" {
		fmt.Fprintf(b, "  VPN server    %s\n", network.ServerEndpoint)
	}
	if len(network.ServerAddresses) > 0 {
		fmt.Fprintf(b, "  Server IPs    %s\n", strings.Join(network.ServerAddresses, ", "))
	}
	if len(network.DoHProviders) > 0 {
		fmt.Fprintf(b, "  DoH targets   %s\n", strings.Join(network.DoHProviders, ", "))
	}
}

func renderVerboseEvidence(b *strings.Builder, probe ProbeResult) {
	if probe.Target != "" {
		fmt.Fprintf(b, "           target: %s\n", probe.Target)
	}
	if probe.Error != "" {
		fmt.Fprintf(b, "           error: %s\n", probe.Error)
	}
	if probe.Evidence.Endpoint != "" {
		fmt.Fprintf(b, "           endpoint: %s\n", probe.Evidence.Endpoint)
	}
	if len(probe.Evidence.ResolvedAddresses) > 0 {
		fmt.Fprintf(b, "           resolved: %s\n", strings.Join(probe.Evidence.ResolvedAddresses, ", "))
	}
	if route := probe.Evidence.Route; route != nil {
		fmt.Fprintf(b, "           route: destination=%s dev=%s via=%s table=%s rule=%s\n", emptyAs(route.Destination, "-"), emptyAs(route.Interface, "-"), emptyAs(route.Gateway, "-"), emptyAs(route.Table, "-"), emptyAs(route.Rule, "-"))
	}
	if dns := probe.Evidence.DNS; dns != nil {
		fmt.Fprintf(b, "           dns: server=%s name=%s rcode=%d answers=%s\n", emptyAs(dns.Server, "-"), emptyAs(dns.Name, "-"), dns.ResponseCode, strings.Join(dns.Addresses, ","))
	}
	if tls := probe.Evidence.TLS; tls != nil {
		fmt.Fprintf(b, "           tls: version=%s cipher=%s subject=%s issuer=%s handshake=%d ms\n", emptyAs(tls.Version, "-"), emptyAs(tls.Cipher, "-"), emptyAs(tls.PeerSubject, "-"), emptyAs(tls.PeerIssuer, "-"), tls.HandshakeMS)
	}
	if http := probe.Evidence.HTTP; http != nil {
		fmt.Fprintf(b, "           http: status=%d location=%s content_length=%d bytes=%d header=%d ms body=%d ms\n", http.StatusCode, emptyAs(http.Location, "-"), http.ContentLength, http.BytesRead, http.HeaderMS, http.BodyMS)
	}
	if ipv6 := probe.Evidence.IPv6; ipv6 != nil {
		fmt.Fprintf(b, "           ipv6: state=%s default_dev=%s table=%s uplink=%s tun=%s\n", emptyAs(ipv6.State, "-"), emptyAs(ipv6.DefaultInterface, "-"), emptyAs(ipv6.RouteTable, "-"), strings.Join(ipv6.UplinkAddresses, ","), strings.Join(ipv6.TunAddresses, ","))
	}
	for _, command := range probe.Evidence.Commands {
		fmt.Fprintf(b, "           command: %s (exit %d)\n", command.Command, command.ExitCode)
		if command.Stdout != "" {
			fmt.Fprintf(b, "             stdout: %s\n", command.Stdout)
		}
		if command.Stderr != "" {
			fmt.Fprintf(b, "             stderr: %s\n", command.Stderr)
		}
	}
	for _, note := range probe.Evidence.Notes {
		fmt.Fprintf(b, "           note: %s\n", note)
	}
}

func Guidance(classification Classification) string {
	switch classification {
	case ClassSessionInactive:
		return "connect a TUN session, or inspect the saved last report"
	case ClassServerBypassFailure:
		return "inspect the VPN server bypass route before retrying the connection"
	case ClassRouteFailure, ClassPolicyRuleFailure:
		return "inspect TUN routes and policy rules with podlaz doctor --tun --verbose"
	case ClassDNSApplyFailure, ClassForeignDNSConflict:
		return "inspect systemd-resolved link ownership and route-only DNS domains"
	case ClassDNSUDPFailure, ClassDNSTCPFailure, ClassDNSResolutionFailure, ClassDNSHijackDetected:
		return "inspect per-link DNS routing and compare UDP, TCP, and system resolver evidence"
	case ClassTCP443Failure:
		return "inspect egress filtering and TCP/443 reachability"
	case ClassTLSFailure:
		return "inspect TLS certificate validation, clock, and interception evidence"
	case ClassHTTPSFailure:
		return "inspect HTTP header/body timings and endpoint-specific results"
	case ClassDoHFailure, ClassDoHPartialFailure:
		return "compare the independent DoH provider results"
	case ClassIPv6Leak, ClassIPv6Unusable:
		return "inspect IPv6 addresses and the selected IPv6 route; podlaz made no IPv6 changes"
	case ClassLikelyPMTUBlackhole:
		return "compare small and bounded larger HTTPS transfers before changing MTU manually"
	case ClassTimeout, ClassCancelled:
		return "rerun the bounded diagnostics and inspect which dependency stopped progress"
	default:
		return "run podlaz doctor --tun --verbose and inspect the saved report"
	}
}

func emptyAs(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func integerOrDash(value int) string {
	if value <= 0 {
		return "-"
	}
	return fmt.Sprintf("%d", value)
}
