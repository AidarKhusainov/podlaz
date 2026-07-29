// Package tundiag contains the bounded, read-only diagnostic model used by
// daemon-side TUN probes and CLI renderers.
package tundiag

import "time"

const (
	SchemaVersion = 1

	StatusHealthy     OverallStatus = "healthy"
	StatusDegraded    OverallStatus = "degraded"
	StatusUnhealthy   OverallStatus = "unhealthy"
	StatusUnavailable OverallStatus = "unavailable"

	ProbePass    ProbeStatus = "pass"
	ProbeFail    ProbeStatus = "fail"
	ProbeSkipped ProbeStatus = "skipped"
)

type OverallStatus string
type ProbeStatus string
type Classification string
type Layer string
type FailurePhase string

const (
	LayerSession Layer = "session"
	LayerBypass  Layer = "server_bypass"
	LayerRoute   Layer = "route"
	LayerDNS     Layer = "dns"
	LayerTCP     Layer = "tcp"
	LayerTLS     Layer = "tls"
	LayerHTTPS   Layer = "https"
	LayerDoH     Layer = "doh"
	LayerIPv6    Layer = "ipv6"
	LayerPMTU    Layer = "pmtu"
)

const (
	FailurePhaseDNSResolution FailurePhase = "dns_resolution"
	FailurePhaseRouteLookup   FailurePhase = "route_lookup"
	FailurePhaseTCPConnect    FailurePhase = "tcp_connect"
	FailurePhaseTLSHandshake  FailurePhase = "tls_handshake"
	FailurePhaseHTTPRequest   FailurePhase = "http_request"
	FailurePhaseHTTPResponse  FailurePhase = "http_response"
	FailurePhaseHTTPBody      FailurePhase = "http_body"
)

const (
	ClassSessionInactive             Classification = "session_inactive"
	ClassSessionMetadataInconsistent Classification = "session_metadata_inconsistent"
	ClassOwnershipMismatch           Classification = "ownership_mismatch"
	ClassServerBypassFailure         Classification = "server_bypass_failure"
	ClassRouteFailure                Classification = "route_failure"
	ClassPolicyRuleFailure           Classification = "policy_rule_failure"
	ClassDNSApplyFailure             Classification = "dns_apply_failure"
	ClassForeignDNSConflict          Classification = "foreign_dns_conflict"
	ClassDNSUDPFailure               Classification = "dns_udp_failure"
	ClassDNSTCPFailure               Classification = "dns_tcp_failure"
	ClassDNSResolutionFailure        Classification = "dns_resolution_failure"
	ClassDNSHijackDetected           Classification = "dns_hijack_detected"
	ClassTCP443Failure               Classification = "tcp_443_failure"
	ClassTLSFailure                  Classification = "tls_failure"
	ClassHTTPSFailure                Classification = "https_failure"
	ClassHTTPSPartialFailure         Classification = "https_partial_failure"
	ClassDoHPartialFailure           Classification = "doh_partial_failure"
	ClassDoHFailure                  Classification = "doh_failure"
	ClassIPv6NotPresent              Classification = "ipv6_not_present"
	ClassIPv6Unusable                Classification = "ipv6_unusable"
	ClassIPv6Leak                    Classification = "ipv6_leak"
	ClassLikelyPMTUBlackhole         Classification = "likely_pmtu_blackhole"
	ClassTimeout                     Classification = "timeout"
	ClassCancelled                   Classification = "cancelled"
	ClassInternalDiagnosticError     Classification = "internal_diagnostic_error"
)

// Report is the stable schema shared by daemon persistence, human output, and JSON output.
type Report struct {
	SchemaVersion         int            `json:"schema_version"`
	Status                OverallStatus  `json:"status"`
	PrimaryClassification Classification `json:"primary_classification,omitempty"`
	GeneratedAt           time.Time      `json:"generated_at"`
	Historical            bool           `json:"historical,omitempty"`
	FailurePhase          string         `json:"failure_phase,omitempty"`
	RollbackStatus        string         `json:"rollback_status,omitempty"`
	Session               Session        `json:"session"`
	Network               Network        `json:"network"`
	Probes                []ProbeResult  `json:"probes"`
	Warnings              []string       `json:"warnings"`
	Errors                []string       `json:"errors"`
	ReportPath            string         `json:"report_path,omitempty"`
}

type Session struct {
	State          string `json:"state"`
	Mode           string `json:"mode,omitempty"`
	ProfileName    string `json:"profile_name,omitempty"`
	TransactionID  string `json:"transaction_id,omitempty"`
	CoreRunning    bool   `json:"core_running"`
	Interface      string `json:"interface,omitempty"`
	MetadataSource string `json:"metadata_source,omitempty"`
}

type Network struct {
	PhysicalInterface string   `json:"physical_interface,omitempty"`
	SSID              string   `json:"ssid,omitempty"`
	Gateway           string   `json:"gateway,omitempty"`
	LocalAddresses    []string `json:"local_addresses,omitempty"`
	TunInterface      string   `json:"tun_interface,omitempty"`
	TunMTU            int      `json:"tun_mtu,omitempty"`
	UplinkMTU         int      `json:"uplink_mtu,omitempty"`
	DNSServers        []string `json:"dns_servers,omitempty"`
	IPv4Status        string   `json:"ipv4_status,omitempty"`
	IPv6Status        string   `json:"ipv6_status,omitempty"`
	ServerEndpoint    string   `json:"server_endpoint,omitempty"`
	ServerHostname    string   `json:"server_hostname,omitempty"`
	ServerName        string   `json:"server_name,omitempty"`
	ServerAddresses   []string `json:"server_addresses,omitempty"`
	DoHProviders      []string `json:"doh_providers,omitempty"`
	NftablesStatus    string   `json:"nftables_status,omitempty"`
}

type ProbeResult struct {
	ID               string         `json:"id"`
	Layer            Layer          `json:"layer"`
	Status           ProbeStatus    `json:"status"`
	DurationMS       int64          `json:"duration_ms"`
	TimeoutMS        int64          `json:"timeout_ms"`
	Target           string         `json:"target,omitempty"`
	Classification   Classification `json:"classification,omitempty"`
	FailurePhase     FailurePhase   `json:"failure_phase,omitempty"`
	Error            string         `json:"error,omitempty"`
	DependencyReason string         `json:"dependency_reason,omitempty"`
	Evidence         Evidence       `json:"evidence,omitempty"`
}

type Evidence struct {
	Endpoint          string            `json:"endpoint,omitempty"`
	ResolvedAddresses []string          `json:"resolved_addresses,omitempty"`
	Route             *RouteEvidence    `json:"route,omitempty"`
	DNS               *DNSEvidence      `json:"dns,omitempty"`
	TLS               *TLSEvidence      `json:"tls,omitempty"`
	HTTP              *HTTPEvidence     `json:"http,omitempty"`
	IPv6              *IPv6Evidence     `json:"ipv6,omitempty"`
	Commands          []CommandEvidence `json:"commands,omitempty"`
	PolicyRules       []string          `json:"policy_rules,omitempty"`
	NftablesRules     []string          `json:"nftables_rules,omitempty"`
	Notes             []string          `json:"notes,omitempty"`
}

type RouteEvidence struct {
	Destination string `json:"destination,omitempty"`
	Interface   string `json:"interface,omitempty"`
	Gateway     string `json:"gateway,omitempty"`
	Table       string `json:"table,omitempty"`
	Rule        string `json:"rule,omitempty"`
	Raw         string `json:"raw,omitempty"`
}

type DNSEvidence struct {
	Server       string   `json:"server,omitempty"`
	Name         string   `json:"name,omitempty"`
	Type         uint16   `json:"type,omitempty"`
	ResponseCode int      `json:"response_code,omitempty"`
	Addresses    []string `json:"addresses,omitempty"`
	MessageID    uint16   `json:"message_id,omitempty"`
}

type TLSEvidence struct {
	Version     string `json:"version,omitempty"`
	Cipher      string `json:"cipher,omitempty"`
	PeerSubject string `json:"peer_subject,omitempty"`
	PeerIssuer  string `json:"peer_issuer,omitempty"`
	HandshakeMS int64  `json:"handshake_ms,omitempty"`
}

type HTTPEvidence struct {
	StatusCode       int    `json:"status_code,omitempty"`
	Location         string `json:"location,omitempty"`
	ContentLength    int64  `json:"content_length,omitempty"`
	BytesRead        int64  `json:"bytes_read,omitempty"`
	HeaderMS         int64  `json:"header_ms,omitempty"`
	BodyMS           int64  `json:"body_ms,omitempty"`
	ResponseAccepted bool   `json:"response_accepted,omitempty"`
	FailurePhase     string `json:"failure_phase,omitempty"`
}

type IPv6Evidence struct {
	State            string   `json:"state,omitempty"`
	UplinkAddresses  []string `json:"uplink_addresses,omitempty"`
	TunAddresses     []string `json:"tun_addresses,omitempty"`
	DefaultInterface string   `json:"default_interface,omitempty"`
	RouteTable       string   `json:"route_table,omitempty"`
}

type CommandEvidence struct {
	Command  string `json:"command,omitempty"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	ExitCode int    `json:"exit_code,omitempty"`
}

func (r Report) HasFailures() bool {
	return r.Status == StatusUnhealthy || r.Status == StatusUnavailable
}

func (r Report) Probe(id string) (ProbeResult, bool) {
	for _, probe := range r.Probes {
		if probe.ID == id {
			return probe, true
		}
	}
	return ProbeResult{}, false
}
